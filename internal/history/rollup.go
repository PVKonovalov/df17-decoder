package history

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

const (
	rollupDelay      = 5 * time.Minute // after the hour, so the batched writer has flushed it
	rollupPruneEvery = 24 * time.Hour
	backfillLogEvery = 500
	hourMs           = int64(time.Hour / time.Millisecond)
)

var operatorRe = regexp.MustCompile(`^[A-Z]{3}[0-9]`)

// OperatorPrefix returns the airline designator of a callsign (its first 3 letters when they are followed by a
// digit), or "" when the callsign doesn't look like an airline flight.
func OperatorPrefix(callsign string) string {
	if !operatorRe.MatchString(callsign) {
		return ""
	}
	return callsign[:3]
}

// Counters are the live service counters the hourly job snapshots; SignalPower is the summed linear power of the
// SignalFrames frames that carried a level.
type Counters struct {
	Messages, Frames, CRCFailures, FixedBitErrors, CPRRejected, Reconnects, SignalFrames uint64
	SignalPower                                                                          float64
}

// RollupSources are the live values the hourly job samples; any may be nil.
type RollupSources struct {
	Counters  func() Counters      // the live counters
	TapeBytes func() (int64, bool) // the tape's size, false when the tape is disabled
}

// sizes are the byte counts stored with an hour; nil when unknown.
type sizes struct{ db, tape *int64 }

// liveHour is what only a running process knows: counter differences and silent minutes.
type liveHour struct {
	messages, frames, crcFailures, fixedBits, cprRejected, reconnects, silentMinutes int64
	signalFrames                                                                     int64
	signalPower                                                                      float64
}

// dbfs is the average signal level, nil without frames carrying one.
func (l *liveHour) dbfs() *float64 {
	if l.signalFrames <= 0 || l.signalPower <= 0 {
		return nil
	}
	return new(10 * math.Log10(l.signalPower/float64(l.signalFrames)))
}

func (l *liveHour) add(o liveHour) {
	l.messages += o.messages
	l.frames += o.frames
	l.crcFailures += o.crcFailures
	l.fixedBits += o.fixedBits
	l.cprRejected += o.cprRejected
	l.reconnects += o.reconnects
	l.silentMinutes += o.silentMinutes
	l.signalFrames += o.signalFrames
	l.signalPower += o.signalPower
}

// carried is the part of an hour a previous run saw, from the hour's start until end.
type carried struct {
	l   liveHour
	end time.Time
}

// liveSnapshots keeps the counters at hour boundaries and at this run's start, the silent minutes per hour, and
// the parts of hours carried over from the previous run (saved on shutdown). An hour's values are known when
// they cover it from its start: this run saw the boundary, or a carried part continues into this run, the time in
// between counting as silent (nothing was received).
type liveSnapshots struct {
	mu        sync.Mutex
	snaps     map[int64]Counters // hour start, unix s → counters
	silent    map[int64]int64    // hour start → minutes without a frame
	carry     map[int64]carried  // hour start → part seen by the previous run
	startAt   time.Time          // this run's start; zero before begin
	start     Counters
	lastMin   time.Time // the previous minute sample
	lastFrame uint64
}

func (m *liveSnapshots) init() {
	if m.snaps == nil {
		m.snaps, m.silent, m.carry = map[int64]Counters{}, map[int64]int64{}, map[int64]carried{}
	}
}

// begin records this run's start at with counters c and the parts carried over from the previous run.
func (m *liveSnapshots) begin(at time.Time, c Counters, carry map[int64]carried) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	m.startAt, m.start = at, c
	maps.Copy(m.carry, carry)
}

// boundary stores the counters at the start of hour and forgets snapshots older than 3 hours.
func (m *liveSnapshots) boundary(hour time.Time, c Counters) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	m.snaps[hour.Unix()] = c
	old := hour.Add(-3 * time.Hour).Unix()
	for h := range m.snaps {
		if h < old {
			delete(m.snaps, h)
			delete(m.silent, h)
		}
	}
	for h := range m.carry {
		if h < old {
			delete(m.carry, h)
		}
	}
}

// minute samples the frame counter at the minute boundary at; a minute whose count didn't move is silent.
func (m *liveSnapshots) minute(at time.Time, frames uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	if m.lastMin.Equal(at.Add(-time.Minute)) && frames == m.lastFrame {
		m.silent[m.lastMin.Truncate(time.Hour).Unix()]++
	}
	m.lastMin, m.lastFrame = at, frames
}

func diff(a, b Counters) *liveHour {
	if b.Messages < a.Messages || b.Frames < a.Frames {
		return nil
	}
	l := &liveHour{
		messages: int64(b.Messages - a.Messages), frames: int64(b.Frames - a.Frames),
		crcFailures: int64(b.CRCFailures - a.CRCFailures), fixedBits: int64(b.FixedBitErrors - a.FixedBitErrors),
		cprRejected: int64(b.CPRRejected - a.CPRRejected), reconnects: int64(b.Reconnects - a.Reconnects),
	}
	if b.SignalFrames > a.SignalFrames && b.SignalPower > a.SignalPower {
		l.signalFrames, l.signalPower = int64(b.SignalFrames-a.SignalFrames), b.SignalPower-a.SignalPower
	}
	return l
}

// part returns the values of the hour starting at hour, from its start until the counters end; nil when they
// don't cover the hour from its start. Called with m.mu held.
func (m *liveSnapshots) part(hour time.Time, end Counters) *liveHour {
	key, next := hour.Unix(), hour.Add(time.Hour)
	if a, ok := m.snaps[key]; ok {
		l := diff(a, end)
		if l != nil {
			l.silentMinutes = m.silent[key]
		}
		return l
	}
	c, ok := m.carry[key]
	switch {
	case !ok:
		return nil
	case !c.end.Before(next): // a whole hour from the previous run
		return &c.l
	case m.startAt.IsZero() || m.startAt.Before(c.end):
		return nil
	case !m.startAt.Before(next): // down from c.end until after the hour
		l := c.l
		l.silentMinutes += int64(next.Sub(c.end) / time.Minute)
		return &l
	}
	l := diff(m.start, end)
	if l == nil {
		return nil
	}
	l.add(c.l)
	l.silentMinutes += m.silent[key] + int64(m.startAt.Sub(c.end)/time.Minute)
	return l
}

// since returns the live values of the hour starting at hour up to the counters now, nil when its start was missed.
func (m *liveSnapshots) since(hour time.Time, now Counters) *liveHour {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.part(hour, now)
}

// hour returns the live values of the finished hour starting at hour, nil when they don't cover it.
func (m *liveSnapshots) hour(hour time.Time) *liveHour {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hourLocked(hour)
}

func (m *liveSnapshots) hourLocked(hour time.Time) *liveHour {
	next := hour.Add(time.Hour)
	if end, ok := m.snaps[next.Unix()]; ok {
		return m.part(hour, end)
	}
	if c, ok := m.carry[hour.Unix()]; ok && (!c.end.Before(next) || !m.startAt.Before(next)) {
		return m.part(hour, Counters{})
	}
	return nil
}

// unsaved returns the parts this run would lose at now: the current hour so far, and the previous hour when its
// rollup (at rollupDelay past the hour) hasn't run yet; by hour start, with the time each part ends.
func (m *liveSnapshots) unsaved(now time.Time, c Counters) map[int64]carried {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[int64]carried{}
	cur := now.Truncate(time.Hour)
	if l := m.part(cur, c); l != nil {
		out[cur.Unix()] = carried{l: *l, end: now}
	}
	if prev := cur.Add(-time.Hour); now.Before(cur.Add(rollupDelay)) {
		if l := m.hourLocked(prev); l != nil {
			out[prev.Unix()] = carried{l: *l, end: cur}
		}
	}
	return out
}

// RunRollups computes the hourly rollups until ctx is cancelled: a background backfill of missing hours first,
// then each hour at HH:05 the previous hour, with the counters snapshotted at every hour boundary and the frame
// counter every minute (silent minutes);
// rollups older than StatsRetention are deleted daily.
func (s *Store) RunRollups(ctx context.Context, src RollupSources) {
	s.src.Store(&src)
	live := &s.live
	now := s.opt.Now()
	if src.Counters != nil {
		carry, err := s.loadLive()
		if err != nil {
			s.opt.Logger.Warn("stats: reading the previous run's counters failed", "err", err)
		}
		live.begin(now, src.Counters(), carry)
		defer func() {
			if err := s.saveLive(live.unsaved(s.opt.Now(), src.Counters())); err != nil {
				s.opt.Logger.Warn("stats: saving the counters of the unfinished hours failed", "err", err)
			}
		}()
	}
	nextBoundary := now.Truncate(time.Hour).Add(time.Hour)
	nextMinute := now.Truncate(time.Minute).Add(time.Minute)
	nextRollup := now.Truncate(time.Hour).Add(rollupDelay)
	if !nextRollup.After(now) {
		nextRollup = nextRollup.Add(time.Hour)
	}
	nextPrune := now
	backfilled := make(chan struct{})
	go func(now time.Time) {
		defer close(backfilled)
		s.backfill(ctx, now)
	}(now)
	defer func() { <-backfilled }()
	dailyBuilt := make(chan struct{})
	go func() {
		defer close(dailyBuilt)
		if err := s.buildDaily(ctx); err != nil && ctx.Err() == nil {
			s.opt.Logger.Warn("daily aircraft: building failed", "err", err)
		}
	}()
	defer func() { <-dailyBuilt }()

	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		now = s.opt.Now()
		if !now.Before(nextMinute) {
			if src.Counters != nil {
				c := src.Counters()
				live.minute(nextMinute, c.Frames)
				if !nextMinute.Before(nextBoundary) {
					live.boundary(nextBoundary, c)
				}
			}
			if !nextMinute.Before(nextBoundary) {
				nextBoundary = nextBoundary.Add(time.Hour)
			}
			nextMinute = nextMinute.Add(time.Minute)
			if nextMinute.Before(now) { // the clock jumped: realign
				nextMinute = now.Truncate(time.Minute).Add(time.Minute)
				nextBoundary = now.Truncate(time.Hour).Add(time.Hour)
			}
		}
		if !now.Before(nextRollup) {
			hour := nextRollup.Truncate(time.Hour).Add(-time.Hour)
			if err := s.rollupHour(ctx, hour, live.hour(hour), s.sizes(src)); err != nil {
				s.opt.Logger.Warn("stats rollup failed", "hour", hour.UTC().Format(time.RFC3339), "err", err)
			}
			nextRollup = nextRollup.Add(time.Hour)
		}
		if !now.Before(nextPrune) {
			if err := s.pruneRollups(now); err != nil {
				s.opt.Logger.Warn("stats retention failed", "err", err)
			}
			nextPrune = now.Add(rollupPruneEvery)
		}
		timer.Reset(max(minTime(nextMinute, nextRollup, nextPrune).Sub(now), time.Millisecond))
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

// loadLive reads and deletes the parts of hours saved by the previous run; deleting them means a run that ends
// without saving (a crash) leaves those hours unknown instead of counting its time as silent.
func (s *Store) loadLive() (map[int64]carried, error) {
	rows, err := s.db.Query(`SELECT hour, saved_at, messages, frames, crc_failures, fixed_bit_errors, cpr_rejected,
		reconnects, silent_minutes, signal_frames, signal_power FROM live_partial`)
	if err != nil {
		return nil, err
	}
	out := map[int64]carried{}
	for rows.Next() {
		var (
			h, saved int64
			l        liveHour
		)
		if err := rows.Scan(&h, &saved, &l.messages, &l.frames, &l.crcFailures, &l.fixedBits, &l.cprRejected,
			&l.reconnects, &l.silentMinutes, &l.signalFrames, &l.signalPower); err != nil {
			rows.Close()
			return nil, err
		}
		out[h] = carried{l: l, end: time.UnixMilli(saved)}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_, err = s.db.Exec(`DELETE FROM live_partial`)
	return out, err
}

// saveLive replaces the saved parts of hours with parts, for the next run.
func (s *Store) saveLive(parts map[int64]carried) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM live_partial`); err != nil {
		return err
	}
	for h, c := range parts {
		l := c.l
		if _, err := tx.Exec(`INSERT INTO live_partial VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, h, c.end.UnixMilli(),
			l.messages, l.frames, l.crcFailures, l.fixedBits, l.cprRejected, l.reconnects, l.silentMinutes,
			l.signalFrames, l.signalPower); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(ts ...time.Time) time.Time {
	m := ts[0]
	for _, t := range ts[1:] {
		if t.Before(m) {
			m = t
		}
	}
	return m
}

// sizes samples the database (with -wal and -shm) and the tape.
func (s *Store) sizes(src RollupSources) sizes {
	var sz sizes
	var db int64
	found := false
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if fi, err := os.Stat(s.opt.File + suffix); err == nil {
			db += fi.Size()
			found = true
		}
	}
	if found {
		sz.db = &db
	}
	if src.TapeBytes != nil {
		if n, ok := src.TapeBytes(); ok {
			sz.tape = &n
		}
	}
	return sz
}

// rollupVersion is the version of the computed columns; older hours are recomputed by the backfill while their
// flights are still stored.
const rollupVersion = 3

// Altitude bands of hourly_altitude: BandGround, BandUnknown, else the band's lower bound in ft (5000 ft bands,
// the last from MaxBandFt up).
const (
	BandGround  = -1
	BandUnknown = -2
	BandFt      = 5000
	MaxBandFt   = 40000
)

// altitudeBand returns the band of a position.
func altitudeBand(alt sql.NullInt64, onGround bool) int {
	switch {
	case onGround:
		return BandGround
	case !alt.Valid:
		return BandUnknown
	}
	return int(min(max(alt.Int64, 0)/BandFt*BandFt, MaxBandFt))
}

// Notable event kinds.
const (
	KindEmergency = "emergency"
	KindFirstSeen = "firstSeen"
	KindGNSSLost  = "gnssLost"
)

// NotableEvent is an emergency squawk (its first sighting in the flight), the first sighting of an ICAO address, or
// the first loss of position (tracker.QualityLost) in a flight.
type NotableEvent struct {
	T        time.Time `json:"t"`
	Kind     string    `json:"kind"`
	ICAO     string    `json:"icao"`
	Country  string    `json:"country,omitempty"`
	Callsign string    `json:"callsign,omitempty"`
	Squawk   string    `json:"squawk,omitempty"`
	FlightID int64     `json:"flightId"`
}

func (e NotableEvent) key() string {
	return e.Kind + "/" + e.ICAO + "/" + strconv.FormatInt(e.FlightID, 10) + "/" + e.Squawk
}

// hourStats is one hour's rollup before it is stored.
type hourStats struct {
	start              time.Time
	adsb, modes        int
	started, positions int
	maxRange, bearing  *float64
	countries          map[string]int
	operators          map[string]int
	altitude           map[int]int
	events             []NotableEvent
	seen               map[string][2]int64 // icao → first and last sighting in the hour (unix ms)
	days               map[dayICAO]bool    // the local days of the hour's aircraft → ADS-B that day
	quality            qualityCounts
}

// qualityCounts splits the hour's ADS-B aircraft by their worst position quality: lost, then degraded, then no
// position received at all (no loss reported: out of range, too few frames), else ok.
type qualityCounts struct{ ok, degraded, lost, noPosition int }

// classify counts each ADS-B address once by the qualities its flights reported in the hour and whether any
// position of it was stored.
func classify(adsb map[string]bool, reported map[string]map[tracker.Quality]bool, positioned map[string]bool) qualityCounts {
	var c qualityCounts
	for icao, isADSB := range adsb {
		switch q := reported[icao]; {
		case !isADSB:
		case q[tracker.QualityLost]:
			c.lost++
		case q[tracker.QualityDegraded]:
			c.degraded++
		case !positioned[icao]:
			c.noPosition++
		default:
			c.ok++
		}
	}
	return c
}

type flightRef struct{ icao, callsign string }

// computeHour aggregates the flights and positions in [start, end) from db (the writer or a read-only handle).
func (s *Store) computeHour(ctx context.Context, db *sql.DB, start, end time.Time) (hourStats, error) {
	h := hourStats{start: start, countries: map[string]int{}, operators: map[string]int{}, altitude: map[int]int{},
		seen: map[string][2]int64{}, days: map[dayICAO]bool{}}
	lo, hi := start.UnixMilli(), end.UnixMilli()
	loc := s.location()

	rows, err := db.QueryContext(ctx, `SELECT f.id, f.icao, f.adsb, coalesce(f.callsign, ''), coalesce(f.squawk, ''),
		f.first_seen, f.last_seen, (SELECT min(g.first_seen) FROM flights g WHERE g.icao = f.icao),
		(SELECT a.first_seen FROM aircraft_seen a WHERE a.icao = f.icao)
		FROM flights f WHERE f.first_seen < ? AND f.last_seen >= ?`, hi, lo)
	if err != nil {
		return h, err
	}
	adsb := map[string]bool{}        // address → any ADS-B flight in the hour
	flights := map[int64]flightRef{} // flights overlapping the hour
	countries := map[string]map[string]bool{}
	operators := map[string]map[string]bool{}
	addTo := func(m map[string]map[string]bool, key, icao string) {
		if m[key] == nil {
			m[key] = map[string]bool{}
		}
		m[key][icao] = true
	}
	for rows.Next() {
		var id, first, last, earliest int64
		var icao, callsign, squawk string
		var isADSB bool
		var known sql.NullInt64
		if err := rows.Scan(&id, &icao, &isADSB, &callsign, &squawk, &first, &last, &earliest, &known); err != nil {
			rows.Close()
			return h, err
		}
		adsb[icao] = adsb[icao] || isADSB
		addDays(h.days, icao, isADSB, max(first, lo), min(last, hi-1), loc, "")
		flights[id] = flightRef{icao, callsign}
		if sn, ok := h.seen[icao]; ok {
			h.seen[icao] = [2]int64{min(sn[0], first), max(sn[1], last)}
		} else {
			h.seen[icao] = [2]int64{first, last}
		}
		if first >= lo {
			h.started++
			// new: the earliest stored flight of the address, and no earlier sighting remembered
			if first == earliest && (!known.Valid || known.Int64 >= first) {
				h.events = append(h.events, NotableEvent{T: time.UnixMilli(first).UTC(), Kind: KindFirstSeen, ICAO: icao,
					Callsign: callsign, FlightID: id})
			}
		}
		if isADSB && slices.Contains(EmergencySquawks, squawk) {
			h.events = append(h.events, NotableEvent{T: time.UnixMilli(first).UTC(), Kind: KindEmergency, ICAO: icao,
				Callsign: callsign, Squawk: squawk, FlightID: id})
		}
		if c := icaoCountry(icao); c != "" {
			addTo(countries, c, icao)
		}
		if p := OperatorPrefix(callsign); p != "" {
			addTo(operators, p, icao)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return h, err
	}
	for _, a := range adsb {
		if a {
			h.adsb++
		} else {
			h.modes++
		}
	}
	for k, set := range countries {
		h.countries[k] = len(set)
	}
	for k, set := range operators {
		h.operators[k] = len(set)
	}

	erows, err := db.QueryContext(ctx, `SELECT min(s.t), f.id, f.icao, coalesce(f.callsign, ''), s.squawk
		FROM samples s JOIN flights f ON f.id = s.flight_id
		WHERE s.squawk IN (?, ?, ?) AND s.t >= ? AND s.t < ? GROUP BY f.id, s.squawk`,
		EmergencySquawks[0], EmergencySquawks[1], EmergencySquawks[2], lo, hi)
	if err != nil {
		return h, err
	}
	for erows.Next() {
		var e NotableEvent
		var t int64
		if err := erows.Scan(&t, &e.FlightID, &e.ICAO, &e.Callsign, &e.Squawk); err != nil {
			erows.Close()
			return h, err
		}
		e.T, e.Kind = time.UnixMilli(t).UTC(), KindEmergency
		h.events = append(h.events, e)
	}
	erows.Close()
	if err := erows.Err(); err != nil {
		return h, err
	}
	for i := range h.events {
		h.events[i].Country = icaoCountry(h.events[i].ICAO)
	}

	reported, err := s.hourQuality(ctx, db, lo, hi, flights, &h)
	if err != nil {
		return h, err
	}
	positioned := map[string]bool{}
	prows, err := db.QueryContext(ctx, `SELECT flight_id, lat, lon, alt, on_ground FROM positions WHERE t >= ? AND t < ?`, lo, hi)
	if err != nil {
		return h, err
	}
	defer prows.Close()
	for prows.Next() {
		var id int64
		var p geo.Point
		var alt sql.NullInt64
		var ground bool
		if err := prows.Scan(&id, &p.Lat, &p.Lon, &alt, &ground); err != nil {
			return h, err
		}
		if f, ok := flights[id]; ok {
			positioned[f.icao] = true
		}
		h.positions++
		h.altitude[altitudeBand(alt, ground)]++
		if s.opt.Receiver != nil {
			if d := geo.DistanceNm(*s.opt.Receiver, p); h.maxRange == nil || d > *h.maxRange {
				h.maxRange, h.bearing = &d, new(geo.Bearing(*s.opt.Receiver, p))
			}
		}
	}
	if err := prows.Err(); err != nil {
		return h, err
	}
	h.quality = classify(adsb, reported, positioned)
	return h, nil
}

// hourQuality returns the position qualities each address reported in [lo, hi) (unix ms) from the flights
// overlapping it: the changes in the hour and the state each flight entered it with. It adds a KindGNSSLost event
// to h for each flight's first loss in the hour.
func (s *Store) hourQuality(ctx context.Context, db *sql.DB, lo, hi int64, flights map[int64]flightRef, h *hourStats) (map[string]map[tracker.Quality]bool, error) {
	out := map[string]map[tracker.Quality]bool{}
	add := func(id int64, q string) {
		f, ok := flights[id]
		if !ok {
			return
		}
		if out[f.icao] == nil {
			out[f.icao] = map[tracker.Quality]bool{}
		}
		out[f.icao][tracker.Quality(q)] = true
	}
	rows, err := db.QueryContext(ctx, `SELECT f.id, (SELECT quality FROM position_quality
		WHERE flight_id = f.id AND t < ? ORDER BY t DESC LIMIT 1)
		FROM flights f WHERE f.first_seen < ? AND f.last_seen >= ? AND f.adsb = 1`, lo, hi, lo)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var q sql.NullString
		if err := rows.Scan(&id, &q); err != nil {
			rows.Close()
			return nil, err
		}
		if q.Valid {
			add(id, q.String)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = db.QueryContext(ctx, `SELECT flight_id, t, quality FROM position_quality WHERE t >= ? AND t < ?
		ORDER BY t`, lo, hi)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lost := map[int64]bool{}
	for rows.Next() {
		var id, t int64
		var q string
		if err := rows.Scan(&id, &t, &q); err != nil {
			return nil, err
		}
		add(id, q)
		if f, ok := flights[id]; ok && tracker.Quality(q) == tracker.QualityLost && !lost[id] {
			lost[id] = true
			h.events = append(h.events, NotableEvent{T: time.UnixMilli(t).UTC(), Kind: KindGNSSLost, ICAO: f.icao,
				Country: icaoCountry(f.icao), Callsign: f.callsign, FlightID: id})
		}
	}
	return out, rows.Err()
}

// icaoCountry returns the country of a hex ICAO address, "" when unknown.
func icaoCountry(icao string) string {
	n, err := strconv.ParseUint(icao, 16, 32)
	if err != nil {
		return ""
	}
	return tracker.Country(uint32(n))
}

// rollupHour computes and stores the rollup of the hour starting at hour (UTC) in one transaction. The computed
// columns are replaced; live values and sizes are stored when given and otherwise keep what an earlier run stored.
func (s *Store) rollupHour(ctx context.Context, hour time.Time, live *liveHour, sz sizes) error {
	start := hour.UTC().Truncate(time.Hour)
	hs, err := s.computeHour(ctx, s.db, start, start.Add(time.Hour))
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	h := start.Unix()
	lv := make([]any, 8)
	if live != nil {
		lv = []any{live.messages, live.frames, live.crcFailures, live.fixedBits, live.cprRejected, live.reconnects,
			live.silentMinutes, live.dbfs()}
	}
	args := append([]any{h, hs.adsb, hs.modes, hs.started, hs.positions, hs.maxRange, hs.bearing, sz.db, sz.tape}, lv...)
	q := hs.quality
	args = append(args, rollupVersion, q.ok, q.degraded, q.lost, q.noPosition)
	if _, err := tx.Exec(`INSERT INTO hourly (hour, aircraft_adsb, aircraft_modes, flights_started, positions,
		max_range_nm, max_range_bearing, db_bytes, tape_bytes, messages, frames, crc_failures, fixed_bit_errors,
		cpr_rejected, reconnects, silent_minutes, signal_dbfs, rollup_version, adsb_position_ok, adsb_degraded,
		adsb_lost, adsb_no_position) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (hour) DO UPDATE SET aircraft_adsb = excluded.aircraft_adsb,
		adsb_position_ok = excluded.adsb_position_ok, adsb_degraded = excluded.adsb_degraded,
		adsb_lost = excluded.adsb_lost, adsb_no_position = excluded.adsb_no_position,
		aircraft_modes = excluded.aircraft_modes, flights_started = excluded.flights_started,
		positions = excluded.positions, max_range_nm = excluded.max_range_nm,
		max_range_bearing = excluded.max_range_bearing, rollup_version = excluded.rollup_version,
		db_bytes = coalesce(excluded.db_bytes, db_bytes), tape_bytes = coalesce(excluded.tape_bytes, tape_bytes),
		messages = coalesce(excluded.messages, messages), frames = coalesce(excluded.frames, frames),
		crc_failures = coalesce(excluded.crc_failures, crc_failures),
		fixed_bit_errors = coalesce(excluded.fixed_bit_errors, fixed_bit_errors),
		cpr_rejected = coalesce(excluded.cpr_rejected, cpr_rejected),
		reconnects = coalesce(excluded.reconnects, reconnects),
		silent_minutes = coalesce(excluded.silent_minutes, silent_minutes),
		signal_dbfs = CASE WHEN excluded.frames IS NULL THEN signal_dbfs ELSE excluded.signal_dbfs END`, args...); err != nil {
		return err
	}
	for _, t := range []struct {
		table, column string
		counts        map[string]int
	}{{"hourly_country", "country", hs.countries}, {"hourly_operator", "prefix", hs.operators}} {
		if _, err := tx.Exec(`DELETE FROM `+t.table+` WHERE hour = ?`, h); err != nil {
			return err
		}
		for key, n := range t.counts {
			if _, err := tx.Exec(`INSERT INTO `+t.table+` (hour, `+t.column+`, aircraft) VALUES (?, ?, ?)`, h, key, n); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`DELETE FROM hourly_altitude WHERE hour = ?`, h); err != nil {
		return err
	}
	for band, n := range hs.altitude {
		if _, err := tx.Exec(`INSERT INTO hourly_altitude (hour, band, positions) VALUES (?, ?, ?)`, h, band, n); err != nil {
			return err
		}
	}
	for _, e := range hs.events {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO notable_events (kind, icao, flight_id, squawk, t, callsign)
			VALUES (?, ?, ?, ?, ?, nullif(?, ''))`, e.Kind, e.ICAO, e.FlightID, e.Squawk, e.T.UnixMilli(), e.Callsign); err != nil {
			return err
		}
	}
	if err := writeDays(tx, hs.days); err != nil {
		return err
	}
	for icao, sn := range hs.seen {
		if _, err := tx.Exec(`INSERT INTO aircraft_seen (icao, first_seen, last_seen) VALUES (?, ?, ?)
			ON CONFLICT (icao) DO UPDATE SET first_seen = min(first_seen, excluded.first_seen),
			last_seen = max(last_seen, excluded.last_seen)`, icao, sn[0], sn[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// backfill computes every hour missing from hourly (or computed by an older version) between the oldest stored flight (but not before the rollup
// retention, nor the flight retention) and the hour before now, keeping the live values and sizes stored.
func (s *Store) backfill(ctx context.Context, now time.Time) {
	var oldest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT min(first_seen) FROM flights`).Scan(&oldest); err != nil || !oldest.Valid {
		return
	}
	from := time.UnixMilli(oldest.Int64).UTC().Truncate(time.Hour)
	if ret := s.opt.StatsRetention; ret > 0 {
		from = maxTime(from, now.Add(-ret).UTC().Truncate(time.Hour))
	}
	if ret := s.opt.Retention; ret > 0 { // an hour reaching past the flight retention may have lost flights
		from = maxTime(from, now.Add(-ret).UTC().Truncate(time.Hour).Add(time.Hour))
	}
	last := now.UTC().Truncate(time.Hour).Add(-time.Hour)
	if from.After(last) {
		return
	}
	have := map[int64]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT hour FROM hourly WHERE hour >= ? AND hour <= ? AND rollup_version >= ?`,
		from.Unix(), last.Unix(), rollupVersion)
	if err != nil {
		s.opt.Logger.Warn("stats backfill failed", "err", err)
		return
	}
	for rows.Next() {
		var h int64
		if rows.Scan(&h) == nil {
			have[h] = true
		}
	}
	rows.Close()
	var missing []time.Time
	for h := from; !h.After(last); h = h.Add(time.Hour) {
		if !have[h.Unix()] {
			missing = append(missing, h)
		}
	}
	if len(missing) == 0 {
		return
	}
	s.opt.Logger.Info("stats backfill started", "hours", len(missing), "from", missing[0].Format(time.RFC3339))
	for i, h := range missing {
		if ctx.Err() != nil {
			return
		}
		if err := s.rollupHour(ctx, h, nil, sizes{}); err != nil {
			s.opt.Logger.Warn("stats backfill failed", "hour", h.Format(time.RFC3339), "err", err)
			return
		}
		if (i+1)%backfillLogEvery == 0 {
			s.opt.Logger.Info("stats backfill progress", "done", i+1, "of", len(missing))
		}
	}
	s.opt.Logger.Info("stats backfill finished", "hours", len(missing))
}

// pruneRollups deletes rollups of hours older than StatsRetention before now.
func (s *Store) pruneRollups(now time.Time) error {
	if s.opt.StatsRetention <= 0 {
		return nil
	}
	cut := now.Add(-s.opt.StatsRetention)
	for _, q := range []struct {
		table, column string
		v             int64
	}{
		{"hourly", "hour", cut.Unix()}, {"hourly_country", "hour", cut.Unix()}, {"hourly_operator", "hour", cut.Unix()},
		{"hourly_altitude", "hour", cut.Unix()}, {"notable_events", "t", cut.UnixMilli()},
	} {
		if _, err := s.db.Exec(`DELETE FROM `+q.table+` WHERE `+q.column+` < ?`, q.v); err != nil {
			return fmt.Errorf("%s: %w", q.table, err)
		}
	}
	if _, err := s.db.Exec(`DELETE FROM daily_aircraft WHERE day < ?`, dayOf(cut, s.location())); err != nil {
		return fmt.Errorf("daily_aircraft: %w", err)
	}
	return nil
}
