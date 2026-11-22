package history

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

// knownStore holds:
//
//	flight 1  4840D6 ADS-B   positions at +0, +10, +20 s (30000, 30500, 31000 ft)
//	flight 2  738ABC Mode-S  samples at +5 s (3000 ft) and +40 s (3100 ft, 7000)
//	flight 3  40621D ADS-B   position at +100 s
//	flight 4  4840D6 ADS-B   position at +200 s (a later flight of the same address)
func knownStore(t *testing.T) *Store {
	t.Helper()
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{})
	t.Cleanup(func() { s.Close() })
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	add := func(id int64, icao string, adsb bool, secs ...int) {
		s.Flight(tracker.FlightInfo{ID: id, ICAO: icao, ADSB: adsb, Callsign: map[bool]string{true: "KLM1023"}[adsb],
			FirstSeen: at(secs[0]), LastSeen: at(secs[len(secs)-1]), Closed: true})
	}
	add(1, "4840D6", true, 0, 20)
	for i, sec := range []int{0, 10, 20} {
		s.Point(1, point(at(sec), 30000+500*i))
	}
	add(2, "738ABC", false, 5, 40)
	s.Sample(2, tracker.Sample{T: at(5).UnixMilli(), Alt: 3000, HasAlt: true})
	s.Sample(2, tracker.Sample{T: at(40).UnixMilli(), Alt: 3100, HasAlt: true, Squawk: "7000"})
	add(3, "40621D", true, 100)
	s.Point(3, point(at(100), 12000))
	add(4, "4840D6", true, 200)
	s.Point(4, point(at(200), 5000))
	if _, err := s.write(drain(s)); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAt(t *testing.T) {
	s := knownStore(t)
	type want struct {
		flight int64
		posSec int // -1: no position
		smpSec int // -1: no sample
	}
	cases := []struct {
		sec  int
		want []want
	}{
		{-1, nil},                                // before anything
		{0, []want{{1, 0, -1}}},                  // a point exactly at t counts
		{15, []want{{1, 10, -1}, {2, -1, 5}}},    // last point at or before t, not the next one
		{70, []want{{1, 20, -1}, {2, -1, 40}}},   // 50 s old: within the 60 s TTL
		{90, []want{{2, -1, 40}}},                // flight 1's last point is 70 s old: gone
		{100, []want{{3, 100, -1}, {2, -1, 40}}}, // 60 s old sample: still within TTL; flight 3 appears
		{150, []want{{3, 100, -1}}},              // flight 2 gone, flight 4 not started
		{230, []want{{4, 200, -1}}},              // the later flight of 4840D6 only
	}
	for _, c := range cases {
		states, err := s.At(t.Context(), t0.Add(time.Duration(c.sec)*time.Second), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		var got []want
		for _, st := range states {
			w := want{st.Flight.ID, -1, -1}
			if st.Position != nil {
				w.posSec = int((st.Position.T - t0.UnixMilli()) / 1000)
			}
			if st.Sample != nil {
				w.smpSec = int((st.Sample.T - t0.UnixMilli()) / 1000)
			}
			got = append(got, w)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("at +%d s: got %v, want %v", c.sec, got, c.want)
		}
	}

	states, _ := s.At(t.Context(), t0.Add(15*time.Second), time.Minute)
	if p := states[0].Position; p.Alt != 30500 || p.AltSrc != tracker.AltBaro || p.Known != tracker.KnownGS || p.GS != 420 {
		t.Errorf("position = %+v", p)
	}
	if f := states[0].Flight; f.Country != "Netherlands" || f.Callsign != "KLM1023" || !f.Closed || f.Points != 3 || *f.MaxAlt != 31000 {
		t.Errorf("flight = %+v", f)
	}
	if smp := states[1].Sample; smp.Alt == nil || *smp.Alt != 3000 || smp.Squawk != "" {
		t.Errorf("sample = %+v", smp)
	}
}

func TestFlights(t *testing.T) {
	s := knownStore(t)
	ids := func(q FlightQuery) []int64 {
		t.Helper()
		if q.Limit == 0 {
			q.Limit = 100
		}
		list, err := s.Flights(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, f := range list {
			out = append(out, f.ID)
		}
		return out
	}
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	for _, c := range []struct {
		name string
		q    FlightQuery
		want []int64
	}{
		{"all, newest first", FlightQuery{}, []int64{4, 3, 2, 1}},
		{"overlapping a range", FlightQuery{From: at(30), To: at(100)}, []int64{3, 2}},
		{"by address", FlightQuery{ICAO: "4840d6"}, []int64{4, 1}},
		{"limit", FlightQuery{Limit: 2}, []int64{4, 3}},
		{"nothing", FlightQuery{From: at(300)}, nil},
	} {
		if got := ids(c.q); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFlightByID(t *testing.T) {
	s := knownStore(t)
	d, err := s.FlightByID(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if d.ICAO != "738ABC" || d.ADSB || d.Country != "Israel" || len(d.Positions) != 0 || len(d.Samples) != 2 || d.Samples[1].Squawk != "7000" {
		t.Errorf("flight 2 = %+v", d)
	}
	if d, _ = s.FlightByID(t.Context(), 1); len(d.Positions) != 3 || d.Positions[2].T != t0.Add(20*time.Second).UnixMilli() {
		t.Errorf("flight 1 positions = %+v", d.Positions)
	}
	if _, err = s.FlightByID(t.Context(), 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestOpen_MigratesV1(t *testing.T) {
	file := filepath.Join(t.TempDir(), "h.db")
	db, err := sql.Open("sqlite", dsn(file))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		migrations[0],
		`INSERT INTO meta(key, value) VALUES('schema_version', '1')`,
		`INSERT INTO flights(id, icao, country, adsb, first_seen, last_seen, closed) VALUES(5, '4840D6', 'Netherlands', 1, 0, 0, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s := open(t, file, Options{})
	defer s.Close()
	if v := queryInt(t, s, `SELECT value FROM meta WHERE key = 'schema_version'`); v != int64(SchemaVersion) {
		t.Errorf("schema version %d, want %d", v, SchemaVersion)
	}
	if n := queryInt(t, s, `SELECT count(*) FROM sqlite_master WHERE name IN ('hourly', 'hourly_country', 'hourly_operator')`); n != 3 {
		t.Errorf("%d rollup tables", n)
	}
	if n := queryInt(t, s, `SELECT count(*) FROM aircraft_seen WHERE icao = '4840D6'`); n != 1 {
		t.Error("aircraft_seen not filled from the flights")
	}
	if n := queryInt(t, s, `SELECT count(*) FROM pragma_table_info('flights') WHERE name = 'country'`); n != 0 {
		t.Error("country column still there")
	}
	if f, err := s.FlightByID(t.Context(), 5); err != nil || f.Country != "Netherlands" || s.LastFlightID() != 5 {
		t.Errorf("flight 5 = %+v (%v), last id %d", f, err, s.LastFlightID())
	}
}
