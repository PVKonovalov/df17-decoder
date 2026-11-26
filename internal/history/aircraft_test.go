package history

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

var testReceiver = geo.Point{Lat: 25.07, Lon: 22.87}

func fl(id int64, icao, callsign string, adsb bool, first time.Time, dur time.Duration, closed bool) tracker.FlightInfo {
	return tracker.FlightInfo{ID: id, ICAO: icao, Callsign: callsign, ADSB: adsb, FirstSeen: first,
		LastSeen: first.Add(dur), Closed: closed}
}

func writeAll(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.write(drain(s)); err != nil {
		t.Fatal(err)
	}
}

type seenRow struct {
	flights, adsb int64
	first, last   int64
	callsign      string
}

func seen(t *testing.T, s *Store, icao string) (r seenRow) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT flights, adsb, first_seen, last_seen, coalesce(last_callsign, '') FROM aircraft_seen
		WHERE icao = ?`, icao).Scan(&r.flights, &r.adsb, &r.first, &r.last, &r.callsign); err != nil {
		t.Fatalf("aircraft_seen %s: %v", icao, err)
	}
	return r
}

func TestAircraftSeen_CountsOnClose(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{})
	defer s.Close()
	s.Flight(fl(1, "AB12CD", "FAKE101", false, t0, time.Minute, false))
	writeAll(t, s)
	if n := queryInt(t, s, `SELECT count(*) FROM aircraft_seen`); n != 0 {
		t.Fatalf("open flight counted: %d rows", n)
	}
	s.Flight(fl(1, "AB12CD", "FAKE101", true, t0, 2*time.Minute, true))
	s.Flight(fl(1, "AB12CD", "FAKE101", true, t0, 2*time.Minute, true))
	writeAll(t, s)
	s.Flight(fl(1, "AB12CD", "FAKE101", true, t0, 2*time.Minute, true))
	s.Flight(fl(2, "AB12CD", "", false, t0.Add(time.Hour), time.Minute, true))
	writeAll(t, s)
	r := seen(t, s, "AB12CD")
	if r.flights != 2 || r.adsb != 1 || r.callsign != "FAKE101" || r.first != t0.UnixMilli() ||
		r.last != t0.Add(time.Hour+time.Minute).UnixMilli() {
		t.Errorf("aircraft_seen %+v", r)
	}
}

func TestAircraftSeen_CrashCloseCounted(t *testing.T) {
	file := filepath.Join(t.TempDir(), "h.db")
	s := open(t, file, Options{})
	s.Flight(fl(1, "AB12CD", "FAKE101", true, t0, time.Minute, false))
	writeAll(t, s)
	s.Close()
	s = open(t, file, Options{})
	defer s.Close()
	if r := seen(t, s, "AB12CD"); r.flights != 1 {
		t.Errorf("crash-closed flight counted %d times", r.flights)
	}
}

// v7Store writes closed flights and rewinds aircraft_seen and max_distance_nm to what migration 8 leaves.
func v7Store(t *testing.T, file string) {
	t.Helper()
	s := open(t, file, Options{})
	defer s.Close()
	for i := range int64(3) {
		s.Flight(fl(i+1, "AB12CD", "FAKE10"+string(rune('1'+i)), i == 1, t0.Add(time.Duration(i)*time.Hour), time.Minute, true))
		s.Point(i+1, tracker.TrackPoint{T: t0.Add(time.Duration(i) * time.Hour).UnixMilli(), Lat: 25.07, Lon: 23.07 + float64(i)})
	}
	s.Flight(fl(4, "C0FFEE", "", false, t0, time.Minute, true))
	writeAll(t, s)
	for _, q := range []string{
		`UPDATE aircraft_seen SET flights = 0, adsb = 0, last_callsign = NULL`,
		`UPDATE flights SET max_distance_nm = NULL`,
		`INSERT OR REPLACE INTO meta(key, value) VALUES('backfill_aircraft', '-1')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAircraftSeen_Backfill(t *testing.T) {
	file := filepath.Join(t.TempDir(), "h.db")
	v7Store(t, file)
	s := open(t, file, Options{Receiver: &testReceiver})
	defer s.Close()
	if r := seen(t, s, "AB12CD"); r.flights != 0 {
		t.Fatalf("Open ran the backfill: %+v", r)
	}
	stop := run(s)
	s.Flight(fl(5, "AB12CD", "FAKE200", true, t0.Add(5*time.Hour), time.Minute, true))
	deadline := time.Now().Add(5 * time.Second)
	for queryInt(t, s, `SELECT count(*) FROM meta WHERE key = 'backfill_aircraft'`) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("backfill did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	if r := seen(t, s, "AB12CD"); r.flights != 4 || r.adsb != 1 || r.callsign != "FAKE200" {
		t.Errorf("AB12CD %+v, want 4 flights (3 backfilled + 1 closed), ADS-B, FAKE200", r)
	}
	if r := seen(t, s, "C0FFEE"); r.flights != 1 || r.adsb != 0 {
		t.Errorf("C0FFEE %+v", r)
	}
	if n := queryInt(t, s, `SELECT count(*) FROM flights WHERE points > 0 AND max_distance_nm IS NULL`); n != 0 {
		t.Errorf("%d flights without a max distance", n)
	}
	var d float64
	if err := s.db.QueryRow(`SELECT max_distance_nm FROM flights WHERE id = 3`).Scan(&d); err != nil || d < 110 || d > 130 {
		t.Errorf("flight 3 max distance %v (%v), want 2.2° of longitude at 25°N", d, err)
	}
}

func TestAircraftSeen_SurvivesRetention(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{Retention: 24 * time.Hour, StatsRetention: time.Hour,
		Now: func() time.Time { return now }})
	defer s.Close()
	s.Flight(fl(1, "AB12CD", "FAKE101", true, t0, time.Minute, true))
	s.Flight(fl(2, "AB12CD", "FAKE102", false, now.Add(-time.Hour), time.Minute, true))
	writeAll(t, s)
	s.prune()
	if err := s.pruneRollups(now); err != nil {
		t.Fatal(err)
	}
	a, err := s.Aircraft(context.Background(), "ab12cd")
	if err != nil {
		t.Fatal(err)
	}
	if a.Flights != 2 || a.RetainedFlights != 1 || !a.FirstSeen.Equal(t0) || !a.ADSB ||
		a.RetainedFrom == nil || !a.RetainedFrom.Equal(now.Add(-time.Hour)) {
		t.Errorf("summary after retention %+v", a)
	}
}

// searchStore holds AB12CD (FAKE101, then FAKE777), AB34EF (XFAKE10), C0FFEE (Mode-S, no callsign, open) and,
// only in aircraft_seen, 123456 (OLD555).
func searchStore(t *testing.T) *Store {
	t.Helper()
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{})
	t.Cleanup(func() { s.Close() })
	s.Flight(fl(1, "AB12CD", "FAKE101", true, t0, time.Minute, true))
	s.Flight(fl(2, "AB12CD", "FAKE777", true, t0.Add(3*time.Hour), time.Minute, true))
	s.Flight(fl(3, "AB34EF", "XFAKE10", true, t0.Add(time.Hour), time.Minute, true))
	s.Flight(fl(4, "C0FFEE", "", false, t0.Add(2*time.Hour), time.Minute, false))
	writeAll(t, s)
	if _, err := s.db.Exec(`INSERT INTO aircraft_seen VALUES ('123456', ?, ?, 7, 0, 'OLD555')`,
		t0.Add(-time.Hour).UnixMilli(), t0.Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	return s
}

func icaos(ms []AircraftMatch) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.ICAO)
	}
	return out
}

func TestSearch(t *testing.T) {
	s := searchStore(t)
	ctx := context.Background()
	live := func(icao string) bool { return icao == "C0FFEE" }
	for _, c := range []struct {
		q     string
		limit int
		want  []string
	}{
		{"ab", 20, []string{"AB12CD", "AB34EF"}},   // hex prefix, newest first
		{"AB12CD", 20, []string{"AB12CD"}},         // full address
		{"c", 20, []string{"C0FFEE"}},              // hex prefix, live
		{"fake", 20, []string{"AB12CD", "AB34EF"}}, // prefix (AB12CD) and substring (XFAKE10), one row each
		{"fa", 20, []string{"AB12CD"}},             // under 3 characters: prefix only
		{"777", 20, []string{"AB12CD"}},            // substring of a non-hex text
		{"old", 20, []string{"123456"}},            // last callsign of a pruned aircraft
		{"1", 20, []string{"123456"}},              // hex prefix in aircraft_seen only
		{"fake", 1, []string{"AB12CD"}},            // limit
		{"zzz", 20, nil},                           // nothing
		{"%", 20, nil},                             // LIKE wildcards are literal
	} {
		got, err := s.Search(ctx, c.q, c.limit, live)
		if err != nil {
			t.Fatal(err)
		}
		if g := icaos(got); !slices.Equal(g, c.want) {
			t.Errorf("Search(%q, %d) = %v, want %v", c.q, c.limit, g, c.want)
		}
	}
	got, err := s.Search(ctx, "", 20, live)
	if err != nil || len(got) != 0 {
		t.Errorf("empty query: %v, %v", got, err)
	}

	all, _ := s.Search(ctx, "fake", 20, func(icao string) bool { return icao == "AB34EF" })
	if g := icaos(all); !slices.Equal(g, []string{"AB34EF", "AB12CD"}) || !all[0].Live || all[1].Live {
		t.Errorf("live first: %v", all)
	}
	m := all[1]
	if m.LastCallsign != "FAKE777" || !slices.Equal(m.Callsigns, []string{"FAKE777", "FAKE101"}) || m.Flights != 2 ||
		!m.ADSB || !m.LastSeen.Equal(t0.Add(3*time.Hour+time.Minute)) || m.Country == "" {
		t.Errorf("AB12CD match %+v", m)
	}
	open, _ := s.Search(ctx, "c0", 20, nil)
	if len(open) != 1 || open[0].Flights != 1 || open[0].ADSB || len(open[0].Callsigns) != 0 {
		t.Errorf("open Mode-S flight %+v", open)
	}
	old, _ := s.Search(ctx, "123", 20, nil)
	if len(old) != 1 || old[0].Flights != 7 || old[0].LastCallsign != "OLD555" {
		t.Errorf("aircraft_seen only %+v", old)
	}
}

func TestMatchesSearch(t *testing.T) {
	for _, c := range []struct {
		q, icao, cs string
		want        bool
	}{
		{"AB", "AB12CD", "", true}, {"FAKE", "AB12CD", "FAKE101", true}, {"AKE", "AB12CD", "FAKE101", true},
		{"AK", "AB12CD", "FAKE101", false}, {"12", "AB12CD", "", false}, {"", "AB12CD", "FAKE101", false},
	} {
		if got := MatchesSearch(c.q, c.icao, c.cs); got != c.want {
			t.Errorf("MatchesSearch(%q, %q, %q) = %v", c.q, c.icao, c.cs, got)
		}
	}
}

func TestAircraftSummary(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{})
	defer s.Close()
	day := 24 * time.Hour
	add := func(id int64, cs string, first time.Time, dur time.Duration, alt int, minD, maxD float64) {
		f := fl(id, "AB12CD", cs, true, first, dur, true)
		f.MaxAlt, f.MinDistanceNm, f.MaxDistanceNm = new(alt), new(minD), new(maxD)
		s.Flight(f)
	}
	add(1, "FAKE101", t0, time.Hour, 30000, 12, 80)
	add(2, "FAKE101", t0.Add(day), time.Hour, 36000, 5, 60)
	add(3, "FAKE777", t0.Add(2*day+11*time.Hour+30*time.Minute), time.Hour, 20000, 40, 150) // 23:30 → 00:30 UTC
	s.Flight(fl(4, "C0FFEE", "", false, t0.Add(-day), time.Minute, true))
	s.Quality(1, tracker.QualityChange{T: t0.Add(time.Minute).UnixMilli(), Quality: tracker.QualityDegraded})
	s.Quality(2, tracker.QualityChange{T: t0.Add(day).UnixMilli(), Quality: tracker.QualityDegraded})
	s.Quality(2, tracker.QualityChange{T: t0.Add(day + time.Minute).UnixMilli(), Quality: tracker.QualityLost})
	writeAll(t, s)

	a, err := s.Aircraft(context.Background(), "AB12CD")
	if err != nil {
		t.Fatal(err)
	}
	want := []CallsignCount{{"FAKE101", 2}, {"FAKE777", 1}}
	if a.Flights != 3 || a.RetainedFlights != 3 || !slices.Equal(a.Callsigns, want) || *a.MaxAlt != 36000 ||
		*a.MinDistanceNm != 5 || *a.MaxDistanceNm != 150 || a.DaysSeen != 4 || !a.FirstSeen.Equal(t0) ||
		!a.LastSeen.Equal(t0.Add(2*day+12*time.Hour+30*time.Minute)) || a.Country == "" {
		t.Errorf("summary %+v", a)
	}
	if a.Quality == nil || *a.Quality != (QualitySummary{OK: 1, Degraded: 1, Lost: 1}) {
		t.Errorf("quality %+v", a.Quality)
	}
	if a.RetainedFrom == nil || !a.RetainedFrom.Equal(t0.Add(-day)) {
		t.Errorf("retainedFrom %v, want the oldest flight of any aircraft", a.RetainedFrom)
	}
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	s.opt.Location = tokyo
	if a, _ := s.Aircraft(context.Background(), "AB12CD"); a.DaysSeen != 3 {
		t.Errorf("days seen in Tokyo %d, want 3", a.DaysSeen)
	}
	s.opt.Location = time.UTC
	if m, _ := s.Aircraft(context.Background(), "C0FFEE"); m.Quality != nil || m.ADSB {
		t.Errorf("Mode-S summary %+v", m)
	}
	if _, err := s.Aircraft(context.Background(), "ABCDEF"); err != ErrNoAircraft {
		t.Errorf("unknown address: %v", err)
	}
}

func TestFlights_Paging(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{})
	defer s.Close()
	for i := range int64(25) {
		s.Flight(fl(i+1, "AB12CD", "FAKE101", true, t0.Add(time.Duration(i/3)*time.Hour), time.Minute, true)) // ties of 3
	}
	s.Flight(fl(100, "C0FFEE", "", false, t0, time.Minute, true))
	writeAll(t, s)
	var got []int64
	p := FlightQuery{ICAO: "ab12cd", Limit: 4}
	for range 10 {
		page, err := s.Flights(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range page {
			got = append(got, f.ID)
		}
		if len(page) < p.Limit {
			break
		}
		last := page[len(page)-1]
		p.Before, p.BeforeID = last.FirstSeen.UnixMilli(), last.ID
	}
	want := []int64{25}
	for id := int64(24); id >= 1; id-- {
		want = append(want, id)
	}
	if !slices.Equal(got, want) {
		t.Errorf("pages %v, want %v", got, want)
	}
	page, _ := s.Flights(context.Background(), FlightQuery{ICAO: "AB12CD", Before: t0.Add(time.Hour).UnixMilli(), Limit: 20})
	if ids := []int64{page[0].ID, page[len(page)-1].ID}; len(page) != 3 || ids[0] != 3 || ids[1] != 1 {
		t.Errorf("before only: %d rows", len(page))
	}
	if f := page[0]; f.DurationS != 60 {
		t.Errorf("duration %d", f.DurationS)
	}
}

func TestTracks(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{})
	defer s.Close()
	s.Flight(fl(1, "AB12CD", "FAKE101", true, t0, time.Minute, true))
	s.Point(1, point(t0, 30000))
	s.Point(1, tracker.TrackPoint{T: t0.Add(time.Second).UnixMilli(), Lat: 25.2, Lon: 23})
	s.Flight(fl(2, "C0FFEE", "", true, t0, time.Minute, true))
	writeAll(t, s)
	got, err := s.Tracks(context.Background(), []int64{2, 99, 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 2 || len(got[0].T) != 0 || got[1].ID != 1 || got[1].ICAO != "AB12CD" {
		t.Fatalf("tracks %+v", got)
	}
	tr := got[1]
	if len(tr.T) != 2 || tr.Lat[1] != 25.2 || *tr.Alt[0] != 30000 || tr.Alt[1] != nil || *tr.GS[0] != 420 ||
		tr.GS[1] != nil || tr.Track[0] != nil {
		t.Errorf("track %+v", tr)
	}
}

func TestWrite_DistancesFromPoints(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{Receiver: &testReceiver})
	defer s.Close()
	s.Flight(fl(1, "AB12CD", "FAKE101", true, t0, time.Minute, false))
	for i, lon := range []float64{22.97, 23.27, 23.07} {
		s.Point(1, tracker.TrackPoint{T: t0.Add(time.Duration(i) * time.Second).UnixMilli(), Lat: 25.07, Lon: lon})
	}
	writeAll(t, s)
	f, err := s.FlightByID(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if f.MinDistanceNm == nil || f.MaxDistanceNm == nil || *f.MinDistanceNm > 6 || *f.MaxDistanceNm < 21 || *f.MaxDistanceNm > 22.5 {
		t.Errorf("open flight distances %v–%v, want about 5.4–21.7 nm", f.MinDistanceNm, f.MaxDistanceNm)
	}
}
