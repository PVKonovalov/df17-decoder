package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

// ErrNotFound is returned for an unknown flight ID.
var ErrNotFound = errors.New("history: flight not found")

// Flight is a stored flight. Times are data time; Country is derived from the ICAO address.
type Flight struct {
	ID            int64     `json:"id"`
	ICAO          string    `json:"icao"`
	Callsign      string    `json:"callsign,omitempty"`
	Squawk        string    `json:"squawk,omitempty"`
	Category      string    `json:"category,omitempty"`
	Country       string    `json:"country,omitempty"`
	ADSB          bool      `json:"adsb"`
	FirstSeen     time.Time `json:"firstSeen"`
	LastSeen      time.Time `json:"lastSeen"`
	Closed        bool      `json:"closed"`
	Points        int       `json:"points"`
	MaxAlt        *int      `json:"maxAlt,omitempty"`
	MinDistanceNm *float64  `json:"minDistanceNm,omitempty"`
}

// SamplePoint is a stored altitude/squawk sample; T is unix ms in data time.
type SamplePoint struct {
	T      int64  `json:"t"`
	Alt    *int   `json:"alt,omitempty"`
	Squawk string `json:"squawk,omitempty"`
}

// FlightDetail is a flight with all its positions and samples in time order.
type FlightDetail struct {
	Flight
	Positions []tracker.TrackPoint `json:"positions"`
	Samples   []SamplePoint        `json:"samples"`
}

// State is one flight's latest known state at a moment: its last position and/or sample.
type State struct {
	Flight   Flight              `json:"flight"`
	Position *tracker.TrackPoint `json:"position,omitempty"`
	Sample   *SamplePoint        `json:"sample,omitempty"`
}

// FlightQuery selects flights overlapping [From, To]; ICAO "" matches every address. Text, when set, matches the
// ICAO address exactly or the start of the callsign (case-insensitive).
type FlightQuery struct {
	From, To time.Time
	ICAO     string
	Text     string
	Limit    int
}

const flightColumns = `f.id, f.icao, f.callsign, f.squawk, f.category, f.adsb, f.first_seen, f.last_seen, f.closed,
	f.points, f.max_alt, f.min_distance_nm`

type scanner interface{ Scan(dest ...any) error }

func scanFlight(row scanner, extra ...any) (Flight, error) {
	var (
		f                          Flight
		callsign, squawk, category sql.NullString
		firstSeen, lastSeen        int64
		maxAlt                     sql.NullInt64
		minDist                    sql.NullFloat64
	)
	dest := append([]any{&f.ID, &f.ICAO, &callsign, &squawk, &category, &f.ADSB, &firstSeen, &lastSeen, &f.Closed,
		&f.Points, &maxAlt, &minDist}, extra...)
	if err := row.Scan(dest...); err != nil {
		return f, err
	}
	f.Callsign, f.Squawk, f.Category = callsign.String, squawk.String, category.String
	f.FirstSeen, f.LastSeen = time.UnixMilli(firstSeen).UTC(), time.UnixMilli(lastSeen).UTC()
	if maxAlt.Valid {
		f.MaxAlt = new(int(maxAlt.Int64))
	}
	if minDist.Valid {
		f.MinDistanceNm = new(minDist.Float64)
	}
	if icao, err := strconv.ParseUint(f.ICAO, 16, 32); err == nil {
		f.Country = tracker.Country(uint32(icao))
	}
	return f, nil
}

// positionColumns are scanned by positionDest into a TrackPoint, all nullable (LEFT JOIN).
type positionDest struct {
	t, alt, vr        sql.NullInt64
	lat, lon, gs, trk sql.NullFloat64
	gnss, ground      sql.NullBool
}

func (d *positionDest) dest() []any {
	return []any{&d.t, &d.lat, &d.lon, &d.alt, &d.gnss, &d.gs, &d.trk, &d.vr, &d.ground}
}

func (d *positionDest) point() *tracker.TrackPoint {
	if !d.t.Valid {
		return nil
	}
	p := &tracker.TrackPoint{T: d.t.Int64, Lat: d.lat.Float64, Lon: d.lon.Float64, OnGround: d.ground.Bool}
	if d.alt.Valid {
		p.Alt, p.AltSrc = int(d.alt.Int64), tracker.AltBaro
		if d.gnss.Bool {
			p.AltSrc = tracker.AltGNSS
		}
	}
	if d.gs.Valid {
		p.GS, p.Known = d.gs.Float64, p.Known|tracker.KnownGS
	}
	if d.trk.Valid {
		p.Track, p.Known = d.trk.Float64, p.Known|tracker.KnownTrack
	}
	if d.vr.Valid {
		p.VR, p.Known = int(d.vr.Int64), p.Known|tracker.KnownVR
	}
	return p
}

type sampleDest struct {
	t, alt sql.NullInt64
	squawk sql.NullString
}

func (d *sampleDest) dest() []any { return []any{&d.t, &d.alt, &d.squawk} }

func (d *sampleDest) sample() *SamplePoint {
	if !d.t.Valid {
		return nil
	}
	s := &SamplePoint{T: d.t.Int64, Squawk: d.squawk.String}
	if d.alt.Valid {
		s.Alt = new(int(d.alt.Int64))
	}
	return s
}

// Flights returns the flights overlapping the query's time range, newest first.
func (s *Store) Flights(ctx context.Context, q FlightQuery) ([]Flight, error) {
	from, to := int64(math.MinInt64), int64(math.MaxInt64)
	if !q.From.IsZero() {
		from = q.From.UnixMilli()
	}
	if !q.To.IsZero() {
		to = q.To.UnixMilli()
	}
	query := `SELECT ` + flightColumns + ` FROM flights f WHERE f.first_seen <= ? AND f.last_seen >= ?`
	args := []any{to, from}
	if q.ICAO != "" {
		query += ` AND f.icao = ?`
		args = append(args, strings.ToUpper(q.ICAO))
	}
	if t := strings.ToUpper(strings.TrimSpace(q.Text)); t != "" {
		query += ` AND (f.icao = ? OR f.callsign LIKE ? ESCAPE '\')`
		args = append(args, t, strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(t)+"%")
	}
	query += ` ORDER BY f.first_seen DESC, f.id DESC LIMIT ?`
	args = append(args, q.Limit)

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

// FlightByID returns one flight with its positions and samples, or ErrNotFound.
func (s *Store) FlightByID(ctx context.Context, id int64) (FlightDetail, error) {
	f, err := scanFlight(s.ro.QueryRowContext(ctx, `SELECT `+flightColumns+` FROM flights f WHERE f.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return FlightDetail{}, ErrNotFound
	}
	if err != nil {
		return FlightDetail{}, fmt.Errorf("history: %w", err)
	}
	d := FlightDetail{Flight: f, Positions: []tracker.TrackPoint{}, Samples: []SamplePoint{}}

	rows, err := s.ro.QueryContext(ctx, `SELECT t, lat, lon, alt, alt_gnss, gs, track, vr, on_ground
		FROM positions WHERE flight_id = ? ORDER BY t`, id)
	if err != nil {
		return d, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p positionDest
		if err := rows.Scan(p.dest()...); err != nil {
			return d, fmt.Errorf("history: %w", err)
		}
		d.Positions = append(d.Positions, *p.point())
	}
	if err := rows.Err(); err != nil {
		return d, fmt.Errorf("history: %w", err)
	}

	srows, err := s.ro.QueryContext(ctx, `SELECT t, alt, squawk FROM samples WHERE flight_id = ? ORDER BY t`, id)
	if err != nil {
		return d, fmt.Errorf("history: %w", err)
	}
	defer srows.Close()
	for srows.Next() {
		var smp sampleDest
		if err := srows.Scan(smp.dest()...); err != nil {
			return d, fmt.Errorf("history: %w", err)
		}
		d.Samples = append(d.Samples, *smp.sample())
	}
	return d, srows.Err()
}

// At returns, for every flight present at data time t, its last position and last sample in [t-maxAge, t];
// flights with neither are left out. Ordered by ICAO address.
func (s *Store) At(ctx context.Context, t time.Time, maxAge time.Duration) ([]State, error) {
	hi := t.UnixMilli()
	lo := hi - maxAge.Milliseconds()
	rows, err := s.ro.QueryContext(ctx, `SELECT `+flightColumns+`,
			p.t, p.lat, p.lon, p.alt, p.alt_gnss, p.gs, p.track, p.vr, p.on_ground, sm.t, sm.alt, sm.squawk
		FROM flights f
		LEFT JOIN positions p ON p.flight_id = f.id
			AND p.t = (SELECT max(t) FROM positions WHERE flight_id = f.id AND t BETWEEN ?1 AND ?2)
		LEFT JOIN samples sm ON sm.flight_id = f.id
			AND sm.t = (SELECT max(t) FROM samples WHERE flight_id = f.id AND t BETWEEN ?1 AND ?2)
		WHERE f.first_seen <= ?2 AND f.last_seen >= ?1 AND (p.t IS NOT NULL OR sm.t IS NOT NULL)
		ORDER BY f.icao, f.id`, lo, hi)
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	out := []State{}
	for rows.Next() {
		var (
			p   positionDest
			smp sampleDest
		)
		f, err := scanFlight(rows, append(p.dest(), smp.dest()...)...)
		if err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		out = append(out, State{Flight: f, Position: p.point(), Sample: smp.sample()})
	}
	return out, rows.Err()
}
