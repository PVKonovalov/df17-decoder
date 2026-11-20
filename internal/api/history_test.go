package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/history"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/stats"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

var hT0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// historyHandler serves a store holding flight 1 (4840D6, positions at +0 and +10 s) and flight 2 (738ABC,
// Mode-S-only, one sample at +5 s); store nil disables history.
func historyHandler(t *testing.T, enabled bool) http.Handler {
	t.Helper()
	d := Deps{
		Tracker: tracker.New(tracker.Options{TTL: time.Minute}),
		Stats:   stats.New(time.Now()),
		Source:  func() source.StatsSnapshot { return source.StatsSnapshot{} },
	}
	if enabled {
		store, err := history.Open(history.Options{File: filepath.Join(t.TempDir(), "h.db")})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Go(func() { store.Run(ctx) })
		store.Flight(tracker.FlightInfo{ID: 1, ICAO: "4840D6", Callsign: "KLM1023", ADSB: true, FirstSeen: hT0, LastSeen: hT0.Add(10 * time.Second)})
		for _, sec := range []int{0, 10} {
			store.Point(1, tracker.TrackPoint{T: hT0.Add(time.Duration(sec) * time.Second).UnixMilli(), Lat: 25.1, Lon: 22.9,
				Alt: 30000, AltSrc: tracker.AltBaro})
		}
		store.Flight(tracker.FlightInfo{ID: 2, ICAO: "738ABC", FirstSeen: hT0.Add(5 * time.Second), LastSeen: hT0.Add(5 * time.Second)})
		store.Sample(2, tracker.Sample{T: hT0.Add(5 * time.Second).UnixMilli(), Alt: 2500, HasAlt: true, Squawk: "0243"})
		cancel()
		wg.Wait()
		d.History = store
	}
	return New(d)
}

func getJSON(t *testing.T, h http.Handler, path string, v any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s: Content-Type %q", path, ct)
	}
	if v != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatalf("%s: %v: %s", path, err, rec.Body)
		}
	}
	return rec.Code
}

func TestHistory_Disabled(t *testing.T) {
	h := historyHandler(t, false)
	for _, p := range []string{"/api/history/flights", "/api/history/flights/1", "/api/history/at?t=0", "/api/history/timeline?from=0&to=1"} {
		if code := getJSON(t, h, p, nil); code != http.StatusServiceUnavailable {
			t.Errorf("%s: %d, want 503", p, code)
		}
	}
}

func TestHistory_Flights(t *testing.T) {
	h := historyHandler(t, true)
	var list []map[string]any
	if code := getJSON(t, h, "/api/history/flights", &list); code != http.StatusOK || len(list) != 2 || list[0]["icao"] != "738ABC" {
		t.Fatalf("%d %v", code, list)
	}
	if list[1]["country"] != "Netherlands" || list[1]["callsign"] != "KLM1023" || list[1]["points"].(float64) != 2 {
		t.Errorf("flight 1 = %v", list[1])
	}
	from := strconv.FormatInt(hT0.Add(6*time.Second).UnixMilli(), 10)
	if getJSON(t, h, "/api/history/flights?icao=4840D6&from="+from+"&to="+hT0.Add(time.Hour).Format(time.RFC3339), &list); len(list) != 1 || list[0]["id"].(float64) != 1 {
		t.Errorf("filtered = %v", list)
	}
	if getJSON(t, h, "/api/history/flights?limit=1", &list); len(list) != 1 {
		t.Errorf("limit 1 = %v", list)
	}
	if getJSON(t, h, "/api/history/flights?q=klm", &list); len(list) != 1 || list[0]["id"].(float64) != 1 {
		t.Errorf("q=klm = %v", list)
	}
	for _, q := range []string{"from=yesterday", "icao=XYZ", "limit=0", "limit=5000", "from=2000&to=1000", "q=" + strings.Repeat("A", 17)} {
		if code := getJSON(t, h, "/api/history/flights?"+q, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", q, code)
		}
	}
}

func TestHistory_Flight(t *testing.T) {
	h := historyHandler(t, true)
	var f struct {
		ID        int64
		Positions []map[string]any
		Samples   []map[string]any
	}
	if code := getJSON(t, h, "/api/history/flights/1", &f); code != http.StatusOK || f.ID != 1 || len(f.Positions) != 2 || len(f.Samples) != 0 {
		t.Errorf("%d %+v", code, f)
	}
	if f.Positions[1]["alt"].(float64) != 30000 {
		t.Errorf("position = %v", f.Positions[1])
	}
	if code := getJSON(t, h, "/api/history/flights/2", &f); code != http.StatusOK || len(f.Positions) != 0 || f.Samples[0]["squawk"] != "0243" {
		t.Errorf("%d %+v", code, f)
	}
	if code := getJSON(t, h, "/api/history/flights/9", nil); code != http.StatusNotFound {
		t.Errorf("unknown: %d", code)
	}
	if code := getJSON(t, h, "/api/history/flights/x", nil); code != http.StatusBadRequest {
		t.Errorf("bad id: %d", code)
	}
}

func TestHistory_At(t *testing.T) {
	h := historyHandler(t, true)
	var states []struct {
		Flight   map[string]any
		Position map[string]any
		Sample   map[string]any
	}
	if code := getJSON(t, h, "/api/history/at?t="+hT0.Add(7*time.Second).Format(time.RFC3339), &states); code != http.StatusOK || len(states) != 2 {
		t.Fatalf("%d %+v", code, states)
	}
	if states[0].Flight["icao"] != "4840D6" || states[0].Position["t"].(float64) != float64(hT0.UnixMilli()) || states[0].Sample != nil {
		t.Errorf("4840D6 = %+v", states[0])
	}
	if states[1].Flight["icao"] != "738ABC" || states[1].Position != nil || states[1].Sample["alt"].(float64) != 2500 {
		t.Errorf("738ABC = %+v", states[1])
	}
	// the tracker TTL (1 min) bounds the age of the last point
	if getJSON(t, h, "/api/history/at?t="+strconv.FormatInt(hT0.Add(80*time.Second).UnixMilli(), 10), &states); len(states) != 0 {
		t.Errorf("80 s later: %+v", states)
	}
	for _, q := range []string{"", "?t=", "?t=soon"} {
		if code := getJSON(t, h, "/api/history/at"+q, nil); code != http.StatusBadRequest {
			t.Errorf("%q: %d, want 400", q, code)
		}
	}
}

func TestHistory_Timeline(t *testing.T) {
	h := historyHandler(t, true)
	var tl history.Timeline
	q := "/api/history/timeline?from=" + hT0.Format(time.RFC3339) + "&to=" + hT0.Add(3*time.Minute).Format(time.RFC3339)
	if code := getJSON(t, h, q, &tl); code != http.StatusOK || tl.Bucket != "1m0s" || len(tl.Buckets) != 3 || tl.Buckets[0] != [2]int{1, 1} || tl.Buckets[1] != [2]int{} {
		t.Fatalf("%d %+v", code, tl)
	}
	if getJSON(t, h, q+"&bucket=30s", &tl); len(tl.Buckets) != 6 {
		t.Errorf("30 s buckets: %+v", tl)
	}
	for _, bad := range []string{"", "from=0", "from=1000&to=1000", "from=0&to=1000&bucket=1ms", "from=0&to=1000&bucket=x",
		"from=0&to=" + strconv.FormatInt(int64(history.MaxTimelineBuckets+1)*60_000, 10)} {
		if code := getJSON(t, h, "/api/history/timeline?"+bad, nil); code != http.StatusBadRequest {
			t.Errorf("%q: %d, want 400", bad, code)
		}
	}
}
