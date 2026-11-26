package history

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

const (
	backfillKey   = "backfill_aircraft" // meta: -1 counts pending, else the last flight id given a max distance
	backfillChunk = 200
	MaxTrackIDs   = 20
	maxCallsigns  = 5
)

// ErrNoAircraft is returned for an address with no stored sighting.
var ErrNoAircraft = errors.New("history: aircraft not found")

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// countClosed adds the flights matching cond to aircraft_seen: one more flight each, the seen times widened, ADS-B
// kept once seen and the newest stored callsign.
func countClosed(db execer, cond string, args ...any) error {
	_, err := db.Exec(`INSERT INTO aircraft_seen (icao, first_seen, last_seen, flights, adsb, last_callsign)
		SELECT icao, min(first_seen), max(last_seen), count(*), max(adsb),
			(SELECT g.callsign FROM flights g WHERE g.icao = f.icao AND g.callsign IS NOT NULL ORDER BY g.last_seen DESC LIMIT 1)
		FROM flights f WHERE `+cond+` GROUP BY icao
		ON CONFLICT (icao) DO UPDATE SET first_seen = min(first_seen, excluded.first_seen),
			last_seen = max(last_seen, excluded.last_seen), flights = flights + excluded.flights,
			adsb = max(adsb, excluded.adsb), last_callsign = coalesce(excluded.last_callsign, last_callsign)`, args...)
	return err
}

type querier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// backfillState reads the backfill cursor through q (the writer connection or a transaction on it).
func backfillState(q querier) (int64, bool) {
	var v string
	if err := q.QueryRow(`SELECT value FROM meta WHERE key = ?`, backfillKey).Scan(&v); err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil
}

// closeStale closes the open flights last seen before cutoff (unix ms), counting them unless the backfill will.
func (s *Store) closeStale(cutoff int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if c, ok := backfillState(tx); !ok || c >= 0 {
		if err := countClosed(tx, `closed = 0 AND last_seen < ?`, cutoff); err != nil {
			return err
		}
	}
	res, err := tx.Exec(`UPDATE flights SET closed = 1 WHERE closed = 0 AND last_seen < ?`, cutoff)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.opt.Logger.Info("history: closed flights left open by the previous run", "flights", n)
	}
	return tx.Commit()
}

// backfillCounts fills aircraft_seen's totals from the stored closed flights once after the migration. Run calls it
// before its first write, so no close is counted twice.
func (s *Store) backfillCounts() {
	if c, ok := backfillState(s.db); !ok || c >= 0 {
		return
	}
	err := func() error {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`UPDATE aircraft_seen SET flights = 0, adsb = 0`); err != nil {
			return err
		}
		if err := countClosed(tx, `closed = 1`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE meta SET value = '0' WHERE key = ?`, backfillKey); err != nil {
			return err
		}
		return tx.Commit()
	}()
	if err != nil {
		s.writeErrors.Add(1)
		s.opt.Logger.Warn("history: aircraft backfill failed", "err", err)
	}
}

// backfillStep gives up to backfillChunk stored flights their max distance from the receiver and returns a channel
// that fires for the next step, or nil when the backfill is done.
func (s *Store) backfillStep() <-chan time.Time {
	cursor, ok := backfillState(s.db)
	if !ok || cursor < 0 {
		return nil
	}
	n, last, err := s.fillMaxDistance(cursor)
	if err == nil && n > 0 {
		_, err = s.db.Exec(`UPDATE meta SET value = ? WHERE key = ?`, strconv.FormatInt(last, 10), backfillKey)
	}
	if err == nil && n == 0 {
		_, err = s.db.Exec(`DELETE FROM meta WHERE key = ?`, backfillKey)
	}
	if err != nil {
		s.writeErrors.Add(1)
		s.opt.Logger.Warn("history: max distance backfill failed", "err", err)
		_, _ = s.db.Exec(`DELETE FROM meta WHERE key = ?`, backfillKey)
		return nil
	}
	if n == 0 {
		return nil
	}
	return time.After(10 * time.Millisecond)
}

func (s *Store) fillMaxDistance(after int64) (n int, last int64, err error) {
	if s.opt.Receiver == nil {
		return 0, after, nil
	}
	rows, err := s.db.Query(`SELECT id FROM flights WHERE id > ? AND max_distance_nm IS NULL AND points > 0 ORDER BY id LIMIT ?`,
		after, backfillChunk)
	if err != nil {
		return 0, after, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, after, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return 0, after, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, after, err
	}
	defer tx.Rollback()
	for _, id := range ids {
		var d *float64
		prows, err := tx.Query(`SELECT lat, lon FROM positions WHERE flight_id = ?`, id)
		if err != nil {
			return 0, after, err
		}
		for prows.Next() {
			var p geo.Point
			if err := prows.Scan(&p.Lat, &p.Lon); err != nil {
				prows.Close()
				return 0, after, err
			}
			if v := geo.DistanceNm(*s.opt.Receiver, p); d == nil || v > *d {
				d = &v
			}
		}
		prows.Close()
		if d != nil {
			if _, err := tx.Exec(`UPDATE flights SET max_distance_nm = ? WHERE id = ? AND max_distance_nm IS NULL`, *d, id); err != nil {
				return 0, after, err
			}
		}
	}
	return len(ids), ids[len(ids)-1], tx.Commit()
}

// AircraftMatch is one address found by Search. Flights counts closed flights ever plus the stored open ones.
type AircraftMatch struct {
	ICAO         string    `json:"icao"`
	LastCallsign string    `json:"lastCallsign,omitempty"`
	Callsigns    []string  `json:"callsigns"`
	Country      string    `json:"country,omitempty"`
	LastSeen     time.Time `json:"lastSeen"`
	Flights      int       `json:"flights"`
	ADSB         bool      `json:"adsb"`
	Live         bool      `json:"live"`
}

var hexPrefix = regexp.MustCompile(`^[0-9A-F]{1,6}$`)

// NormalizeSearch upper-cases and trims a search text.
func NormalizeSearch(q string) string { return strings.ToUpper(strings.TrimSpace(q)) }

// MatchesSearch reports whether an address or callsign matches the normalized text q as Search matches it.
func MatchesSearch(q, icao, callsign string) bool {
	if q == "" {
		return false
	}
	if hexPrefix.MatchString(q) && strings.HasPrefix(icao, q) {
		return true
	}
	if callsign == "" {
		return false
	}
	return strings.HasPrefix(callsign, q) || len(q) >= 3 && strings.Contains(callsign, q)
}

func escapeLike(v string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(v)
}

// Search returns up to limit aircraft whose address starts with q (1–6 hex digits) or whose callsign starts with q
// or, for 3 or more characters, contains it; live ones (by live) first, then by last seen, newest first.
func (s *Store) Search(ctx context.Context, q string, limit int, live func(icao string) bool) ([]AircraftMatch, error) {
	q = NormalizeSearch(q)
	out := []AircraftMatch{}
	if q == "" {
		return out, nil
	}
	hex := any(nil)
	if hexPrefix.MatchString(q) {
		hex = q
	}
	sub := any(nil)
	if len(q) >= 3 {
		sub = "%" + escapeLike(q) + "%"
	}
	rows, err := s.ro.QueryContext(ctx, `WITH c AS (
			SELECT icao FROM aircraft_seen WHERE icao >= ?1 AND icao < ?1 || '~'
			UNION SELECT icao FROM flights WHERE icao >= ?1 AND icao < ?1 || '~'
			UNION SELECT icao FROM flights WHERE callsign >= ?2 AND callsign < ?2 || '~'
			UNION SELECT icao FROM aircraft_seen WHERE last_callsign >= ?2 AND last_callsign < ?2 || '~'
			UNION SELECT icao FROM flights WHERE callsign LIKE ?3 ESCAPE '\'
			UNION SELECT icao FROM aircraft_seen WHERE last_callsign LIKE ?3 ESCAPE '\')
		SELECT c.icao, max(coalesce((SELECT last_seen FROM aircraft_seen a WHERE a.icao = c.icao), 0),
			coalesce((SELECT max(last_seen) FROM flights f WHERE f.icao = c.icao), 0)) FROM c`, hex, q, sub)
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	type cand struct {
		icao string
		last int64
		live bool
	}
	var cs []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.icao, &c.last); err != nil {
			rows.Close()
			return nil, fmt.Errorf("history: %w", err)
		}
		c.live = live != nil && live(c.icao)
		cs = append(cs, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	slices.SortFunc(cs, func(a, b cand) int {
		if a.live != b.live {
			if a.live {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(b.last, a.last), cmp.Compare(a.icao, b.icao))
	})
	for _, c := range cs[:min(len(cs), limit)] {
		m, err := s.match(ctx, c.icao)
		if err != nil {
			return nil, err
		}
		m.Live = c.live
		out = append(out, m)
	}
	return out, nil
}

// match builds an address's search result from aircraft_seen and its stored flights.
func (s *Store) match(ctx context.Context, icao string) (AircraftMatch, error) {
	m := AircraftMatch{ICAO: icao, Callsigns: []string{}, Country: country(icao)}
	var (
		seenLast, flightsLast sql.NullInt64
		seenFlights, open     int
		seenADSB, flightsADSB sql.NullBool
		seenCallsign          sql.NullString
	)
	err := s.ro.QueryRowContext(ctx, `SELECT last_seen, flights, adsb, last_callsign FROM aircraft_seen WHERE icao = ?`, icao).
		Scan(&seenLast, &seenFlights, &seenADSB, &seenCallsign)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return m, fmt.Errorf("history: %w", err)
	}
	if err := s.ro.QueryRowContext(ctx, `SELECT max(last_seen), coalesce(sum(closed = 0), 0), max(adsb) FROM flights WHERE icao = ?`, icao).
		Scan(&flightsLast, &open, &flightsADSB); err != nil {
		return m, fmt.Errorf("history: %w", err)
	}
	rows, err := s.ro.QueryContext(ctx, `SELECT callsign FROM flights WHERE icao = ? AND callsign IS NOT NULL
		GROUP BY callsign ORDER BY max(last_seen) DESC LIMIT ?`, icao, maxCallsigns)
	if err != nil {
		return m, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cs string
		if err := rows.Scan(&cs); err != nil {
			return m, fmt.Errorf("history: %w", err)
		}
		m.Callsigns = append(m.Callsigns, cs)
	}
	if err := rows.Err(); err != nil {
		return m, fmt.Errorf("history: %w", err)
	}
	if len(m.Callsigns) > 0 {
		m.LastCallsign = m.Callsigns[0]
	} else if seenCallsign.Valid {
		m.LastCallsign = seenCallsign.String
		m.Callsigns = append(m.Callsigns, seenCallsign.String)
	}
	m.LastSeen = time.UnixMilli(max(seenLast.Int64, flightsLast.Int64)).UTC()
	m.Flights = seenFlights + open
	m.ADSB = seenADSB.Bool || flightsADSB.Bool
	return m, nil
}

func country(icao string) string {
	if v, err := strconv.ParseUint(icao, 16, 24); err == nil {
		return tracker.Country(uint32(v))
	}
	return ""
}

// CallsignCount is a callsign with its number of stored flights.
type CallsignCount struct {
	Callsign string `json:"callsign"`
	Flights  int    `json:"flights"`
}

// QualitySummary counts stored ADS-B flights by their worst position quality.
type QualitySummary struct {
	OK       int `json:"ok"`
	Degraded int `json:"degraded"`
	Lost     int `json:"lost"`
}

// AircraftSummary is one address's history: FirstSeen, LastSeen, Flights and ADSB over all time (aircraft_seen plus
// open flights), the rest from the flights still stored. RetainedFrom is the oldest stored flight's start.
type AircraftSummary struct {
	ICAO            string          `json:"icao"`
	Country         string          `json:"country,omitempty"`
	FirstSeen       time.Time       `json:"firstSeen"`
	LastSeen        time.Time       `json:"lastSeen"`
	Flights         int             `json:"flights"`
	ADSB            bool            `json:"adsb"`
	Live            bool            `json:"live"` // set by the API
	RetainedFlights int             `json:"retainedFlights"`
	Callsigns       []CallsignCount `json:"callsigns"`
	MaxAlt          *int            `json:"maxAlt,omitempty"`
	MinDistanceNm   *float64        `json:"minDistanceNm,omitempty"`
	MaxDistanceNm   *float64        `json:"maxDistanceNm,omitempty"`
	DaysSeen        int             `json:"daysSeen"`
	Quality         *QualitySummary `json:"quality,omitempty"`
	RetainedFrom    *time.Time      `json:"retainedFrom,omitempty"`
}

// Aircraft returns the summary of icao (upper-case hex), with days counted in loc, or ErrNoAircraft.
func (s *Store) Aircraft(ctx context.Context, icao string, loc *time.Location) (AircraftSummary, error) {
	icao = strings.ToUpper(icao)
	a := AircraftSummary{ICAO: icao, Country: country(icao), Callsigns: []CallsignCount{}}
	var (
		seenFirst, seenLast sql.NullInt64
		seenFlights         int
		seenADSB            bool
	)
	err := s.ro.QueryRowContext(ctx, `SELECT first_seen, last_seen, flights, adsb FROM aircraft_seen WHERE icao = ?`, icao).
		Scan(&seenFirst, &seenLast, &seenFlights, &seenADSB)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return a, fmt.Errorf("history: %w", err)
	}
	var (
		first, last, open, maxAlt sql.NullInt64
		adsb                      sql.NullBool
		minD, maxD                sql.NullFloat64
	)
	if err := s.ro.QueryRowContext(ctx, `SELECT count(*), min(first_seen), max(last_seen), sum(closed = 0), max(adsb),
			max(max_alt), min(min_distance_nm), max(max_distance_nm) FROM flights WHERE icao = ?`, icao).
		Scan(&a.RetainedFlights, &first, &last, &open, &adsb, &maxAlt, &minD, &maxD); err != nil {
		return a, fmt.Errorf("history: %w", err)
	}
	if !seenFirst.Valid && a.RetainedFlights == 0 {
		return a, ErrNoAircraft
	}
	a.FirstSeen = time.UnixMilli(minValid(seenFirst, first)).UTC()
	a.LastSeen = time.UnixMilli(max(seenLast.Int64, last.Int64)).UTC()
	a.Flights = seenFlights + int(open.Int64)
	a.ADSB = seenADSB || adsb.Bool
	if maxAlt.Valid {
		a.MaxAlt = new(int(maxAlt.Int64))
	}
	if minD.Valid {
		a.MinDistanceNm = new(minD.Float64)
	}
	if maxD.Valid {
		a.MaxDistanceNm = new(maxD.Float64)
	}
	var oldest sql.NullInt64
	if err := s.ro.QueryRowContext(ctx, `SELECT min(first_seen) FROM flights`).Scan(&oldest); err != nil {
		return a, fmt.Errorf("history: %w", err)
	}
	if oldest.Valid {
		a.RetainedFrom = new(time.UnixMilli(oldest.Int64).UTC())
	}

	rows, err := s.ro.QueryContext(ctx, `SELECT callsign, count(*) FROM flights WHERE icao = ? AND callsign IS NOT NULL
		GROUP BY callsign ORDER BY count(*) DESC, max(last_seen) DESC`, icao)
	if err != nil {
		return a, fmt.Errorf("history: %w", err)
	}
	for rows.Next() {
		var c CallsignCount
		if err := rows.Scan(&c.Callsign, &c.Flights); err != nil {
			rows.Close()
			return a, fmt.Errorf("history: %w", err)
		}
		a.Callsigns = append(a.Callsigns, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return a, fmt.Errorf("history: %w", err)
	}

	days := map[string]bool{}
	rows, err = s.ro.QueryContext(ctx, `SELECT first_seen, last_seen FROM flights WHERE icao = ?`, icao)
	if err != nil {
		return a, fmt.Errorf("history: %w", err)
	}
	for rows.Next() {
		var f, l int64
		if err := rows.Scan(&f, &l); err != nil {
			rows.Close()
			return a, fmt.Errorf("history: %w", err)
		}
		end := time.UnixMilli(l).In(loc)
		for d := time.UnixMilli(f).In(loc); ; d = d.AddDate(0, 0, 1) {
			days[d.Format(time.DateOnly)] = true
			if d.Format(time.DateOnly) >= end.Format(time.DateOnly) {
				break
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return a, fmt.Errorf("history: %w", err)
	}
	a.DaysSeen = len(days)

	rows, err = s.ro.QueryContext(ctx, `SELECT coalesce(max(CASE q.quality WHEN 'lost' THEN 2 WHEN 'degraded' THEN 1 ELSE 0 END), 0)
		FROM flights f LEFT JOIN position_quality q ON q.flight_id = f.id WHERE f.icao = ? AND f.adsb = 1 GROUP BY f.id`, icao)
	if err != nil {
		return a, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	var qs QualitySummary
	n := 0
	for rows.Next() {
		var w int
		if err := rows.Scan(&w); err != nil {
			return a, fmt.Errorf("history: %w", err)
		}
		n++
		switch w {
		case 2:
			qs.Lost++
		case 1:
			qs.Degraded++
		default:
			qs.OK++
		}
	}
	if n > 0 {
		a.Quality = &qs
	}
	return a, rows.Err()
}

func minValid(a, b sql.NullInt64) int64 {
	switch {
	case a.Valid && b.Valid:
		return min(a.Int64, b.Int64)
	case a.Valid:
		return a.Int64
	}
	return b.Int64
}

func (s *Store) flightRows(ctx context.Context, query string, args ...any) ([]Flight, error) {
	rows, err := s.ro.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	out := []Flight{}
	for rows.Next() {
		f, err := scanFlight(rows)
		if err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Track is one flight's positions as parallel arrays, oldest first: t in unix ms, alt in ft, gs in kt, track in
// degrees; unknown values are null.
type Track struct {
	ID    int64      `json:"id"`
	ICAO  string     `json:"icao"`
	T     []int64    `json:"t"`
	Lat   []float64  `json:"lat"`
	Lon   []float64  `json:"lon"`
	Alt   []*int     `json:"alt"`
	GS    []*float64 `json:"gs"`
	Track []*float64 `json:"track"`
}

// Tracks returns the positions of the given flights in the order of ids; unknown ids are left out.
func (s *Store) Tracks(ctx context.Context, ids []int64) ([]Track, error) {
	out := []Track{}
	for _, id := range ids {
		tr := Track{ID: id, T: []int64{}, Lat: []float64{}, Lon: []float64{}, Alt: []*int{}, GS: []*float64{}, Track: []*float64{}}
		err := s.ro.QueryRowContext(ctx, `SELECT icao FROM flights WHERE id = ?`, id).Scan(&tr.ICAO)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		rows, err := s.ro.QueryContext(ctx, `SELECT t, lat, lon, alt, gs, track FROM positions WHERE flight_id = ? ORDER BY t`, id)
		if err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		for rows.Next() {
			var (
				t         int64
				lat, lon  float64
				alt       sql.NullInt64
				gs, track sql.NullFloat64
			)
			if err := rows.Scan(&t, &lat, &lon, &alt, &gs, &track); err != nil {
				rows.Close()
				return nil, fmt.Errorf("history: %w", err)
			}
			tr.T, tr.Lat, tr.Lon = append(tr.T, t), append(tr.Lat, lat), append(tr.Lon, lon)
			tr.Alt = append(tr.Alt, nullPtr(alt.Valid, int(alt.Int64)))
			tr.GS = append(tr.GS, nullPtr(gs.Valid, gs.Float64))
			tr.Track = append(tr.Track, nullPtr(track.Valid, track.Float64))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		out = append(out, tr)
	}
	return out, nil
}

func nullPtr[T any](ok bool, v T) *T {
	if !ok {
		return nil
	}
	return &v
}
