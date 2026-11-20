package history

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sync"
	"time"
)

// currentTTL is how long the computed partial current hour is reused.
const currentTTL = 30 * time.Second

// HourRow is one hour of the rollups; Partial marks the current hour, computed on the fly up to now.
type HourRow struct {
	Hour            time.Time `json:"hour"`
	AircraftADSB    int       `json:"aircraftAdsb"`
	AircraftModeS   int       `json:"aircraftModes"`
	FlightsStarted  int       `json:"flightsStarted"`
	Positions       int       `json:"positions"`
	Messages        *int64    `json:"messages,omitempty"`
	MaxRangeNm      *float64  `json:"maxRangeNm,omitempty"`
	MaxRangeBearing *float64  `json:"maxRangeBearing,omitempty"`
	DBBytes         *int64    `json:"dbBytes,omitempty"`
	TapeBytes       *int64    `json:"tapeBytes,omitempty"`
	Frames          *int64    `json:"frames,omitempty"`
	CRCFailures     *int64    `json:"crcFailures,omitempty"`
	FixedBitErrors  *int64    `json:"fixedBitErrors,omitempty"`
	CPRRejected     *int64    `json:"cprRejected,omitempty"`
	Reconnects      *int64    `json:"reconnects,omitempty"`
	SilentMinutes   *int64    `json:"silentMinutes,omitempty"`
	SignalDBFS      *float64  `json:"signalDbfs,omitempty"`
	Partial         bool      `json:"partial,omitempty"`
}

// setLive fills the live columns from l (nil leaves them unknown).
func (r *HourRow) setLive(l *liveHour) {
	if l == nil {
		return
	}
	r.Messages, r.Frames, r.CRCFailures = &l.messages, &l.frames, &l.crcFailures
	r.FixedBitErrors, r.CPRRejected, r.Reconnects = &l.fixedBits, &l.cprRejected, &l.reconnects
	r.SilentMinutes, r.SignalDBFS = &l.silentMinutes, l.signalDbfs
}

// AltitudeBand is the stored positions in one altitude band: "ground", "unknown", or MinFt up to MinFt + 5000 ft
// (the last band open-ended).
type AltitudeBand struct {
	Band      string `json:"band"`
	MinFt     *int   `json:"minFt,omitempty"`
	Positions int    `json:"positions"`
}

// Notable is GET /api/stats/notable: event counts in the range and the events, emergencies first, newest first.
type Notable struct {
	Emergencies int            `json:"emergencies"`
	FirstSeen   int            `json:"firstSeen"`
	Events      []NotableEvent `json:"events"`
}

// CountryCount is a country's aircraft-hours: each aircraft counts once per hour it was seen.
type CountryCount struct {
	Country       string `json:"country"`
	AircraftHours int    `json:"aircraftHours"`
}

// OperatorCount is a callsign prefix's aircraft-hours.
type OperatorCount struct {
	Prefix        string `json:"prefix"`
	AircraftHours int    `json:"aircraftHours"`
}

// RangeDay is the furthest stored position of one local day, and the hour it was in.
type RangeDay struct {
	Day        string    `json:"day"` // YYYY-MM-DD in the requested timezone
	MaxRangeNm float64   `json:"maxRangeNm"`
	Bearing    *float64  `json:"bearing,omitempty"`
	Hour       time.Time `json:"hour"`
}

// SizeSample is the database and tape size stored with an hour.
type SizeSample struct {
	Hour     time.Time
	DB, Tape *int64
}

type currentCache struct {
	mu  sync.Mutex
	at  time.Time // wall time of the computation
	row HourRow
	hs  hourStats
}

// current returns the partial rollup of the hour containing now, computed at most every currentTTL.
func (s *Store) current(ctx context.Context, now time.Time) (HourRow, hourStats, error) {
	start := now.UTC().Truncate(time.Hour)
	c := &s.cur
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.row.Hour.Equal(start) && now.Sub(c.at) < currentTTL && !now.Before(c.at) {
		return c.row, c.hs, nil
	}
	hs, err := s.computeHour(ctx, s.ro, start, now)
	if err != nil {
		return HourRow{}, hs, err
	}
	row := HourRow{Hour: start, AircraftADSB: hs.adsb, AircraftModeS: hs.modes, FlightsStarted: hs.started,
		Positions: hs.positions, MaxRangeNm: hs.maxRange, MaxRangeBearing: hs.bearing, Partial: true}
	if src := s.src.Load(); src != nil {
		if src.Counters != nil {
			row.setLive(s.live.since(start, src.Counters()))
		}
		sz := s.sizes(*src)
		row.DBBytes, row.TapeBytes = sz.db, sz.tape
	}
	c.at, c.row, c.hs = now, row, hs
	return row, hs, nil
}

// includesCurrent reports whether [from, to) contains the hour of now.
func includesCurrent(from, to, now time.Time) bool {
	h := now.UTC().Truncate(time.Hour)
	return !h.Before(from.UTC().Truncate(time.Hour)) && h.Before(to)
}

// Hourly returns the stored hours in [from, to) and, when the range reaches it, the current hour up to now.
func (s *Store) Hourly(ctx context.Context, from, to, now time.Time) ([]HourRow, error) {
	rows, err := s.ro.QueryContext(ctx, `SELECT hour, aircraft_adsb, aircraft_modes, flights_started, positions,
		messages, max_range_nm, max_range_bearing, db_bytes, tape_bytes, frames, crc_failures, fixed_bit_errors,
		cpr_rejected, reconnects, silent_minutes, signal_dbfs FROM hourly WHERE hour >= ? AND hour < ?
		ORDER BY hour`, from.UTC().Truncate(time.Hour).Unix(), to.Unix())
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	out := []HourRow{}
	cur := now.UTC().Truncate(time.Hour)
	for rows.Next() {
		var r HourRow
		var h int64
		var msgs, db, tape, frames, crc, fixed, cpr, rec, silent sql.NullInt64
		var rng, brg, sig sql.NullFloat64
		if err := rows.Scan(&h, &r.AircraftADSB, &r.AircraftModeS, &r.FlightsStarted, &r.Positions, &msgs, &rng,
			&brg, &db, &tape, &frames, &crc, &fixed, &cpr, &rec, &silent, &sig); err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		r.Hour = time.Unix(h, 0).UTC()
		if !r.Hour.Before(cur) {
			continue // a rerun of the current hour is replaced by the live one below
		}
		r.Messages, r.DBBytes, r.TapeBytes = nullInt(msgs), nullInt(db), nullInt(tape)
		r.MaxRangeNm, r.MaxRangeBearing = nullFloat(rng), nullFloat(brg)
		r.Frames, r.CRCFailures, r.FixedBitErrors = nullInt(frames), nullInt(crc), nullInt(fixed)
		r.CPRRejected, r.Reconnects, r.SilentMinutes, r.SignalDBFS = nullInt(cpr), nullInt(rec), nullInt(silent), nullFloat(sig)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	if includesCurrent(from, to, now) {
		row, _, err := s.current(ctx, now)
		if err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		out = append(out, row)
	}
	return out, nil
}

func nullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func nullFloat(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

// keyCounts sums table's aircraft per key over the stored hours in [from, to), plus the current hour's extra.
func (s *Store) keyCounts(ctx context.Context, table, column string, from, to, now time.Time, extra func(hourStats) map[string]int) (map[string]int, error) {
	rows, err := s.ro.QueryContext(ctx, `SELECT `+column+`, sum(aircraft) FROM `+table+`
		WHERE hour >= ? AND hour < ? GROUP BY `+column, from.UTC().Truncate(time.Hour).Unix(),
		min(to.Unix(), now.UTC().Truncate(time.Hour).Unix()))
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		out[k] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	if includesCurrent(from, to, now) {
		_, hs, err := s.current(ctx, now)
		if err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		for k, n := range extra(hs) {
			out[k] += n
		}
	}
	return out, nil
}

// top returns the limit largest counts, ties by key.
func top[T any](counts map[string]int, limit int, mk func(string, int) T) []T {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int { return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b)) })
	out := make([]T, 0, min(limit, len(keys)))
	for _, k := range keys[:min(limit, len(keys))] {
		out = append(out, mk(k, counts[k]))
	}
	return out
}

// Countries returns the limit countries with the most aircraft-hours in [from, to), current hour included.
func (s *Store) Countries(ctx context.Context, from, to, now time.Time, limit int) ([]CountryCount, error) {
	m, err := s.keyCounts(ctx, "hourly_country", "country", from, to, now, func(h hourStats) map[string]int { return h.countries })
	if err != nil {
		return nil, err
	}
	return top(m, limit, func(k string, n int) CountryCount { return CountryCount{k, n} }), nil
}

// Operators returns the limit callsign prefixes with the most aircraft-hours in [from, to), current hour included.
func (s *Store) Operators(ctx context.Context, from, to, now time.Time, limit int) ([]OperatorCount, error) {
	m, err := s.keyCounts(ctx, "hourly_operator", "prefix", from, to, now, func(h hourStats) map[string]int { return h.operators })
	if err != nil {
		return nil, err
	}
	return top(m, limit, func(k string, n int) OperatorCount { return OperatorCount{k, n} }), nil
}

// RangeDaily returns, per day in loc, the furthest stored position of the hours in [from, to), in day order.
func (s *Store) RangeDaily(ctx context.Context, from, to, now time.Time, loc *time.Location) ([]RangeDay, error) {
	hours, err := s.Hourly(ctx, from, to, now)
	if err != nil {
		return nil, err
	}
	byDay := map[string]RangeDay{}
	for _, h := range hours {
		if h.MaxRangeNm == nil {
			continue
		}
		day := h.Hour.In(loc).Format(time.DateOnly)
		if best, ok := byDay[day]; !ok || *h.MaxRangeNm > best.MaxRangeNm {
			byDay[day] = RangeDay{Day: day, MaxRangeNm: *h.MaxRangeNm, Bearing: h.MaxRangeBearing, Hour: h.Hour}
		}
	}
	out := make([]RangeDay, 0, len(byDay))
	for _, d := range byDay {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b RangeDay) int { return cmp.Compare(a.Day, b.Day) })
	return out, nil
}

// SizeSamples returns the size samples of the hours at or after since, in time order.
func (s *Store) SizeSamples(ctx context.Context, since time.Time) ([]SizeSample, error) {
	rows, err := s.ro.QueryContext(ctx, `SELECT hour, db_bytes, tape_bytes FROM hourly
		WHERE hour >= ? AND (db_bytes IS NOT NULL OR tape_bytes IS NOT NULL) ORDER BY hour`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	var out []SizeSample
	for rows.Next() {
		var h int64
		var db, tape sql.NullInt64
		if err := rows.Scan(&h, &db, &tape); err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		out = append(out, SizeSample{Hour: time.Unix(h, 0).UTC(), DB: nullInt(db), Tape: nullInt(tape)})
	}
	return out, rows.Err()
}

// Oldest returns the first sighting of the oldest stored flight and the oldest rollup hour; zero when none.
func (s *Store) Oldest(ctx context.Context) (flights, rollups time.Time, err error) {
	var f, r sql.NullInt64
	if err := s.ro.QueryRowContext(ctx, `SELECT (SELECT min(first_seen) FROM flights), (SELECT min(hour) FROM hourly)`).Scan(&f, &r); err != nil {
		return flights, rollups, fmt.Errorf("history: %w", err)
	}
	if f.Valid {
		flights = time.UnixMilli(f.Int64).UTC()
	}
	if r.Valid {
		rollups = time.Unix(r.Int64, 0).UTC()
	}
	return flights, rollups, nil
}

// DBBytes returns the size of the database with its -wal and -shm files.
func (s *Store) DBBytes() int64 {
	if sz := s.sizes(RollupSources{}); sz.db != nil {
		return *sz.db
	}
	return 0
}

// File returns the database path.
func (s *Store) File() string { return s.opt.File }

// StatsRetention returns how long rollups are kept; 0 keeps them forever.
func (s *Store) StatsRetention() time.Duration { return s.opt.StatsRetention }

// Retention returns how long closed flights are kept; 0 keeps them forever.
func (s *Store) Retention() time.Duration { return s.opt.Retention }

// bandLabel names an altitude band of hourly_altitude.
func bandLabel(band int) AltitudeBand {
	switch {
	case band == BandGround:
		return AltitudeBand{Band: "ground"}
	case band == BandUnknown:
		return AltitudeBand{Band: "unknown"}
	case band >= MaxBandFt:
		return AltitudeBand{Band: fmt.Sprintf("≥%dk", band/1000), MinFt: new(band)}
	}
	return AltitudeBand{Band: fmt.Sprintf("%d–%dk", band/1000, (band+BandFt)/1000), MinFt: new(band)}
}

// AltitudeBands returns the stored positions per altitude band over [from, to), current hour included: every band
// from the ground up, then unknown, zeros included.
func (s *Store) AltitudeBands(ctx context.Context, from, to, now time.Time) ([]AltitudeBand, error) {
	rows, err := s.ro.QueryContext(ctx, `SELECT band, sum(positions) FROM hourly_altitude WHERE hour >= ? AND hour < ?
		GROUP BY band`, from.UTC().Truncate(time.Hour).Unix(), min(to.Unix(), now.UTC().Truncate(time.Hour).Unix()))
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	counts := map[int]int{}
	for rows.Next() {
		var band, n int
		if err := rows.Scan(&band, &n); err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		counts[band] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	if includesCurrent(from, to, now) {
		_, hs, err := s.current(ctx, now)
		if err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		for b, n := range hs.altitude {
			counts[b] += n
		}
	}
	bands := []int{BandGround}
	for ft := 0; ft <= MaxBandFt; ft += BandFt {
		bands = append(bands, ft)
	}
	out := make([]AltitudeBand, 0, len(bands)+1)
	for _, b := range append(bands, BandUnknown) {
		ab := bandLabel(b)
		ab.Positions = counts[b]
		out = append(out, ab)
	}
	return out, nil
}

// rank orders emergencies before first sightings.
func rank(e NotableEvent) int {
	if e.Kind == KindEmergency {
		return 0
	}
	return 1
}

// NotableEvents returns the counts of notable events in [from, to) and at most limit of them, emergencies first,
// then newest first; the current hour's events are included.
func (s *Store) NotableEvents(ctx context.Context, from, to, now time.Time, limit int) (Notable, error) {
	out := Notable{Events: []NotableEvent{}}
	rows, err := s.ro.QueryContext(ctx, `SELECT kind, icao, flight_id, squawk, t, coalesce(callsign, '')
		FROM notable_events WHERE t >= ? AND t < ?`, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return out, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	var all []NotableEvent
	for rows.Next() {
		var e NotableEvent
		var t int64
		if err := rows.Scan(&e.Kind, &e.ICAO, &e.FlightID, &e.Squawk, &t, &e.Callsign); err != nil {
			return out, fmt.Errorf("history: %w", err)
		}
		e.T, e.Country = time.UnixMilli(t).UTC(), icaoCountry(e.ICAO)
		seen[e.key()] = true
		all = append(all, e)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("history: %w", err)
	}
	if includesCurrent(from, to, now) {
		_, hs, err := s.current(ctx, now)
		if err != nil {
			return out, fmt.Errorf("history: %w", err)
		}
		for _, e := range hs.events {
			if !seen[e.key()] && !e.T.Before(from) && e.T.Before(to) {
				all = append(all, e)
			}
		}
	}
	for _, e := range all {
		if e.Kind == KindEmergency {
			out.Emergencies++
		} else {
			out.FirstSeen++
		}
	}
	slices.SortFunc(all, func(a, b NotableEvent) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), b.T.Compare(a.T), cmp.Compare(a.ICAO, b.ICAO))
	})
	out.Events = append(out.Events, all[:min(limit, len(all))]...)
	return out, nil
}
