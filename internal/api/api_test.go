package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/stats"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

type fixture struct {
	handler http.Handler
	src     source.StatsSnapshot
	stats   *stats.Stats
}

// newFixture builds a tracker with:
//
//	738001 ADS-B, position 10 NM north of the receiver, 0 ft (a real zero altitude)
//	738002 ADS-B, position ~40 NM south, 12000 ft, heard 2 s earlier
//	738003 Mode S only (DF11 + DF4), 3000 ft, no position
func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := stats.New(time.Now())
	tr := tracker.New(tracker.Options{
		TTL:             time.Minute,
		KnownAddressTTL: time.Minute,
		Resolver: tracker.PositionResolver{
			Receiver: geo.Point{Lat: 32.09, Lon: 34.96}, MaxRangeNm: 300,
			PairMaxAge: 10 * time.Second, RefMaxAge: time.Minute,
		},
		Stats: st,
	})
	now := time.Now()
	pos := func(icao uint32, at time.Time, lat, lon float64, alt int) {
		for _, odd := range []bool{false, true} {
			tr.Update(modes.Message{DF: 17, ICAO: icao, Time: at, TC: 11, AirbornePosition: &modes.AirbornePosition{
				Altitude: &alt, CPR: modes.EncodeCPR(lat, lon, odd),
			}}, nil)
		}
	}
	pos(0x738002, now.Add(-2*time.Second), 31.42, 34.96, 12000)
	pos(0x738001, now, 32.2567, 34.96, 0)
	tr.Update(modes.Message{DF: 11, ICAO: 0x738003, Time: now, AllCall: &modes.AllCall{Capability: 5}}, nil)
	tr.Update(modes.Message{DF: 4, ICAO: 0x738003, Time: now, AddressParity: true, Surveillance: &modes.Surveillance{Altitude: new(3000)}}, nil)
	st.AddFrame(17)
	st.AddFrame(4)

	f := &fixture{stats: st}
	f.handler = New(Deps{
		Tracker:        tr,
		Stats:          st,
		Source:         func() source.StatsSnapshot { return f.src },
		SourceURL:      "tcp://test:30002",
		StreamInterval: 20 * time.Millisecond,
	})
	return f
}

func (f *fixture) get(t *testing.T, path string) (*httptest.ResponseRecorder, []map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s: Content-Type = %q", path, ct)
	}
	var list []map[string]any
	if rec.Code == http.StatusOK && strings.HasPrefix(strings.TrimSpace(rec.Body.String()), "[") {
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
	}
	return rec, list
}

func icaos(list []map[string]any) string {
	var s []string
	for _, a := range list {
		s = append(s, a["icao"].(string))
	}
	return strings.Join(s, ",")
}

func TestAircraftList_SortAndFilter(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		query string
		want  string
	}{
		{"", "738001,738002,738003"},
		{"?withPosition=true", "738001,738002"},
		{"?sort=distance", "738001,738002,738003"},
		{"?sort=altitude", "738002,738003,738001"},
		{"?sort=lastSeen", "738001,738003,738002"},
		{"?sort=distance&withPosition=false", "738001,738002,738003"},
	}
	for _, tt := range tests {
		rec, list := f.get(t, "/api/aircraft"+tt.query)
		if rec.Code != http.StatusOK || icaos(list) != tt.want {
			t.Errorf("%q: %d %s, want %s", tt.query, rec.Code, icaos(list), tt.want)
		}
	}
}

func TestAircraftList_BadParams(t *testing.T) {
	f := newFixture(t)
	for _, q := range []string{"?sort=speed", "?withPosition=maybe"} {
		if rec, _ := f.get(t, "/api/aircraft"+q); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}
}

func TestAircraftJSON_OmitsUnknownKeepsZero(t *testing.T) {
	f := newFixture(t)
	_, list := f.get(t, "/api/aircraft")

	zero, modeS := list[0], list[2]
	if v, ok := zero["altBaro"]; !ok || v.(float64) != 0 {
		t.Errorf("738001 altBaro = %v (present=%v), want 0 kept", v, ok)
	}
	for _, k := range []string{"lat", "lon", "posTime", "distanceNm", "bearing", "country", "hasPosition", "adsb", "messagesByDf"} {
		if _, ok := zero[k]; !ok {
			t.Errorf("738001: missing %q", k)
		}
	}
	if zero["country"] != "Israel" || zero["adsb"] != true || zero["hasPosition"] != true {
		t.Errorf("738001 = %v", zero)
	}
	for _, k := range []string{"lat", "lon", "callsign", "squawk", "groundSpeed", "rssi", "emergency", "adsbVersion"} {
		if _, ok := modeS[k]; ok {
			t.Errorf("738003: unknown field %q must be omitted", k)
		}
	}
	if modeS["adsb"] != false || modeS["hasPosition"] != false || modeS["altBaro"].(float64) != 3000 {
		t.Errorf("738003 = %v", modeS)
	}
	if d := zero["distanceNm"].(float64); d < 9.9 || d > 10.1 {
		t.Errorf("distance = %v, want ~10", d)
	}
}

func TestAircraftOne(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		path string
		code int
	}{
		{"/api/aircraft/738001", http.StatusOK},
		{"/api/aircraft/738abc", http.StatusNotFound},
		{"/api/aircraft/7380", http.StatusBadRequest},
		{"/api/aircraft/zzzzzz", http.StatusBadRequest},
	}
	for _, tt := range tests {
		rec, _ := f.get(t, tt.path)
		if rec.Code != tt.code {
			t.Errorf("%s: %d, want %d", tt.path, rec.Code, tt.code)
		}
	}
	rec, _ := f.get(t, "/api/aircraft/738001")
	var a map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &a)
	if a["icao"] != "738001" {
		t.Errorf("got %v", a)
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	f := newFixture(t)
	if rec, _ := f.get(t, "/api/nope"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestStats(t *testing.T) {
	f := newFixture(t)
	f.src = source.StatsSnapshot{Connected: true, Reconnects: 2, Malformed: 3, LastFrame: time.Now()}
	rec, _ := f.get(t, "/api/stats")
	var s map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	src := s["source"].(map[string]any)
	if src["connected"] != true || src["reconnects"].(float64) != 2 || src["malformedLines"].(float64) != 3 || src["url"] != "tcp://test:30002" {
		t.Errorf("source = %v", src)
	}
	if s["aircraft"].(float64) != 3 || s["positions"].(float64) != 4 || s["frames"].(float64) != 2 {
		t.Errorf("stats = %v", s)
	}
	if perDF := s["framesPerDf"].(map[string]any); perDF["17"].(float64) != 1 || perDF["4"].(float64) != 1 {
		t.Errorf("framesPerDf = %v", perDF)
	}
	if s["messagesPerSecond"].(float64) <= 0 {
		t.Errorf("messagesPerSecond = %v, want > 0", s["messagesPerSecond"])
	}
	for _, k := range []string{"uptimeSeconds", "crcFailures", "addressParityDrops", "cprRejected", "messages"} {
		if _, ok := s[k]; !ok {
			t.Errorf("missing %q", k)
		}
	}
}

func TestHealthz(t *testing.T) {
	f := newFixture(t)
	if rec, _ := f.get(t, "/healthz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no frames yet: %d, want 503", rec.Code)
	}
	f.src.LastFrame = time.Now().Add(-10 * time.Second)
	if rec, _ := f.get(t, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("recent frame: %d, want 200", rec.Code)
	}
	f.src.LastFrame = time.Now().Add(-31 * time.Second)
	if rec, _ := f.get(t, "/healthz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("stale frame: %d, want 503", rec.Code)
	}
}

func TestStream(t *testing.T) {
	f := newFixture(t)
	srv := httptest.NewServer(f.handler)
	defer srv.Close()

	ctx, cancel := context.WithCancel(t.Context())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	events := 0
	for sc.Scan() && events < 3 {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var list []map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &list); err != nil {
			t.Fatal(err)
		}
		if icaos(list) != "738001,738002,738003" {
			t.Errorf("event %d: %s", events, icaos(list))
		}
		events++
	}
	if events != 3 {
		t.Fatalf("got %d events", events)
	}
	cancel()
}

func TestStream_StopsWhenRequestContextEnds(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/stream", nil)
	done := make(chan struct{})
	go func() {
		f.handler.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream handler did not return after context cancel")
	}
}
