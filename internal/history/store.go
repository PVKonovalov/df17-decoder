// Package history keeps flights, thinned track points and Mode-S altitude/squawk samples in SQLite.
package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
	_ "modernc.org/sqlite"
)

const (
	defaultQueueSize     = 1 << 16
	defaultFlushInterval = 15 * time.Second
	retentionInterval    = time.Hour
	busyTimeoutMs        = 5000
	readConns            = 4
)

type Options struct {
	File          string
	FlushInterval time.Duration // batch interval; 15 s when zero
	Retention     time.Duration // closed flights whose last_seen is older are deleted hourly; 0 keeps everything
	QueueSize     int           // events buffered between flushes before new ones are dropped; 65536 when zero
	Logger        *slog.Logger
	Now           func() time.Time // wall clock for retention; time.Now when nil

	ResumeWithin time.Duration // open flights last seen within this before now are kept open for the tracker to resume; 0 closes all
	ResumeTrail  time.Duration // trail length loaded with a resumed flight

	Receiver       *geo.Point    // for the rollups' max range; nil leaves it unknown
	StatsRetention time.Duration // rollups older than this are deleted daily; 0 keeps them
}

// Stats are the store's counters.
type Stats struct {
	FlightsOpen   int64
	PointsWritten uint64
	Dropped       uint64 // events dropped because the queue was full
	WriteErrors   uint64 // failed flushes and retention runs
	LastFlush     time.Time
}

type eventKind uint8

const (
	eventFlight eventKind = iota
	eventPoint
	eventSample
	eventQuality
)

type event struct {
	kind    eventKind
	id      int64
	flight  tracker.FlightInfo
	point   tracker.TrackPoint
	sample  tracker.Sample
	quality tracker.QualityChange
}

// Store is a tracker.FlightSink writing to SQLite. The sink methods never block: they queue the event, or drop
// and count it when the queue is full. Run owns all writes.
type Store struct {
	opt    Options
	db     *sql.DB // the writer's single connection
	ro     *sql.DB // read-only connections for HTTP reads
	queue  chan event
	lastID int64
	resume []tracker.ResumedFlight

	open    map[int64]bool // open flights seen by the writer; Run only
	failing bool           // the last write failed; Run only

	flightsOpen atomic.Int64
	written     atomic.Uint64
	dropped     atomic.Uint64
	writeErrors atomic.Uint64
	lastFlush   atomic.Int64 // unix ms

	live liveSnapshots                 // counters at hour boundaries and silent minutes (RunRollups)
	src  atomic.Pointer[RollupSources] // set by RunRollups
	cur  currentCache                  // the partial current hour, for the stats API
}

// dsn builds the modernc.org/sqlite DSN for file with the given pragmas, in order.
func dsn(file string, pragmas ...string) string {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	return "file:" + file + "?" + q.Encode()
}

// Open opens (creating if needed) the database at opt.File, applies migrations and closes flights left open by a
// crash, with last_seen set to their newest stored point.
func Open(opt Options) (*Store, error) {
	if opt.FlushInterval <= 0 {
		opt.FlushInterval = defaultFlushInterval
	}
	if opt.QueueSize <= 0 {
		opt.QueueSize = defaultQueueSize
	}
	if opt.Logger == nil {
		opt.Logger = slog.New(slog.DiscardHandler)
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	db, err := sql.Open("sqlite", dsn(opt.File, "auto_vacuum(INCREMENTAL)", "journal_mode(WAL)",
		"synchronous(NORMAL)", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMs)))
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{opt: opt, db: db, queue: make(chan event, opt.QueueSize), open: map[int64]bool{}}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	if s.ro, err = sql.Open("sqlite", dsn(opt.File, "query_only(1)", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMs))+"&mode=ro"); err != nil {
		db.Close()
		return nil, fmt.Errorf("history: %w", err)
	}
	s.ro.SetMaxOpenConns(readConns)
	return s, nil
}

func (s *Store) init() error {
	from, to, err := migrate(s.db)
	if err != nil {
		return err
	}
	if from != to {
		s.opt.Logger.Info("history schema migrated", "file", s.opt.File, "from", from, "to", to)
	}
	if from == 0 {
		if _, err := s.db.Exec(`DELETE FROM meta WHERE key = ?`, backfillKey); err != nil {
			return fmt.Errorf("history: %w", err)
		}
	}
	if _, err := s.db.Exec(`UPDATE flights SET last_seen = max(last_seen,
		coalesce((SELECT max(t) FROM positions WHERE flight_id = flights.id), 0),
		coalesce((SELECT max(t) FROM samples WHERE flight_id = flights.id), 0))
		WHERE closed = 0`); err != nil {
		return fmt.Errorf("history: recovery: %w", err)
	}
	cutoff := s.opt.Now().Add(-s.opt.ResumeWithin).UnixMilli()
	if s.opt.ResumeWithin <= 0 {
		cutoff = math.MaxInt64
	}
	if err := s.closeStale(cutoff); err != nil {
		return fmt.Errorf("history: recovery: %w", err)
	}
	if s.resume, err = s.openFlights(); err != nil {
		return fmt.Errorf("history: resume: %w", err)
	}
	for _, r := range s.resume {
		s.open[r.Info.ID] = true
	}
	s.flightsOpen.Store(int64(len(s.open)))
	if len(s.resume) > 0 {
		s.opt.Logger.Info("history: resuming flights of the previous run", "flights", len(s.resume))
	}
	if err := s.db.QueryRow(`SELECT coalesce(max(id), 0) FROM flights`).Scan(&s.lastID); err != nil {
		return fmt.Errorf("history: %w", err)
	}
	return nil
}

// openFlights loads the flights still open, with their positions of the last ResumeTrail before their last_seen.
func (s *Store) openFlights() ([]tracker.ResumedFlight, error) {
	rows, err := s.db.Query(`SELECT id, icao, coalesce(callsign, ''), coalesce(squawk, ''), coalesce(category, ''),
		adsb, first_seen, last_seen, points, max_alt, min_distance_nm, max_distance_nm FROM flights WHERE closed = 0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var out []tracker.ResumedFlight
	for rows.Next() {
		var f tracker.FlightInfo
		var first, last int64
		if err := rows.Scan(&f.ID, &f.ICAO, &f.Callsign, &f.Squawk, &f.Category, &f.ADSB, &first, &last, &f.Points,
			&f.MaxAlt, &f.MinDistanceNm, &f.MaxDistanceNm); err != nil {
			rows.Close()
			return nil, err
		}
		f.FirstSeen, f.LastSeen = time.UnixMilli(first).UTC(), time.UnixMilli(last).UTC()
		if icao, err := strconv.ParseUint(f.ICAO, 16, 24); err == nil {
			f.Country = tracker.Country(uint32(icao))
		}
		out = append(out, tracker.ResumedFlight{Info: f})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		f := &out[i]
		if f.Trail, err = s.trail(f.Info.ID, f.Info.LastSeen.Add(-s.opt.ResumeTrail).UnixMilli()); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// trail returns the stored positions of flight id from unix ms since on, oldest first.
func (s *Store) trail(id, since int64) ([]tracker.TrackPoint, error) {
	rows, err := s.db.Query(`SELECT t, lat, lon, alt, alt_gnss, gs, track, vr, on_ground FROM positions
		WHERE flight_id = ? AND t >= ? ORDER BY t`, id, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tracker.TrackPoint
	for rows.Next() {
		var (
			p         tracker.TrackPoint
			alt, vr   sql.NullInt64
			gs, track sql.NullFloat64
			gnss      bool
		)
		if err := rows.Scan(&p.T, &p.Lat, &p.Lon, &alt, &gnss, &gs, &track, &vr, &p.OnGround); err != nil {
			return nil, err
		}
		switch {
		case !alt.Valid:
		case gnss:
			p.Alt, p.AltSrc = int(alt.Int64), tracker.AltGNSS
		default:
			p.Alt, p.AltSrc = int(alt.Int64), tracker.AltBaro
		}
		if gs.Valid {
			p.GS, p.Known = gs.Float64, p.Known|tracker.KnownGS
		}
		if track.Valid {
			p.Track, p.Known = track.Float64, p.Known|tracker.KnownTrack
		}
		if vr.Valid {
			p.VR, p.Known = int(vr.Int64), p.Known|tracker.KnownVR
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ResumeFlights returns the flights the previous run left open and that are recent enough to continue
// (Options.ResumeWithin), for tracker.Options.Resume.
func (s *Store) ResumeFlights() []tracker.ResumedFlight { return s.resume }

// LastFlightID is the highest flight ID in the database when it was opened; new flights continue after it.
func (s *Store) LastFlightID() int64 { return s.lastID }

// DB returns the database handle (single connection, shared with the writer).
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return errors.Join(s.ro.Close(), s.db.Close()) }

func (s *Store) Flight(f tracker.FlightInfo) {
	s.enqueue(event{kind: eventFlight, id: f.ID, flight: f})
}

func (s *Store) Point(id int64, p tracker.TrackPoint) {
	s.enqueue(event{kind: eventPoint, id: id, point: p})
}

func (s *Store) Sample(id int64, smp tracker.Sample) {
	s.enqueue(event{kind: eventSample, id: id, sample: smp})
}

func (s *Store) Quality(id int64, q tracker.QualityChange) {
	s.enqueue(event{kind: eventQuality, id: id, quality: q})
}

func (s *Store) enqueue(ev event) {
	select {
	case s.queue <- ev:
	default:
		s.dropped.Add(1)
	}
}

func (s *Store) Stats() Stats {
	st := Stats{
		FlightsOpen:   s.flightsOpen.Load(),
		PointsWritten: s.written.Load(),
		Dropped:       s.dropped.Load(),
		WriteErrors:   s.writeErrors.Load(),
	}
	if ms := s.lastFlush.Load(); ms != 0 {
		st.LastFlush = time.UnixMilli(ms)
	}
	return st
}

// Run writes queued events every FlushInterval in one transaction and applies retention hourly, until ctx is
// cancelled; then it writes everything still queued and returns.
func (s *Store) Run(ctx context.Context) {
	flush := time.NewTicker(s.opt.FlushInterval)
	defer flush.Stop()
	prune := time.NewTicker(retentionInterval)
	defer prune.Stop()
	s.prune()
	s.backfillCounts()
	step := s.backfillStep()

	var batch []event
	for {
		select {
		case <-step:
			step = s.backfillStep()
		case ev := <-s.queue:
			batch = append(batch, ev)
		case <-flush.C:
			batch = s.flush(batch)
		case <-prune.C:
			s.prune()
		case <-ctx.Done():
			for {
				select {
				case ev := <-s.queue:
					batch = append(batch, ev)
				default:
					s.flush(batch)
					return
				}
			}
		}
	}
}

// flush writes batch and returns it emptied. A failed batch is discarded, so memory stays bounded; failures are
// logged once per streak.
func (s *Store) flush(batch []event) []event {
	for _, ev := range batch {
		if ev.kind == eventFlight {
			if ev.flight.Closed {
				delete(s.open, ev.id)
			} else {
				s.open[ev.id] = true
			}
		}
	}
	s.flightsOpen.Store(int64(len(s.open)))

	n, err := s.write(batch)
	switch {
	case err != nil:
		s.writeErrors.Add(1)
		if !s.failing {
			s.opt.Logger.Warn("history write failed, dropping the batch; further failures are not logged", "file", s.opt.File, "events", len(batch), "err", err)
		}
	case s.failing:
		s.opt.Logger.Info("history writes work again", "file", s.opt.File)
	}
	s.failing = err != nil
	if err == nil {
		s.written.Add(uint64(n))
		s.lastFlush.Store(s.opt.Now().UnixMilli())
	}
	clear(batch)
	return batch[:0]
}

type flightAgg struct {
	points     int
	last       int64
	maxAlt     *int
	minD, maxD *float64
}

func (a *flightAgg) addDistance(d float64) {
	if a.minD == nil || d < *a.minD {
		a.minD = &d
	}
	if a.maxD == nil || d > *a.maxD {
		a.maxD = &d
	}
}

func (a *flightAgg) add(t int64, alt *int) {
	a.last = max(a.last, t)
	if alt != nil && (a.maxAlt == nil || *alt > *a.maxAlt) {
		a.maxAlt = alt
	}
}

// write stores batch in one transaction and returns the number of new positions.
func (s *Store) write(batch []event) (n int, err error) {
	if len(batch) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	upsert, err := tx.Prepare(`INSERT INTO flights(id, icao, callsign, squawk, category, adsb,
			first_seen, last_seen, closed, max_alt, min_distance_nm, max_distance_nm)
		VALUES(?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12)
		ON CONFLICT(id) DO UPDATE SET callsign = excluded.callsign, squawk = excluded.squawk,
			category = excluded.category, adsb = excluded.adsb, last_seen = max(last_seen, excluded.last_seen),
			closed = excluded.closed,
			max_alt = CASE WHEN ?10 IS NULL THEN max_alt ELSE max(coalesce(max_alt, ?10), ?10) END,
			min_distance_nm = CASE WHEN ?11 IS NULL THEN min_distance_nm
				ELSE min(coalesce(min_distance_nm, ?11), ?11) END,
			max_distance_nm = CASE WHEN ?12 IS NULL THEN max_distance_nm
				ELSE max(coalesce(max_distance_nm, ?12), ?12) END`)
	if err != nil {
		return 0, err
	}
	insPos, err := tx.Prepare(`INSERT OR IGNORE INTO positions(flight_id, t, lat, lon, alt, alt_gnss, gs, track, vr, on_ground)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	insSample, err := tx.Prepare(`INSERT OR REPLACE INTO samples(flight_id, t, alt, squawk) VALUES(?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}

	insQuality, err := tx.Prepare(`INSERT OR REPLACE INTO position_quality(flight_id, t, quality) VALUES(?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	wasClosed, err := tx.Prepare(`SELECT closed FROM flights WHERE id = ?`)
	if err != nil {
		return 0, err
	}
	var closing []int64

	aggs := map[int64]*flightAgg{}
	agg := func(id int64) *flightAgg {
		a := aggs[id]
		if a == nil {
			a = &flightAgg{}
			aggs[id] = a
		}
		return a
	}
	for _, ev := range batch {
		switch ev.kind {
		case eventFlight:
			f := ev.flight
			if f.Closed {
				var c bool
				if e := wasClosed.QueryRow(f.ID).Scan(&c); errors.Is(e, sql.ErrNoRows) || e == nil && !c {
					closing = append(closing, f.ID)
				} else if e != nil {
					return 0, e
				}
			}
			_, err = upsert.Exec(f.ID, f.ICAO, nullString(f.Callsign), nullString(f.Squawk), nullString(f.Category), f.ADSB, f.FirstSeen.UnixMilli(), f.LastSeen.UnixMilli(), f.Closed, f.MaxAlt, f.MinDistanceNm, f.MaxDistanceNm)
		case eventPoint:
			p := ev.point
			var alt *int
			if p.AltSrc != tracker.AltUnknown {
				alt = &p.Alt
			}
			var res sql.Result
			res, err = insPos.Exec(ev.id, p.T, p.Lat, p.Lon, alt, p.AltSrc == tracker.AltGNSS,
				known(p.Known, tracker.KnownGS, p.GS), known(p.Known, tracker.KnownTrack, p.Track),
				known(p.Known, tracker.KnownVR, p.VR), p.OnGround)
			if err == nil {
				if k, _ := res.RowsAffected(); k == 1 {
					n++
					a := agg(ev.id)
					a.points++
					a.add(p.T, alt)
					if s.opt.Receiver != nil {
						a.addDistance(geo.DistanceNm(*s.opt.Receiver, geo.Point{Lat: p.Lat, Lon: p.Lon}))
					}
				}
			}
		case eventSample:
			smp := ev.sample
			var alt *int
			if smp.HasAlt {
				alt = &smp.Alt
			}
			if _, err = insSample.Exec(ev.id, smp.T, alt, nullString(smp.Squawk)); err == nil {
				agg(ev.id).add(smp.T, alt)
			}
		case eventQuality:
			_, err = insQuality.Exec(ev.id, ev.quality.T, string(ev.quality.Quality))
		}
		if err != nil {
			return 0, err
		}
	}
	for id, a := range aggs {
		if _, err = tx.Exec(`UPDATE flights SET points = points + ?1, last_seen = max(last_seen, ?2),
				max_alt = CASE WHEN ?3 IS NULL THEN max_alt ELSE max(coalesce(max_alt, ?3), ?3) END,
				min_distance_nm = CASE WHEN ?5 IS NULL THEN min_distance_nm ELSE min(coalesce(min_distance_nm, ?5), ?5) END,
				max_distance_nm = CASE WHEN ?6 IS NULL THEN max_distance_nm ELSE max(coalesce(max_distance_nm, ?6), ?6) END
			WHERE id = ?4`, a.points, a.last, a.maxAlt, id, a.minD, a.maxD); err != nil {
			return 0, err
		}
	}
	for _, id := range closing {
		if err = countClosed(tx, `id = ?`, id); err != nil {
			return 0, err
		}
	}
	return n, tx.Commit()
}

// prune deletes closed flights (with their positions, samples and position quality) last seen before now - Retention, then
// returns free pages to the file system.
func (s *Store) prune() {
	if s.opt.Retention <= 0 {
		return
	}
	cutoff := s.opt.Now().Add(-s.opt.Retention).UnixMilli()
	n, err := s.deleteBefore(cutoff)
	if err == nil {
		_, err = s.db.Exec(`PRAGMA incremental_vacuum`)
	}
	if err != nil {
		s.writeErrors.Add(1)
		s.opt.Logger.Warn("history retention failed", "file", s.opt.File, "err", err)
		return
	}
	if n > 0 {
		s.opt.Logger.Info("history retention", "deletedFlights", n, "before", time.UnixMilli(cutoff).UTC())
	}
}

func (s *Store) deleteBefore(cutoff int64) (n int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	const old = `SELECT id FROM flights WHERE closed = 1 AND last_seen < ?`
	for _, q := range []string{
		`DELETE FROM positions WHERE flight_id IN (` + old + `)`,
		`DELETE FROM samples WHERE flight_id IN (` + old + `)`,
		`DELETE FROM position_quality WHERE flight_id IN (` + old + `)`,
	} {
		if _, err = tx.Exec(q, cutoff); err != nil {
			return 0, err
		}
	}
	res, err := tx.Exec(`DELETE FROM flights WHERE closed = 1 AND last_seen < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ = res.RowsAffected()
	return n, tx.Commit()
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// known returns v when flag is set in k, else nil (SQL NULL).
func known[T any](k, flag tracker.Known, v T) any {
	if k&flag == 0 {
		return nil
	}
	return v
}
