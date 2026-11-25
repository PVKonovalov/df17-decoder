package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/coverage"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/playback"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/tape"
	"github.com/gorilla/websocket"
)

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
}

type wsIn struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func readWS(t *testing.T, c *websocket.Conn) wsIn {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var m wsIn
	if err := c.ReadJSON(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestWS_Updates(t *testing.T) {
	srv := httptest.NewServer(newFixture(t).handler)
	defer srv.Close()
	c, _, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	m := readWS(t, c)
	if m.Type != "receiver" || string(m.Data) != `{"lat":25.07,"lon":22.87,"maxRangeNm":300}` {
		t.Fatalf("first message = %s %s", m.Type, m.Data)
	}
	m = readWS(t, c)
	if want := `{"tileUrl":"http://tiles.lan/{z}/{x}/{y}.png","attribution":"lan","maxZoom":17,"darkFilter":false,"waypointLabelMinZoom":0,"waypoints":[]}`; m.Type != "map" || string(m.Data) != want {
		t.Fatalf("second message = %s %s", m.Type, m.Data)
	}
	m = readWS(t, c)
	if m.Type != "trails" || !strings.HasPrefix(string(m.Data), `{"window":"10m0s","trails":{"738001":[{"t":`) {
		t.Fatalf("third message = %s %s", m.Type, m.Data)
	}
	m = readWS(t, c)
	var cov coverage.Snapshot
	if err := json.Unmarshal(m.Data, &cov); m.Type != "coverage" || err != nil || len(cov.Points) < 2 || cov.Receiver.Lat != 25.07 {
		t.Fatalf("fourth message = %s %s (err %v)", m.Type, m.Data, err)
	}
	m = readWS(t, c)
	var grid coverage.Grid
	if err := json.Unmarshal(m.Data, &grid); m.Type != "coverageGrid" || err != nil || len(grid.Bands) != 4 {
		t.Fatalf("fifth message = %s %s (err %v)", m.Type, m.Data, err)
	}

	// aircraft and stats every 20 ms, coverage every 30 ms, interleaved
	counts := map[string]int{}
	for counts["aircraft"] < 3 || counts["coverage"] < 2 {
		m = readWS(t, c)
		counts[m.Type]++
		switch m.Type {
		case "aircraft":
			var list []map[string]any
			if err := json.Unmarshal(m.Data, &list); err != nil || len(list) != 3 || list[0]["icao"] != "738001" {
				t.Fatalf("aircraft message = %.200s (err %v)", m.Data, err)
			}
			m = readWS(t, c)
			var st statsResponse
			if err := json.Unmarshal(m.Data, &st); m.Type != "stats" || err != nil || st.Aircraft != 3 || st.Source.URL != "tcp://test:30002" {
				t.Fatalf("stats message after aircraft = %s %s (err %v)", m.Type, m.Data, err)
			}
		case "coverage", "trailPoints":
		default:
			t.Fatalf("unexpected message %s", m.Type)
		}
	}
}

func TestWS_TrailsThenOnlyNewPoints(t *testing.T) {
	f := newFixture(t)
	srv := httptest.NewServer(f.handler)
	defer srv.Close()
	c, _, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	type point struct {
		T   int64   `json:"t"`
		Lat float64 `json:"lat"`
		Alt *int    `json:"alt"`
	}
	next := func(typ string) json.RawMessage {
		t.Helper()
		for {
			if m := readWS(t, c); m.Type == typ {
				return m.Data
			}
		}
	}
	var trails struct {
		Window string             `json:"window"`
		Trails map[string][]point `json:"trails"`
	}
	if err := json.Unmarshal(next("trails"), &trails); err != nil {
		t.Fatal(err)
	}
	if len(trails.Trails) != 2 || len(trails.Trails["738001"]) != 1 || len(trails.Trails["738002"]) != 1 {
		t.Fatalf("trails = %+v", trails)
	}
	if p := trails.Trails["738002"][0]; p.Alt == nil || *p.Alt != 12000 {
		t.Errorf("738002 point = %+v", p)
	}
	if got := string(next("trailPoints")); got != "{}" {
		t.Errorf("first trailPoints = %s, want {} (nothing new)", got)
	}

	last := trails.Trails["738001"][0].T
	at := time.UnixMilli(last).Add(6 * time.Second)
	f.tracker.Update(modes.Message{DF: 17, ICAO: 0x738001, Time: at, TC: 11, AirbornePosition: &modes.AirbornePosition{
		Altitude: new(1000), CPR: modes.EncodeCPR(25.25, 22.87, false),
	}}, nil)

	var pts map[string][]point
	for {
		if err := json.Unmarshal(next("trailPoints"), &pts); err != nil {
			t.Fatal(err)
		}
		if len(pts) > 0 {
			break
		}
	}
	if len(pts) != 1 || len(pts["738001"]) != 1 || pts["738001"][0].T != at.UnixMilli() {
		t.Errorf("new points = %+v", pts)
	}
	if got := string(next("trailPoints")); got != "{}" {
		t.Errorf("trailPoints after the new point = %s, want {} (nothing resent)", got)
	}
}

func TestWS_RejectsBadRequests(t *testing.T) {
	srv := httptest.NewServer(newFixture(t).handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/ws")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("plain GET: code %d, content type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	_, resp, err = websocket.DefaultDialer.Dial(wsURL(srv), http.Header{"Origin": {"http://evil.example"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin: err %v, resp %v", err, resp)
	}
}

func TestWS_ServerShutdownSendsGoingAway(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	srv := httptest.NewUnstartedServer(newFixture(t).handler)
	srv.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	srv.Start()
	defer srv.Close()

	c, _, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	readWS(t, c)
	cancel()

	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, _, err := c.ReadMessage()
		if err == nil {
			continue
		}
		var ce *websocket.CloseError
		if !errors.As(err, &ce) || ce.Code != websocket.CloseGoingAway {
			t.Fatalf("err = %v, want close 1001", err)
		}
		return
	}
}

func TestWS_ClientCloseEndsHandler(t *testing.T) {
	done := make(chan struct{})
	h := newFixture(t).handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		if r.URL.Path == "/api/ws" {
			close(done)
		}
	}))
	defer srv.Close()

	c, _, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	readWS(t, c)
	_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	c.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler still running after client closed")
	}
}

// newPlaybackFixture adds playback over a tape of undecodable frames every 10 s from 10 to 1 minutes ago.
func newPlaybackFixture(t *testing.T, maxSessions int) (*httptest.Server, time.Time) {
	t.Helper()
	f := newFixture(t)
	dir := filepath.Join(t.TempDir(), "tape")
	rec, err := tape.New(tape.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	for i := range 55 {
		rec.Record(source.Frame{Data: make([]byte, 7), Time: start.Add(time.Duration(i) * 10 * time.Second)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec.Run(ctx)
	st := rec.Stats()
	f.handler = New(Deps{
		Receiver: Receiver{Lat: 25.07, Lon: 22.87, MaxRangeNm: 300},
		Tracker:  f.tracker, Stats: f.stats, Source: func() source.StatsSnapshot { return source.StatsSnapshot{} },
		StreamInterval: 50 * time.Millisecond,
		Playback: &playback.Manager{
			Tape: tape.Open(dir), Tracker: f.tracker.Options(), MaxSessions: maxSessions, EndMargin: time.Second,
			Bounds: func() (time.Time, time.Time) { return st.Oldest, st.Newest },
		},
	})
	srv := httptest.NewServer(f.handler)
	t.Cleanup(srv.Close)
	return srv, start
}

func dialWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func sendControl(t *testing.T, c *websocket.Conn, raw string) {
	t.Helper()
	if err := c.WriteMessage(websocket.TextMessage, []byte(raw)); err != nil {
		t.Fatal(err)
	}
}

// until reads messages until one of type typ arrives.
func until(t *testing.T, c *websocket.Conn, typ string) wsIn {
	t.Helper()
	for range 200 {
		if m := readWS(t, c); m.Type == typ {
			return m
		}
	}
	t.Fatalf("no %q message", typ)
	return wsIn{}
}

func playbackOf(t *testing.T, m wsIn) wsPlayback {
	t.Helper()
	var p wsPlayback
	if err := json.Unmarshal(m.Data, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWS_PlaybackSwitchesTheFeed(t *testing.T) {
	srv, start := newPlaybackFixture(t, 2)
	c := dialWS(t, srv)
	if p := playbackOf(t, until(t, c, "playback")); p.Mode != "live" || p.TapeStart == nil || !p.TapeStart.Equal(start) {
		t.Fatalf("live status %+v", p)
	}

	at := start.Add(5 * time.Minute).UTC().Format(time.RFC3339)
	sendControl(t, c, `{"type":"playback","data":{"action":"seek","t":"`+at+`"}}`)
	m := until(t, c, "trails")
	if string(m.Data) != `{"window":"10m0s","trails":{}}` {
		t.Errorf("playback trails %s", m.Data)
	}
	// right after the full trails: deltas, then the session's aircraft, live stats and the status
	for _, want := range []string{"trailPoints", "aircraft", "stats", "playback"} {
		m = readWS(t, c)
		if m.Type != want {
			t.Fatalf("got %s, want %s", m.Type, want)
		}
		switch want {
		case "aircraft":
			if string(m.Data) != "[]" {
				t.Errorf("playback aircraft %s", m.Data)
			}
		case "stats":
			var st statsResponse
			if err := json.Unmarshal(m.Data, &st); err != nil || st.Aircraft != 3 {
				t.Errorf("stats must stay live: %s", m.Data)
			}
		case "playback":
			if p := playbackOf(t, m); p.Mode != "playback" || !p.Playing || p.Speed != 1 || p.T.Before(start.Add(5*time.Minute)) {
				t.Errorf("status %+v", p)
			}
		}
	}
	if m := until(t, c, "trailPoints"); string(m.Data) != "{}" {
		t.Errorf("deltas after a seek %s", m.Data)
	}

	sendControl(t, c, `{"type":"playback","data":{"action":"pause"}}`)
	for p := playbackOf(t, until(t, c, "playback")); p.Playing; p = playbackOf(t, until(t, c, "playback")) {
	}
	sendControl(t, c, `{"type":"playback","data":{"action":"speed","speed":16}}`)
	for p := playbackOf(t, until(t, c, "playback")); p.Speed != 16; p = playbackOf(t, until(t, c, "playback")) {
	}

	sendControl(t, c, `{"type":"playback","data":{"action":"live"}}`)
	if m := until(t, c, "trails"); !strings.Contains(string(m.Data), `"738001"`) {
		t.Errorf("live trails after live %s", m.Data)
	}
	if m := until(t, c, "aircraft"); !strings.Contains(string(m.Data), `"738001"`) {
		t.Errorf("live aircraft after live %.100s", m.Data)
	}
	if p := playbackOf(t, until(t, c, "playback")); p.Mode != "live" {
		t.Errorf("status after live %+v", p)
	}
}

func TestWS_PlaybackErrors(t *testing.T) {
	srv, start := newPlaybackFixture(t, 1)
	c := dialWS(t, srv)
	until(t, c, "playback")
	for _, tc := range []struct{ msg, want string }{
		{`not json`, "invalid message"},
		{`{"type":"nope"}`, "unknown message type"},
		{`{"type":"playback","data":{"action":"rewind"}}`, "unknown playback action"},
		{`{"type":"playback","data":{"action":"pause"}}`, "not in playback"},
		{`{"type":"playback","data":{"action":"seek"}}`, "seek needs t"},
		{`{"type":"playback","data":{"action":"speed","speed":3}}`, "speed must be one of"},
		{`{"type":"playback","data":{"action":"step","seconds":0}}`, "step needs seconds"},
		{`{"type":"playback","data":{"action":"seek","t":"2001-01-01T00:00:00Z"}}`, "outside the tape"},
	} {
		sendControl(t, c, tc.msg)
		var e wsError
		if err := json.Unmarshal(until(t, c, "error").Data, &e); err != nil || !strings.Contains(e.Message, tc.want) {
			t.Errorf("%s: error %q, want %q", tc.msg, e.Message, tc.want)
		}
	}

	// the connection survives, and a second one is refused while the first holds the only session
	sendControl(t, c, `{"type":"playback","data":{"action":"step","seconds":-120}}`)
	p := playbackOf(t, until(t, c, "playback"))
	for p.Mode == "live" { // updates sent before the command was handled
		p = playbackOf(t, until(t, c, "playback"))
	}
	if p.Mode != "playback" || p.T.After(time.Now().Add(-110*time.Second)) || p.T.Before(start) {
		t.Errorf("step from live %+v", p)
	}
	c2 := dialWS(t, srv)
	until(t, c2, "playback")
	sendControl(t, c2, `{"type":"playback","data":{"action":"seek","t":"`+start.Add(time.Minute).UTC().Format(time.RFC3339)+`"}}`)
	var e wsError
	if err := json.Unmarshal(until(t, c2, "error").Data, &e); err != nil || !strings.Contains(e.Message, "too many playback sessions") {
		t.Errorf("second session: %q", e.Message)
	}
}

func TestWS_PlaybackReturnsToLiveAtTheEnd(t *testing.T) {
	srv, _ := newPlaybackFixture(t, 1)
	c := dialWS(t, srv)
	until(t, c, "playback")
	sendControl(t, c, `{"type":"playback","data":{"action":"step","seconds":-3}}`)
	for playbackOf(t, until(t, c, "playback")).Mode != "playback" {
	}
	deadline := time.Now().Add(5 * time.Second)
	sendControl(t, c, `{"type":"playback","data":{"action":"speed","speed":4}}`) // at 1× it stays 3 s behind
	for playbackOf(t, until(t, c, "playback")).Mode != "live" {
		if time.Now().After(deadline) {
			t.Fatal("playback did not return to live at the end of the tape")
		}
	}
}
