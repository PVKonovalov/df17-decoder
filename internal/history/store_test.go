package history

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

var t0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func open(t *testing.T, file string, opt Options) *Store {
	t.Helper()
	opt.File = file
	s, err := Open(opt)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// run starts the writer and returns a function that stops it (final flush included).
func run(s *Store) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { s.Run(ctx) })
	return func() { cancel(); wg.Wait() }
}

func queryInt(t *testing.T, s *Store, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func flight(id int64, at time.Time, closed bool) tracker.FlightInfo {
	return tracker.FlightInfo{ID: id, ICAO: "4840D6", Callsign: "KLM1023", Country: "Netherlands", ADSB: true,
		FirstSeen: at, LastSeen: at, Closed: closed}
}

func point(at time.Time, alt int) tracker.TrackPoint {
	return tracker.TrackPoint{T: at.UnixMilli(), Lat: 25.1, Lon: 22.9, Alt: alt, AltSrc: tracker.AltBaro,
		GS: 420, Known: tracker.KnownGS}
}

func TestOpen_CreatesSchema(t *testing.T) {
	file := filepath.Join(t.TempDir(), "h.db")
	s := open(t, file, Options{})
	if v := queryInt(t, s, `SELECT value FROM meta WHERE key = 'schema_version'`); v != int64(SchemaVersion) || SchemaVersion != 7 {
		t.Errorf("schema version %d, want 7", v)
	}
	if v := queryInt(t, s, `PRAGMA auto_vacuum`); v != 2 {
		t.Errorf("auto_vacuum = %d, want 2 (incremental)", v)
	}
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q (%v)", mode, err)
	}
	for _, idx := range []string{"flights_time", "flights_icao", "positions_t"} {
		if queryInt(t, s, `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, idx) != 1 {
			t.Errorf("index %s missing", idx)
		}
	}
	s.Close()

	s = open(t, file, Options{}) // reopening applies nothing
	defer s.Close()
	if s.LastFlightID() != 0 {
		t.Errorf("last flight %d", s.LastFlightID())
	}
}

func TestOpen_RejectsNewerSchema(t *testing.T) {
	file := filepath.Join(t.TempDir(), "h.db")
	s := open(t, file, Options{})
	if _, err := s.db.Exec(`UPDATE meta SET value = '99' WHERE key = 'schema_version'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(Options{File: file}); err == nil {
		t.Error("opened a database with a newer schema")
	}
}

func TestStore_WritesInBatches(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{FlushInterval: time.Hour})
	defer s.Close()
	stop := run(s)

	s.Flight(flight(7, t0, false))
	for i, alt := range []int{30000, 31000, 30500} {
		s.Point(7, point(t0.Add(time.Duration(i)*5*time.Second), alt))
	}
	s.Point(7, point(t0.Add(10*time.Second), 0)) // same t: ignored
	s.Sample(7, tracker.Sample{T: t0.Add(12 * time.Second).UnixMilli(), Squawk: "1000"})
	time.Sleep(50 * time.Millisecond)
	if n := queryInt(t, s, `SELECT count(*) FROM positions`); n != 0 {
		t.Errorf("%d positions written before the flush", n)
	}

	stop()
	st := s.Stats()
	if st.PointsWritten != 3 || st.FlightsOpen != 1 || st.Dropped != 0 || st.WriteErrors != 0 || st.LastFlush.IsZero() {
		t.Errorf("stats = %+v", st)
	}
	var (
		points, maxAlt, lastSeen int64
		callsign                 string
	)
	if err := s.db.QueryRow(`SELECT points, max_alt, last_seen, callsign FROM flights WHERE id = 7`).Scan(&points, &maxAlt, &lastSeen, &callsign); err != nil {
		t.Fatal(err)
	}
	if points != 3 || maxAlt != 31000 || lastSeen != t0.Add(12*time.Second).UnixMilli() || callsign != "KLM1023" {
		t.Errorf("flight: points %d maxAlt %d lastSeen %d callsign %q", points, maxAlt, lastSeen, callsign)
	}
	if n := queryInt(t, s, `SELECT count(*) FROM positions WHERE gs = 420 AND track IS NULL AND alt_gnss = 0`); n != 3 {
		t.Errorf("%d positions with the expected columns", n)
	}
	if n := queryInt(t, s, `SELECT count(*) FROM samples WHERE alt IS NULL AND squawk = '1000'`); n != 1 {
		t.Errorf("%d samples", n)
	}
}

func TestStore_FlushesEveryInterval(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{FlushInterval: 20 * time.Millisecond})
	defer s.Close()
	stop := run(s)
	defer stop()
	s.Flight(flight(1, t0, false))
	s.Point(1, point(t0, 1000))
	deadline := time.Now().Add(2 * time.Second)
	for s.Stats().PointsWritten < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Stats().PointsWritten != 1 {
		t.Error("point not flushed while running")
	}
}

func TestStore_FullQueueDropsAndCounts(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{QueueSize: 2})
	defer s.Close()
	done := make(chan struct{})
	go func() {
		for i := range 5 {
			s.Point(1, point(t0.Add(time.Duration(i)*time.Second), 1000))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a full queue blocked the caller")
	}
	if d := s.Stats().Dropped; d != 3 {
		t.Errorf("dropped %d, want 3", d)
	}
}

func TestOpen_RecoversOpenFlights(t *testing.T) {
	file := filepath.Join(t.TempDir(), "h.db")
	s := open(t, file, Options{})
	stop := run(s)
	s.Flight(flight(3, t0, false))
	s.Point(3, point(t0.Add(time.Minute), 1000))
	s.Point(3, point(t0.Add(2*time.Minute), 1000))
	s.Flight(flight(4, t0, true))
	stop()
	// simulate a crash: the flight's last_seen lags its newest point
	if _, err := s.db.Exec(`UPDATE flights SET last_seen = ? WHERE id = 3`, t0.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s = open(t, file, Options{})
	defer s.Close()
	if n := queryInt(t, s, `SELECT count(*) FROM flights WHERE closed = 0`); n != 0 {
		t.Errorf("%d flights still open", n)
	}
	if ls := queryInt(t, s, `SELECT last_seen FROM flights WHERE id = 3`); ls != t0.Add(2*time.Minute).UnixMilli() {
		t.Errorf("last_seen = %d, want the newest point", ls)
	}
	if s.LastFlightID() != 4 {
		t.Errorf("last flight id %d, want 4", s.LastFlightID())
	}
}

func TestOpen_ResumesRecentFlights(t *testing.T) {
	file := filepath.Join(t.TempDir(), "h.db")
	s := open(t, file, Options{})
	stop := run(s)
	old, recent := flight(5, t0, false), flight(6, t0, false)
	recent.ICAO, recent.MaxAlt = "738001", new(31000)
	s.Flight(old)
	s.Flight(recent)
	s.Point(6, point(t0.Add(time.Minute), 30000))
	s.Point(6, point(t0.Add(20*time.Minute), 31000))
	stop()
	s.Close()

	now := t0.Add(25 * time.Minute)
	s = open(t, file, Options{ResumeWithin: 11 * time.Minute, ResumeTrail: 10 * time.Minute, Now: func() time.Time { return now }})
	defer s.Close()
	r := s.ResumeFlights()
	if len(r) != 1 || r[0].Info.ID != 6 || r[0].Info.Closed || *r[0].Info.MaxAlt != 31000 || r[0].Info.Country == "" ||
		!r[0].Info.LastSeen.Equal(t0.Add(20*time.Minute)) {
		t.Fatalf("resumed %+v", r)
	}
	if len(r[0].Trail) != 1 || r[0].Trail[0] != point(t0.Add(20*time.Minute), 31000) {
		t.Errorf("trail (last 10 min) %+v", r[0].Trail)
	}
	if n := queryInt(t, s, `SELECT closed FROM flights WHERE id = 5`); n != 1 {
		t.Error("an old open flight was not closed")
	}
	if s.Stats().FlightsOpen != 1 {
		t.Errorf("open flights %d, want 1", s.Stats().FlightsOpen)
	}
}

func TestStore_RetentionDeletesOldFlights(t *testing.T) {
	now := t0.Add(31 * 24 * time.Hour)
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{Retention: 30 * 24 * time.Hour, Now: func() time.Time { return now }})
	defer s.Close()
	for _, f := range []tracker.FlightInfo{flight(1, t0, true), flight(2, now.Add(-time.Hour), true)} {
		s.Flight(f)
		s.Point(f.ID, point(f.LastSeen, 1000))
		s.Sample(f.ID, tracker.Sample{T: f.LastSeen.UnixMilli(), Alt: 1000, HasAlt: true})
	}
	if _, err := s.write(drain(s)); err != nil {
		t.Fatal(err)
	}

	s.prune()
	if ids := queryInt(t, s, `SELECT group_concat(id) FROM flights`); ids != 2 {
		t.Errorf("flights left: %d, want 2", ids)
	}
	for _, table := range []string{"positions", "samples"} {
		if n := queryInt(t, s, `SELECT count(*) FROM `+table+` WHERE flight_id = 1`); n != 0 {
			t.Errorf("%d %s of the deleted flight left", n, table)
		}
		if n := queryInt(t, s, `SELECT count(*) FROM `+table+` WHERE flight_id = 2`); n != 1 {
			t.Errorf("%d %s of the kept flight", n, table)
		}
	}
	if s.Stats().WriteErrors != 0 {
		t.Errorf("write errors %d", s.Stats().WriteErrors)
	}
}

func drain(s *Store) (batch []event) {
	for {
		select {
		case ev := <-s.queue:
			batch = append(batch, ev)
		default:
			return batch
		}
	}
}

func TestStore_WriteFailuresLoggedOncePerStreak(t *testing.T) {
	var buf bytes.Buffer
	file := filepath.Join(t.TempDir(), "h.db")
	s := open(t, file, Options{Logger: slog.New(slog.NewTextHandler(&buf, nil))})
	good := s.db
	bad, err := sql.Open("sqlite", dsn(file))
	if err != nil {
		t.Fatal(err)
	}
	bad.Close()
	s.db = bad
	for i := range 3 {
		s.flush([]event{{kind: eventPoint, id: 1, point: point(t0.Add(time.Duration(i)*time.Second), 1000)}})
	}
	if n := strings.Count(buf.String(), "history write failed"); n != 1 || s.Stats().WriteErrors != 3 {
		t.Fatalf("%d warnings, %d errors; log %s", n, s.Stats().WriteErrors, &buf)
	}

	s.db = good
	defer s.Close()
	s.flush([]event{{kind: eventPoint, id: 1, point: point(t0, 1000)}})
	if !strings.Contains(buf.String(), "history writes work again") || s.Stats().PointsWritten != 1 {
		t.Errorf("no recovery: %+v; log %s", s.Stats(), &buf)
	}
}
