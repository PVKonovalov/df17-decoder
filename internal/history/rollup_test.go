package history

import (
	"context"
	"database/sql"
	"maps"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

var receiver = geo.Point{Lat: 25.07, Lon: 22.87}

// rollupStore holds, around t0 = 12:00 UTC:
//
//	4840D6 ADS-B   KLM1023 12:10–12:20 and KLM1024 12:30–12:40 (one aircraft, two flights), 2 positions
//	738ABC Mode-S  11:30–14:30 (three hours and a half)
//	40621D ADS-B   N123AB 12:50–13:10 (not an airline callsign)
//	3C6444 Mode-S  12:00–12:05, then ADS-B DLH4AB 12:30–12:35 (counts as ADS-B)
func rollupStore(t *testing.T, opt Options) *Store {
	t.Helper()
	opt.Receiver = &receiver
	s := open(t, filepath.Join(t.TempDir(), "h.db"), opt)
	t.Cleanup(func() { s.Close() })
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	add := func(id int64, icao, callsign string, adsb bool, from, to int) {
		s.Flight(tracker.FlightInfo{ID: id, ICAO: icao, Callsign: callsign, ADSB: adsb, FirstSeen: at(from), LastSeen: at(to), Closed: true})
	}
	add(1, "4840D6", "KLM1023", true, 10, 20)
	add(2, "4840D6", "KLM1024", true, 30, 40)
	add(3, "738ABC", "", false, -30, 150)
	add(4, "40621D", "N123AB", true, 50, 70)
	add(5, "3C6444", "", false, 0, 5)
	add(6, "3C6444", "DLH4AB", true, 30, 35)
	s.Point(1, tracker.TrackPoint{T: at(12).UnixMilli(), Lat: receiver.Lat + 0.5, Lon: receiver.Lon}) // 30 NM north
	s.Point(1, tracker.TrackPoint{T: at(15).UnixMilli(), Lat: receiver.Lat, Lon: receiver.Lon + 1.0}) // ~54 NM east
	s.Point(4, tracker.TrackPoint{T: at(65).UnixMilli(), Lat: receiver.Lat - 0.1, Lon: receiver.Lon}) // next hour
	if _, err := s.write(drain(s)); err != nil {
		t.Fatal(err)
	}
	return s
}

type hourRow struct {
	adsb, modes, started, positions int
	messages, dbBytes, tapeBytes    sql.NullInt64
	rangeNm, bearing                sql.NullFloat64
}

func readHour(t *testing.T, s *Store, hour time.Time) (hourRow, bool) {
	t.Helper()
	var r hourRow
	err := s.db.QueryRow(`SELECT aircraft_adsb, aircraft_modes, flights_started, positions, messages, max_range_nm,
		max_range_bearing, db_bytes, tape_bytes FROM hourly WHERE hour = ?`, hour.Unix()).
		Scan(&r.adsb, &r.modes, &r.started, &r.positions, &r.messages, &r.rangeNm, &r.bearing, &r.dbBytes, &r.tapeBytes)
	if err == sql.ErrNoRows {
		return r, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return r, true
}

func keyCounts(t *testing.T, s *Store, table, column string, hour time.Time) map[string]int {
	t.Helper()
	rows, err := s.db.Query(`SELECT `+column+`, aircraft FROM `+table+` WHERE hour = ?`, hour.Unix())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatal(err)
		}
		out[k] = n
	}
	return out
}

func TestRollupHour(t *testing.T) {
	s := rollupStore(t, Options{})
	ctx := context.Background()
	for h := -1; h <= 2; h++ {
		if err := s.rollupHour(ctx, t0.Add(time.Duration(h)*time.Hour), nil, sizes{}); err != nil {
			t.Fatal(err)
		}
	}
	r, ok := readHour(t, s, t0)
	if !ok || r.adsb != 3 || r.modes != 1 || r.started != 5 || r.positions != 2 {
		t.Errorf("12:00 = %+v", r)
	}
	if !r.rangeNm.Valid || math.Abs(r.rangeNm.Float64-54.4) > 0.5 || math.Abs(r.bearing.Float64-90) > 1 {
		t.Errorf("max range %v at %v", r.rangeNm, r.bearing)
	}
	if r.messages.Valid || r.dbBytes.Valid || r.tapeBytes.Valid {
		t.Errorf("unknown values stored: %+v", r)
	}
	if c := keyCounts(t, s, "hourly_country", "country", t0); c["Netherlands"] != 1 || c["Germany"] != 1 || c["Israel"] != 1 || c["United Kingdom"] != 1 || len(c) != 4 {
		t.Errorf("countries %v", c)
	}
	if o := keyCounts(t, s, "hourly_operator", "prefix", t0); o["KLM"] != 1 || o["DLH"] != 1 || len(o) != 2 {
		t.Errorf("operators %v", o)
	}

	// a flight over several hours counts in each; an hour with only its positions elsewhere has no range
	for _, c := range []struct {
		h                  int
		adsb, modes, posns int
	}{{-1, 0, 1, 0}, {1, 1, 1, 1}, {2, 0, 1, 0}} {
		r, _ := readHour(t, s, t0.Add(time.Duration(c.h)*time.Hour))
		if r.adsb != c.adsb || r.modes != c.modes || r.positions != c.posns || r.rangeNm.Valid != (c.posns > 0) {
			t.Errorf("hour %+d: %+v", c.h, r)
		}
	}

	// rerunning replaces, with no duplicate keys
	if err := s.rollupHour(ctx, t0, &liveHour{messages: 1234}, sizes{db: new(int64(10)), tape: new(int64(20))}); err != nil {
		t.Fatal(err)
	}
	if r, _ := readHour(t, s, t0); r.adsb != 3 || r.messages.Int64 != 1234 || r.dbBytes.Int64 != 10 || r.tapeBytes.Int64 != 20 {
		t.Errorf("rerun %+v", r)
	}
	if o := keyCounts(t, s, "hourly_operator", "prefix", t0); o["KLM"] != 1 || len(o) != 2 {
		t.Errorf("operators after a rerun %v", o)
	}
}

func TestOperatorPrefix(t *testing.T) {
	for cs, want := range map[string]string{"KLM1023": "KLM", "DLH4AB": "DLH", "N123AB": "", "GBARX": "", "RCH12": "RCH", "": "", "klm1023": "", "AB1234": ""} {
		if got := OperatorPrefix(cs); got != want {
			t.Errorf("%q: %q, want %q", cs, got, want)
		}
	}
}

func TestBackfill(t *testing.T) {
	s := rollupStore(t, Options{})
	ctx := context.Background()
	if err := s.rollupHour(ctx, t0, &liveHour{messages: 7}, sizes{}); err != nil {
		t.Fatal(err)
	}
	s.backfill(ctx, t0.Add(3*time.Hour+30*time.Minute)) // previous hour is 14:00
	for h := -1; h <= 2; h++ {
		r, ok := readHour(t, s, t0.Add(time.Duration(h)*time.Hour))
		if !ok {
			t.Errorf("hour %+d missing", h)
		}
		if h == 0 && r.messages.Int64 != 7 {
			t.Error("backfill recomputed an existing hour")
		}
	}
	if _, ok := readHour(t, s, t0.Add(3*time.Hour)); ok {
		t.Error("the current hour was stored")
	}

	limited := rollupStore(t, Options{StatsRetention: 2 * time.Hour})
	limited.backfill(ctx, t0.Add(3*time.Hour+30*time.Minute))
	for h, want := range map[int]bool{-1: false, 0: false, 1: true, 2: true} {
		if _, ok := readHour(t, limited, t0.Add(time.Duration(h)*time.Hour)); ok != want {
			t.Errorf("with retention, hour %+d stored = %v", h, ok)
		}
	}
}

func TestRunRollups_BackfillsInTheBackground(t *testing.T) {
	s := rollupStore(t, Options{Now: func() time.Time { return t0.Add(3*time.Hour + 30*time.Minute) }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.RunRollups(ctx, RollupSources{})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := readHour(t, s, t0.Add(2*time.Hour)); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no backfill")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunRollups did not stop")
	}
}

func TestPruneRollups(t *testing.T) {
	s := rollupStore(t, Options{StatsRetention: 90 * time.Minute})
	ctx := context.Background()
	for h := -1; h <= 2; h++ {
		if err := s.rollupHour(ctx, t0.Add(time.Duration(h)*time.Hour), nil, sizes{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.pruneRollups(t0.Add(2*time.Hour + 30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for h, want := range map[int]bool{-1: false, 0: false, 1: true, 2: true} {
		if _, ok := readHour(t, s, t0.Add(time.Duration(h)*time.Hour)); ok != want {
			t.Errorf("hour %+d kept = %v", h, ok)
		}
	}
	if len(keyCounts(t, s, "hourly_country", "country", t0)) != 0 {
		t.Error("country rows of a pruned hour kept")
	}
	cut := t0.Add(time.Hour).UnixMilli()
	if queryInt(t, s, `SELECT count(*) FROM hourly_altitude WHERE hour < ?`, t0.Add(time.Hour).Unix()) != 0 ||
		queryInt(t, s, `SELECT count(*) FROM notable_events WHERE t < ?`, cut) != 0 ||
		queryInt(t, s, `SELECT count(*) FROM aircraft_seen WHERE last_seen < ?`, cut) != 0 ||
		queryInt(t, s, `SELECT count(*) FROM aircraft_seen`) == 0 {
		t.Error("altitude, events or sightings of pruned hours kept")
	}
	keep := rollupStore(t, Options{})
	if err := keep.rollupHour(ctx, t0, nil, sizes{}); err != nil {
		t.Fatal(err)
	}
	if err := keep.pruneRollups(t0.Add(1000 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok := readHour(t, keep, t0); !ok {
		t.Error("retention 0 deleted a rollup")
	}
}

func TestLiveSnapshots(t *testing.T) {
	var m liveSnapshots
	c := func(msgs, frames uint64) Counters {
		return Counters{Messages: msgs, Frames: frames, CRCFailures: frames / 10}
	}
	m.boundary(t0, c(100, 1000))
	frames := uint64(1000)
	for i := range 60 { // minutes 20–24 silent
		if i < 20 || i > 24 {
			frames += 50
		}
		m.minute(t0.Add(time.Duration(i+1)*time.Minute), frames)
	}
	m.boundary(t0.Add(time.Hour), c(160, frames))
	l := m.hour(t0)
	if l == nil || l.messages != 60 || l.frames != 2750 || l.crcFailures != 275 || l.silentMinutes != 5 || l.signalDbfs != nil {
		t.Errorf("full hour: %+v", l)
	}
	if m.hour(t0.Add(-time.Hour)) != nil {
		t.Error("an hour without its start boundary has values")
	}
	if l := m.since(t0.Add(time.Hour), Counters{Messages: 170, Frames: frames + 5, SignalFrames: 2, SignalPower: 0.2}); l == nil ||
		l.messages != 10 || l.signalDbfs == nil || math.Abs(*l.signalDbfs+10) > 1e-9 {
		t.Errorf("current hour: %+v", l)
	}

	var restarted liveSnapshots // started mid-hour: only the end boundary of that hour
	restarted.boundary(t0.Add(time.Hour), c(5, 5))
	if restarted.hour(t0) != nil {
		t.Error("a partial hour has values after a restart")
	}
	var gap liveSnapshots // a missed minute sample doesn't count as silent
	gap.minute(t0.Add(time.Minute), 1)
	gap.minute(t0.Add(3*time.Minute), 1)
	gap.boundary(t0, c(0, 0))
	if gap.silent[t0.Unix()] != 0 {
		t.Error("a gap in the sampling counted as silent")
	}
}

func TestRollup_AltitudeBands(t *testing.T) {
	s := rollupStore(t, Options{})
	at := func(min int) int64 { return t0.Add(time.Duration(min) * time.Minute).UnixMilli() }
	for i, alt := range []int{0, 4999, 5000, 37000, 41000, 52000} {
		s.Point(4, tracker.TrackPoint{T: at(51 + i), Lat: receiver.Lat, Lon: receiver.Lon, Alt: alt, AltSrc: tracker.AltBaro})
	}
	s.Point(4, tracker.TrackPoint{T: at(58), Lat: receiver.Lat, Lon: receiver.Lon, OnGround: true})
	if _, err := s.write(drain(s)); err != nil {
		t.Fatal(err)
	}
	if err := s.rollupHour(t.Context(), t0, nil, sizes{}); err != nil {
		t.Fatal(err)
	}
	got := map[int]int64{}
	for _, b := range []int{BandGround, 0, 5000, 35000, 40000, BandUnknown} {
		got[b] = queryInt(t, s, `SELECT coalesce(sum(positions), 0) FROM hourly_altitude WHERE hour = ? AND band = ?`, t0.Unix(), b)
	}
	// rollupStore's two points of 4840D6 have no altitude
	if want := map[int]int64{BandGround: 1, 0: 2, 5000: 1, 35000: 1, 40000: 2, BandUnknown: 2}; !maps.Equal(got, want) {
		t.Errorf("bands %v, want %v", got, want)
	}

	bands, err := s.AltitudeBands(t.Context(), t0, t0.Add(time.Hour), t0.Add(5*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(bands) != 11 || bands[0].Band != "ground" || bands[0].Positions != 1 || bands[1].Band != "0–5k" ||
		*bands[1].MinFt != 0 || bands[9].Band != "≥40k" || bands[9].Positions != 2 || bands[10].Band != "unknown" {
		t.Errorf("%+v", bands)
	}
}

func TestRollup_NotableEvents(t *testing.T) {
	s := rollupStore(t, Options{})
	// 738ABC squawks 7700 at 13:10–13:20; an ADS-B flight of a new aircraft squawks 7600 from 13:30
	for _, m := range []int{70, 75} {
		s.Sample(3, tracker.Sample{T: t0.Add(time.Duration(m) * time.Minute).UnixMilli(), Squawk: "7700"})
	}
	s.Flight(tracker.FlightInfo{ID: 7, ICAO: "A00001", Callsign: "UAL12", Squawk: "7600", ADSB: true,
		FirstSeen: t0.Add(90 * time.Minute), LastSeen: t0.Add(130 * time.Minute)})
	if _, err := s.write(drain(s)); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	for h := -1; h <= 2; h++ {
		if err := s.rollupHour(ctx, t0.Add(time.Duration(h)*time.Hour), nil, sizes{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.rollupHour(ctx, t0.Add(time.Hour), nil, sizes{}); err != nil { // reruns add nothing
		t.Fatal(err)
	}
	n, err := s.NotableEvents(ctx, t0.Add(-time.Hour), t0.Add(3*time.Hour), t0.Add(5*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	// first sightings: 738ABC (11:30), 4840D6, 40621D, 3C6444 (12:00), A00001; emergencies: 738ABC 7700, A00001 7600
	if n.Emergencies != 2 || n.FirstSeen != 5 || len(n.Events) != 7 {
		t.Fatalf("%+v", n)
	}
	e0, e1 := n.Events[0], n.Events[1]
	if e0.ICAO != "A00001" || e0.Squawk != "7600" || e0.Country != "United States" || e1.ICAO != "738ABC" ||
		!e1.T.Equal(t0.Add(70*time.Minute)) || e1.FlightID != 3 {
		t.Errorf("emergencies %+v %+v", e0, e1)
	}
	if fs := n.Events[2:]; fs[0].ICAO != "A00001" || fs[len(fs)-1].ICAO != "738ABC" || fs[1].Kind != KindFirstSeen {
		t.Errorf("first sightings %+v", fs)
	}
	if few, _ := s.NotableEvents(ctx, t0.Add(-time.Hour), t0.Add(3*time.Hour), t0.Add(5*time.Hour), 1); len(few.Events) != 1 || few.FirstSeen != 5 {
		t.Errorf("limit: %+v", few)
	}
}

func TestRollup_FirstSeenOutlivesFlightRetention(t *testing.T) {
	s := rollupStore(t, Options{})
	ctx := t.Context()
	if err := s.rollupHour(ctx, t0, nil, sizes{}); err != nil {
		t.Fatal(err)
	}
	// the old flights of 4840D6 are deleted; it returns days later and isn't new
	if _, err := s.db.Exec(`DELETE FROM flights WHERE icao = '4840D6'`); err != nil {
		t.Fatal(err)
	}
	later := t0.Add(72 * time.Hour)
	s.Flight(tracker.FlightInfo{ID: 9, ICAO: "4840D6", Callsign: "KLM1101", ADSB: true, FirstSeen: later.Add(time.Minute), LastSeen: later.Add(9 * time.Minute)})
	if _, err := s.write(drain(s)); err != nil {
		t.Fatal(err)
	}
	if err := s.rollupHour(ctx, later, nil, sizes{}); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, s, `SELECT count(*) FROM notable_events WHERE icao = '4840D6'`); n != 1 {
		t.Errorf("%d first sightings of a returning aircraft", n)
	}
	if n := queryInt(t, s, `SELECT last_seen FROM aircraft_seen WHERE icao = '4840D6'`); n != later.Add(9*time.Minute).UnixMilli() {
		t.Errorf("last seen %d", n)
	}
}

func TestRollup_KeepsLiveValuesAndUpgradesOldHours(t *testing.T) {
	s := rollupStore(t, Options{})
	ctx := t.Context()
	if err := s.rollupHour(ctx, t0, &liveHour{messages: 50, frames: 80, silentMinutes: 3, signalDbfs: new(-12.5)}, sizes{db: new(int64(9))}); err != nil {
		t.Fatal(err)
	}
	// an hour of an older version: recomputed by the backfill, its live values kept
	if _, err := s.db.Exec(`UPDATE hourly SET rollup_version = 1, aircraft_adsb = 0 WHERE hour = ?`, t0.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM hourly_altitude`); err != nil {
		t.Fatal(err)
	}
	s.backfill(ctx, t0.Add(90*time.Minute))
	r, _ := readHour(t, s, t0)
	if r.adsb != 3 || r.messages.Int64 != 50 || r.dbBytes.Int64 != 9 || queryInt(t, s, `SELECT frames FROM hourly WHERE hour = ?`, t0.Unix()) != 80 ||
		queryInt(t, s, `SELECT rollup_version FROM hourly WHERE hour = ?`, t0.Unix()) != rollupVersion ||
		queryInt(t, s, `SELECT count(*) FROM hourly_altitude WHERE hour = ?`, t0.Unix()) == 0 {
		t.Errorf("upgraded hour %+v", r)
	}
	var sig float64
	if err := s.db.QueryRow(`SELECT signal_dbfs FROM hourly WHERE hour = ?`, t0.Unix()).Scan(&sig); err != nil || sig != -12.5 {
		t.Errorf("signal %v %v", sig, err)
	}

	// hours reaching past the flight retention are left alone
	short := rollupStore(t, Options{Retention: 2 * time.Hour})
	short.backfill(ctx, t0.Add(3*time.Hour+30*time.Minute))
	for h, want := range map[int]bool{0: false, 1: false, 2: true} {
		if _, ok := readHour(t, short, t0.Add(time.Duration(h)*time.Hour)); ok != want {
			t.Errorf("with flight retention, hour %+d stored = %v", h, ok)
		}
	}
}
