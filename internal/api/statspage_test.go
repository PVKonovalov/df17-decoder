package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/history"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/stats"
	"github.com/PVKonovalov/df17-decoder/internal/storage"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

// sNow is 15:30 UTC: rollups exist for 12, 13 and 14, and 15 is the partial current hour.
var sNow = hT0.Add(3*time.Hour + 30*time.Minute)

// statsHandler serves a history with 4840D6 (KLM1023, 12:00–12:10, positions east of the receiver) and 738ABC
// (Mode-S only, 12:30–15:20), with the rollups backfilled.
func statsHandler(t *testing.T, statsRetention time.Duration) http.Handler {
	t.Helper()
	rx := geo.Point{Lat: 25.07, Lon: 22.87}
	store, err := history.Open(history.Options{File: filepath.Join(t.TempDir(), "h.db"), Receiver: &rx,
		StatsRetention: statsRetention, Now: func() time.Time { return sNow }, Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { store.Run(ctx) })
	store.Flight(tracker.FlightInfo{ID: 1, ICAO: "4840D6", Callsign: "KLM1023", ADSB: true, FirstSeen: hT0, LastSeen: hT0.Add(10 * time.Minute), Closed: true})
	store.Point(1, tracker.TrackPoint{T: hT0.Add(time.Minute).UnixMilli(), Lat: 25.07, Lon: 23.87})
	store.Flight(tracker.FlightInfo{ID: 2, ICAO: "738ABC", FirstSeen: hT0.Add(30 * time.Minute), LastSeen: hT0.Add(200 * time.Minute)})
	cancel()
	wg.Wait()

	rctx, stop := context.WithCancel(context.Background())
	wg.Go(func() { store.RunRollups(rctx, history.RollupSources{}) })
	t.Cleanup(func() { stop(); wg.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, _ := store.Hourly(context.Background(), hT0, sNow, sNow)
		if slices.ContainsFunc(rows, func(r history.HourRow) bool { return r.Hour.Equal(hT0.Add(2*time.Hour)) && !r.Partial }) {
			break // the last finished hour is backfilled
		}
		if time.Now().After(deadline) {
			t.Fatalf("no backfill: %+v", rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return New(Deps{
		Tracker: tracker.New(tracker.Options{TTL: time.Minute}), Stats: stats.New(sNow),
		Source:  func() source.StatsSnapshot { return source.StatsSnapshot{} },
		History: store, Now: func() time.Time { return sNow },
	})
}

func TestStats_Endpoints(t *testing.T) {
	h := statsHandler(t, 0)
	var hourly []history.HourRow
	if code := getJSON(t, h, "/api/stats/hourly?from="+hT0.Format(time.RFC3339)+"&to="+sNow.Format(time.RFC3339), &hourly); code != 200 || len(hourly) != 4 {
		t.Fatalf("%d %+v", code, hourly)
	}
	if h0, cur := hourly[0], hourly[3]; h0.AircraftADSB != 1 || h0.AircraftModeS != 1 || h0.MaxRangeNm == nil || cur.Partial != true || cur.AircraftModeS != 1 || hourly[2].Partial {
		t.Errorf("hours %+v", hourly)
	}
	var def []history.HourRow // default: the last 24 hours
	if getJSON(t, h, "/api/stats/hourly", &def); len(def) != 4 {
		t.Errorf("default range %d rows", len(def))
	}

	var countries []history.CountryCount
	if getJSON(t, h, "/api/stats/countries?from="+hT0.Format(time.RFC3339)+"&limit=1", &countries); len(countries) != 1 || countries[0] != (history.CountryCount{Country: "Israel", AircraftHours: 4}) {
		t.Errorf("countries %+v", countries)
	}
	var ops []history.OperatorCount
	if getJSON(t, h, "/api/stats/operators?from="+hT0.Format(time.RFC3339), &ops); len(ops) != 1 || ops[0].Prefix != "KLM" {
		t.Errorf("operators %+v", ops)
	}
	var bands []history.AltitudeBand
	if getJSON(t, h, "/api/stats/altitude?from="+hT0.Format(time.RFC3339), &bands); len(bands) != 11 || bands[10].Band != "unknown" || bands[10].Positions != 1 {
		t.Errorf("altitude %+v", bands)
	}
	var notable history.Notable
	if getJSON(t, h, "/api/stats/notable?from="+hT0.Format(time.RFC3339)+"&limit=1", &notable); notable.FirstSeen != 2 || len(notable.Events) != 1 {
		t.Errorf("notable %+v", notable)
	}
	var days []history.RangeDay
	if getJSON(t, h, "/api/stats/range-daily?from="+hT0.Format(time.RFC3339)+"&tz=Pacific/Auckland", &days); len(days) != 1 || days[0].Day != "2026-11-21" || days[0].MaxRangeNm < 54 {
		t.Errorf("range days %+v", days)
	}
}

func TestStats_BadRequests(t *testing.T) {
	h := statsHandler(t, 0)
	for _, q := range []string{
		"hourly?from=yesterday", "hourly?from=2000&to=1000", "hourly?from=0&to=" + sNow.Format(time.RFC3339),
		"countries?limit=0", "operators?limit=501", "notable?limit=x", "altitude?to=never",
		"range-daily?tz=Mars/Base", "range-daily?tz=Local", "range-daily?tz=../../etc/passwd", "range-daily?tz=" + strings.Repeat("A", 65),
	} {
		if code := getJSON(t, h, "/api/stats/"+q, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", q, code)
		}
	}
}

func TestStats_RangeClampedToRetention(t *testing.T) {
	h := statsHandler(t, 2*time.Hour)
	var rows []history.HourRow
	if code := getJSON(t, h, "/api/stats/hourly?from=0&to="+sNow.Format(time.RFC3339), &rows); code != 200 || len(rows) == 0 || rows[0].Hour.Before(sNow.Add(-2*time.Hour).Truncate(time.Hour)) {
		t.Errorf("%d %+v", code, rows)
	}
}

func TestStats_DisabledAndStorage(t *testing.T) {
	h := historyHandler(t, false)
	for _, p := range []string{"hourly", "countries", "operators", "range-daily", "altitude", "notable", "daily", "new?day=2026-11-20"} {
		if code := getJSON(t, h, "/api/stats/"+p, nil); code != http.StatusServiceUnavailable {
			t.Errorf("%s: %d, want 503", p, code)
		}
	}
	if code := getJSON(t, h, "/api/storage", nil); code != http.StatusServiceUnavailable {
		t.Errorf("storage without a reporter: %d", code)
	}
	rep := storage.NewReporter(storage.Sources{StateDir: t.TempDir(),
		Volume: func(string) (uint64, uint64, bool) { return 100, 40, true }})
	h = New(Deps{Tracker: tracker.New(tracker.Options{}), Stats: stats.New(sNow),
		Source: func() source.StatsSnapshot { return source.StatsSnapshot{} }, Storage: rep})
	var r storage.Report
	if code := getJSON(t, h, "/api/storage", &r); code != 200 || *r.Volume.FreeBytes != 40 || r.Usage.TapeBytes != nil {
		t.Errorf("%d %+v", code, r)
	}
}

// flatKeys returns the dotted paths of every key in a JSON object, recursing into objects.
func flatKeys(t *testing.T, data []byte, prefix string) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k, v := range m {
		keys = append(keys, prefix+k)
		if strings.HasPrefix(strings.TrimSpace(string(v)), "{") {
			keys = append(keys, flatKeys(t, v, prefix+k+".")...)
		}
	}
	slices.Sort(keys)
	return keys
}

// TestStatsContract keeps the storage and stats JSON in sync with web/src/api/stats.fixture.json, which the
// TypeScript contract test checks against the UI types.
func TestStatsContract(t *testing.T) {
	data, err := os.ReadFile("../../web/src/api/stats.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]json.RawMessage
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{
		"storage": &storage.Report{}, "hourly": &history.HourRow{}, "country": &history.CountryCount{},
		"operator": &history.OperatorCount{}, "rangeDay": &history.RangeDay{}, "altitude": &history.AltitudeBand{},
		"notable": &history.Notable{}, "notableEvent": &history.NotableEvent{},
		"daily": &history.DailyTotals{}, "newAircraft": &history.NewAircraft{},
	} {
		populate(t, reflect.ValueOf(v).Elem())
		got, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		want := fixture[name]
		if name == "notableEvent" {
			var n struct{ Events []json.RawMessage }
			if err := json.Unmarshal(fixture["notable"], &n); err != nil || len(n.Events) == 0 {
				t.Fatal("no notable event in the fixture")
			}
			want = n.Events[0]
		}
		if g, w := flatKeys(t, got, ""), flatKeys(t, want, ""); !slices.Equal(g, w) {
			t.Errorf("%s JSON keys drifted from stats.fixture.json\n Go:      %v\n fixture: %v", name, g, w)
		}
	}
}

func TestStatsDailyAndNew(t *testing.T) {
	h := statsHandler(t, 0)
	var d history.DailyTotals
	if code := getJSON(t, h, "/api/stats/daily", &d); code != http.StatusOK {
		t.Fatal(code)
	}
	want := history.DailyRow{Day: "2026-11-20", Aircraft: 2, ADSB: 1, ModeS: 1, New: 2, NewADSB: 1, NewModeS: 1,
		Learning: true, Partial: true}
	if d.Timezone != "UTC" || len(d.Days) != 1 || d.Days[0] != want {
		t.Errorf("daily %+v", d)
	}

	var list []history.NewAircraft
	if code := getJSON(t, h, "/api/stats/new?day=2026-11-20", &list); code != http.StatusOK {
		t.Fatal(code)
	}
	if len(list) != 2 || list[0].ICAO != "4840D6" || list[0].Callsign != "KLM1023" || !list[0].ADSB ||
		list[1].ICAO != "738ABC" || list[1].ADSB {
		t.Errorf("new %+v", list)
	}
	if getJSON(t, h, "/api/stats/new?day=2026-11-20&limit=1", &list); len(list) != 1 {
		t.Errorf("limit 1: %+v", list)
	}
	if getJSON(t, h, "/api/stats/new?day=2026-11-21", &list); list == nil || len(list) != 0 {
		t.Errorf("a day without new aircraft: %+v", list)
	}
	for _, p := range []string{"/api/stats/new", "/api/stats/new?day=20.11.2026", "/api/stats/new?day=2026-11-20&limit=0",
		"/api/stats/new?day=2026-11-20&limit=1001", "/api/stats/daily?from=x"} {
		if code := getJSON(t, h, p, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", p, code)
		}
	}
}
