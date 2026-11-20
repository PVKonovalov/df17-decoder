package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/coverage"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
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
	if want := `{"tileUrl":"http://tiles.lan/{z}/{x}/{y}.png","attribution":"lan","maxZoom":17,"darkFilter":false}`; m.Type != "map" || string(m.Data) != want {
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
