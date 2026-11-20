package history

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// MaxTimelineBuckets caps the buckets of one timeline query.
const MaxTimelineBuckets = 2000

// EmergencySquawks are the squawks reported as timeline events.
var EmergencySquawks = []string{"7500", "7600", "7700"}

// Timeline is the traffic density over [From, From + len(Buckets)·Bucket): per bucket the flights active in it,
// [ADS-B, Mode-S only], and the emergency events in the range.
type Timeline struct {
	From    time.Time `json:"from"`
	Bucket  string    `json:"bucket"` // Go duration
	Buckets [][2]int  `json:"buckets"`
	Events  []Event   `json:"events"`
}

// Event is an emergency squawk: T is when it was first seen (the first Mode-S sample with it, or the flight's first
// sighting for ADS-B flights, whose squawk changes are not stored).
type Event struct {
	T        time.Time `json:"t"`
	FlightID int64     `json:"flightId"`
	ICAO     string    `json:"icao"`
	Callsign string    `json:"callsign,omitempty"`
	Squawk   string    `json:"squawk"`
}

// Timeline counts the flights active in each bucket of [from, to) (from is truncated to the bucket) and lists the
// emergency events in that range. It fails when the range needs more than MaxTimelineBuckets buckets.
func (s *Store) Timeline(ctx context.Context, from, to time.Time, bucket time.Duration) (Timeline, error) {
	from = from.Truncate(bucket)
	n := int((to.Sub(from) + bucket - 1) / bucket)
	if bucket <= 0 || n < 1 || n > MaxTimelineBuckets {
		return Timeline{}, fmt.Errorf("history: timeline needs 1–%d buckets, got %d", MaxTimelineBuckets, n)
	}
	tl := Timeline{From: from.UTC(), Bucket: bucket.String(), Buckets: make([][2]int, n), Events: []Event{}}
	lo, hi, b := from.UnixMilli(), from.Add(time.Duration(n)*bucket).UnixMilli(), bucket.Milliseconds()

	rows, err := s.ro.QueryContext(ctx, `SELECT first_seen, last_seen, adsb FROM flights WHERE first_seen < ? AND last_seen >= ?`, hi, lo)
	if err != nil {
		return tl, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var first, last int64
		var adsb bool
		if err := rows.Scan(&first, &last, &adsb); err != nil {
			return tl, fmt.Errorf("history: %w", err)
		}
		kind := 1
		if adsb {
			kind = 0
		}
		for i := (max(first, lo) - lo) / b; i <= (min(last, hi-1)-lo)/b; i++ {
			tl.Buckets[i][kind]++
		}
	}
	if err := rows.Err(); err != nil {
		return tl, fmt.Errorf("history: %w", err)
	}

	erows, err := s.ro.QueryContext(ctx, `
		SELECT t, id, icao, callsign, squawk FROM (
			SELECT min(s.t) AS t, f.id, f.icao, f.callsign, s.squawk
			FROM samples s JOIN flights f ON f.id = s.flight_id
			WHERE s.squawk IN (?, ?, ?) AND s.t >= ? AND s.t < ?
			GROUP BY f.id, s.squawk
			UNION ALL
			SELECT f.first_seen, f.id, f.icao, f.callsign, f.squawk FROM flights f
			WHERE f.adsb AND f.squawk IN (?, ?, ?) AND f.first_seen < ? AND f.last_seen >= ?
		) ORDER BY t, id`,
		EmergencySquawks[0], EmergencySquawks[1], EmergencySquawks[2], lo, hi,
		EmergencySquawks[0], EmergencySquawks[1], EmergencySquawks[2], hi, lo)
	if err != nil {
		return tl, fmt.Errorf("history: %w", err)
	}
	defer erows.Close()
	for erows.Next() {
		var e Event
		var t int64
		var callsign *string
		if err := erows.Scan(&t, &e.FlightID, &e.ICAO, &callsign, &e.Squawk); err != nil {
			return tl, fmt.Errorf("history: %w", err)
		}
		if callsign != nil {
			e.Callsign = *callsign
		}
		e.T = time.UnixMilli(max(t, lo)).UTC()
		tl.Events = append(tl.Events, e)
	}
	slices.SortStableFunc(tl.Events, func(a, b Event) int { return a.T.Compare(b.T) })
	return tl, erows.Err()
}
