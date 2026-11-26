package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/history"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/stats"
	"github.com/PVKonovalov/df17-decoder/internal/tape"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

// fakeAircraftHandler serves a store holding AB12CD (FAKE101 at hT0, FAKE777 at +3 h, with 2 positions), C0FFEE
// (Mode-S at +1 h) and a tape covering hT0 + 2 h … + 4 h. The tracker has C0FFEE and BEEF01 (FAKE900, not stored)
// live.
func fakeAircraftHandler(t *testing.T) http.Handler {
	t.Helper()
	store, err := history.Open(history.Options{File: filepath.Join(t.TempDir(), "h.db"), Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { store.Run(ctx) })
	add := func(id int64, icao, cs string, adsb bool, at time.Time) {
		store.Flight(tracker.FlightInfo{ID: id, ICAO: icao, Callsign: cs, ADSB: adsb, FirstSeen: at,
			LastSeen: at.Add(10 * time.Minute), Closed: true})
	}
	add(1, "AB12CD", "FAKE101", true, hT0)
	add(2, "C0FFEE", "", false, hT0.Add(time.Hour))
	add(3, "AB12CD", "FAKE777", true, hT0.Add(3*time.Hour))
	for i := range 2 {
		store.Point(3, tracker.TrackPoint{T: hT0.Add(3*time.Hour + time.Duration(i)*time.Minute).UnixMilli(),
			Lat: 25.1, Lon: 22.9 + 0.1*float64(i), Alt: 30000, AltSrc: tracker.AltBaro})
	}
	cancel()
	wg.Wait()

	dir := filepath.Join(t.TempDir(), "tape")
	rec, err := tape.New(tape.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []int{120, 150, 180, 210, 240} {
		rec.Record(source.Frame{Data: make([]byte, 7), Time: hT0.Add(time.Duration(m) * time.Minute)})
	}
	rctx, rcancel := context.WithCancel(context.Background())
	rcancel()
	rec.Run(rctx)

	tr := tracker.New(tracker.Options{TTL: time.Hour})
	now := time.Now()
	tr.Update(modes.Message{DF: 11, ICAO: 0xC0FFEE, Time: now, AllCall: &modes.AllCall{Capability: 5}}, nil)
	tr.Update(modes.Message{DF: 17, ICAO: 0xBEEF01, Time: now, TC: 4, Identification: &modes.Identification{Callsign: "FAKE900"}}, nil)
	return New(Deps{
		Tracker:  tr,
		Stats:    stats.New(time.Now()),
		Source:   func() source.StatsSnapshot { return source.StatsSnapshot{} },
		History:  store,
		TapeData: tape.Open(dir),
	})
}

func TestAircraftHistory_Disabled(t *testing.T) {
	h := historyHandler(t, false)
	for _, p := range []string{"/api/history/search?q=AB", "/api/history/aircraft/AB12CD", "/api/history/tracks?ids=1"} {
		if code := getJSON(t, h, p, nil); code != http.StatusServiceUnavailable {
			t.Errorf("%s: %d, want 503", p, code)
		}
	}
}

func TestHistorySearch(t *testing.T) {
	h := fakeAircraftHandler(t)
	var got []history.AircraftMatch
	if code := getJSON(t, h, "/api/history/search?q=fake", &got); code != http.StatusOK {
		t.Fatal(code)
	}
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ICAO)
	}
	if !slices.Equal(ids, []string{"BEEF01", "AB12CD"}) || !got[0].Live || got[0].LastCallsign != "FAKE900" || got[1].Live ||
		got[1].Flights != 2 || got[1].LastCallsign != "FAKE777" {
		t.Errorf("search fake: %+v", got)
	}
	if getJSON(t, h, "/api/history/search?q=c0", &got); len(got) != 1 || !got[0].Live || got[0].Flights != 1 {
		t.Errorf("search c0: %+v", got)
	}
	if getJSON(t, h, "/api/history/search?q=fake&limit=1", &got); len(got) != 1 || got[0].ICAO != "BEEF01" {
		t.Errorf("limit: %+v", got)
	}
	for _, p := range []string{"/api/history/search", "/api/history/search?q=%20", "/api/history/search?q=a&limit=0",
		"/api/history/search?q=a&limit=51", "/api/history/search?q=" + strings.Repeat("A", 17)} {
		if code := getJSON(t, h, p, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", p, code)
		}
	}
}

func TestHistoryAircraft(t *testing.T) {
	h := fakeAircraftHandler(t)
	var a history.AircraftSummary
	if code := getJSON(t, h, "/api/history/aircraft/ab12cd", &a); code != http.StatusOK {
		t.Fatal(code)
	}
	if a.Flights != 2 || a.Live || len(a.Callsigns) != 2 || a.DaysSeen != 1 || !a.FirstSeen.Equal(hT0) ||
		a.RetainedFrom == nil || !a.RetainedFrom.Equal(hT0) {
		t.Errorf("summary %+v", a)
	}
	if getJSON(t, h, "/api/history/aircraft/C0FFEE", &a); !a.Live {
		t.Errorf("C0FFEE not live: %+v", a)
	}
	for p, want := range map[string]int{
		"/api/history/aircraft/ABCDEF": http.StatusNotFound,
		"/api/history/aircraft/XYZ":    http.StatusBadRequest,
	} {
		if code := getJSON(t, h, p, nil); code != want {
			t.Errorf("%s: %d, want %d", p, code, want)
		}
	}
}

func TestHistoryFlights_PagingAndOnTape(t *testing.T) {
	h := fakeAircraftHandler(t)
	var page []history.Flight
	if code := getJSON(t, h, "/api/history/flights?icao=AB12CD&limit=1", &page); code != http.StatusOK || len(page) != 1 {
		t.Fatal(code, page)
	}
	f := page[0]
	if f.ID != 3 || !f.OnTape || f.DurationS != 600 || f.Points != 2 || f.MaxAlt == nil || *f.MaxAlt != 30000 {
		t.Errorf("first page %+v", f)
	}
	next := "/api/history/flights?icao=AB12CD&limit=1&before=" + strconv.FormatInt(f.FirstSeen.UnixMilli(), 10) +
		"&beforeId=" + strconv.FormatInt(f.ID, 10)
	if getJSON(t, h, next, &page); len(page) != 1 || page[0].ID != 1 || page[0].OnTape {
		t.Errorf("second page %+v", page)
	}
	for _, p := range []string{"/api/history/flights?before=x", "/api/history/flights?beforeId=3"} {
		if code := getJSON(t, h, p, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", p, code)
		}
	}
}

func TestHistoryTracks(t *testing.T) {
	h := fakeAircraftHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/history/tracks?ids=3,1,3,99", nil))
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body)
	}
	var raw struct {
		Tracks []map[string]json.RawMessage `json:"tracks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || len(raw.Tracks) != 2 {
		t.Fatalf("%v %s", err, rec.Body)
	}
	if got := string(raw.Tracks[0]["alt"]); got != "[30000,30000]" || string(raw.Tracks[0]["gs"]) != "[null,null]" ||
		string(raw.Tracks[1]["t"]) != "[]" {
		t.Errorf("compact columns: %s", rec.Body)
	}
	ids := make([]string, 21)
	for i := range ids {
		ids[i] = strconv.Itoa(i + 1)
	}
	for _, p := range []string{"/api/history/tracks", "/api/history/tracks?ids=1,x", "/api/history/tracks?ids=" + strings.Join(ids, ",")} {
		if code := getJSON(t, h, p, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", p, code)
		}
	}
	if code := getJSON(t, h, "/api/history/tracks?ids="+strings.Join(ids[:20], ","), nil); code != http.StatusOK {
		t.Errorf("20 ids: %d", code)
	}
}

// TestAircraftHistoryContract checks the new history JSON shapes against web/src/api/history.fixture.json.
func TestAircraftHistoryContract(t *testing.T) {
	data, err := os.ReadFile("../../web/src/api/history.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]json.RawMessage
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{
		"match": &history.AircraftMatch{}, "aircraft": &history.AircraftSummary{}, "flight": &history.Flight{},
		"tracks": &TracksResponse{}, "track": &history.Track{}, "sample": &history.SamplePoint{},
	} {
		populate(t, reflect.ValueOf(v).Elem())
		got, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if g, w := flatKeys(t, got, ""), flatKeys(t, fixture[name], ""); !slices.Equal(g, w) {
			t.Errorf("%s JSON keys drifted from history.fixture.json\n Go:      %v\n fixture: %v", name, g, w)
		}
	}
}

// BenchmarkHistoryFlight serves /api/history/flights/{id} for a 2-hour flight with a point every 2 s (3600 points,
// more than the thinned trail keeps).
func BenchmarkHistoryFlight(b *testing.B) {
	store, err := history.Open(history.Options{File: filepath.Join(b.TempDir(), "h.db")})
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { store.Run(ctx) })
	store.Flight(tracker.FlightInfo{ID: 1, ICAO: "AB12CD", Callsign: "FAKE101", ADSB: true, FirstSeen: hT0,
		LastSeen: hT0.Add(2 * time.Hour), Closed: true})
	for i := range 3600 {
		store.Point(1, tracker.TrackPoint{T: hT0.Add(time.Duration(i) * 2 * time.Second).UnixMilli(), Lat: 25 + float64(i)/3600,
			Lon: 22.9, Alt: 30000, AltSrc: tracker.AltBaro, GS: 420, Track: 10, Known: tracker.KnownGS | tracker.KnownTrack})
	}
	cancel()
	wg.Wait()
	h := New(Deps{Tracker: tracker.New(tracker.Options{TTL: time.Minute}), Stats: stats.New(time.Now()),
		Source: func() source.StatsSnapshot { return source.StatsSnapshot{} }, History: store})
	b.ResetTimer()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/history/flights/1", nil))
		if rec.Code != http.StatusOK {
			b.Fatal(rec.Code)
		}
		b.SetBytes(int64(rec.Body.Len()))
	}
}
