package history

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// LearningDays are the days after the oldest sighting whose "new" counts are mostly the start of recording.
const LearningDays = 7

// DailyRow is one local day of GET /api/stats/daily.
type DailyRow struct {
	Day         string `json:"day"`
	Aircraft    int    `json:"aircraft"`
	ADSB        int    `json:"adsb"`
	ModeS       int    `json:"modeS"`
	New         int    `json:"new"`
	NewADSB     int    `json:"newAdsb"`
	NewModeS    int    `json:"newModeS"`
	Returning   int    `json:"returning"`
	Learning    bool   `json:"learning"`    // within LearningDays of the oldest sighting
	Partial     bool   `json:"partial"`     // today, so far
	Approximate bool   `json:"approximate"` // before daily_exact_from: flights deleted or another timezone
}

// DailyTotals is GET /api/stats/daily.
type DailyTotals struct {
	Timezone string     `json:"timezone"`
	Days     []DailyRow `json:"days"`
}

// NewAircraft is one aircraft first seen on a day (GET /api/stats/new).
type NewAircraft struct {
	ICAO      string    `json:"icao"`
	Callsign  string    `json:"callsign,omitempty"` // of its first flight that day with one
	Country   string    `json:"country,omitempty"`
	FirstSeen time.Time `json:"firstSeen"`
	ADSB      bool      `json:"adsb"` // sent ADS-B that day
}

// firstSeenSQL is the address's first sighting: aircraft_seen, else (not rolled up yet) its earliest flight.
const firstSeenSQL = `coalesce((SELECT fs_a.first_seen FROM aircraft_seen fs_a WHERE fs_a.icao = %[1]s),
	(SELECT min(fs_f.first_seen) FROM flights fs_f WHERE fs_f.icao = %[1]s))`

// Daily returns the local days (Options.Location) from from's day to to's day, not before the oldest sighting
// nor after today: distinct aircraft from daily_aircraft, plus for today and yesterday the aircraft of the hours
// not rolled up yet (the current and the previous hour), computed from the flights.
func (s *Store) Daily(ctx context.Context, from, to, now time.Time) (DailyTotals, error) {
	loc := s.location()
	out := DailyTotals{Timezone: loc.String(), Days: []DailyRow{}}
	var oldest sql.NullInt64
	if err := s.ro.QueryRowContext(ctx, `SELECT min(m) FROM (SELECT min(first_seen) AS m FROM aircraft_seen
		UNION ALL SELECT min(first_seen) FROM flights)`).Scan(&oldest); err != nil {
		return out, fmt.Errorf("history: %w", err)
	}
	if !oldest.Valid {
		return out, nil
	}
	first := max(dayOf(from, loc), dayOf(time.UnixMilli(oldest.Int64), loc))
	today := dayOf(now, loc)
	last := min(dayOf(to, loc), today)
	if first > last {
		return out, nil
	}
	learnUntil := dayOf(time.UnixMilli(oldest.Int64).In(loc).AddDate(0, 0, LearningDays), loc)
	exact, err := s.DailyExactFrom(ctx)
	if err != nil {
		return out, fmt.Errorf("history: %w", err)
	}

	type seen struct {
		adsb  bool
		first int64
	}
	byDay := map[string]map[string]seen{}
	add := func(day, icao string, adsb bool, firstSeen int64) {
		if day < first || day > last {
			return
		}
		m := byDay[day]
		if m == nil {
			m = map[string]seen{}
			byDay[day] = m
		}
		m[icao] = seen{m[icao].adsb || adsb, firstSeen}
	}
	rows, err := s.ro.QueryContext(ctx, `SELECT d.day, d.icao, d.adsb, `+fmt.Sprintf(firstSeenSQL, "d.icao")+`
		FROM daily_aircraft d WHERE d.day >= ? AND d.day <= ?`, first, last)
	if err != nil {
		return out, fmt.Errorf("history: %w", err)
	}
	for rows.Next() {
		var day, icao string
		var adsb bool
		var fs int64
		if err := rows.Scan(&day, &icao, &adsb, &fs); err != nil {
			rows.Close()
			return out, fmt.Errorf("history: %w", err)
		}
		add(day, icao, adsb, fs)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("history: %w", err)
	}

	lo := now.UTC().Truncate(time.Hour).Add(-time.Hour).UnixMilli()
	hi := now.UnixMilli()
	rows, err = s.ro.QueryContext(ctx, `SELECT f.icao, f.adsb, f.first_seen, f.last_seen, `+fmt.Sprintf(firstSeenSQL, "f.icao")+`
		FROM flights f WHERE f.first_seen <= ? AND f.last_seen >= ?`, hi, lo)
	if err != nil {
		return out, fmt.Errorf("history: %w", err)
	}
	for rows.Next() {
		var icao string
		var adsb bool
		var f, l, fs int64
		if err := rows.Scan(&icao, &adsb, &f, &l, &fs); err != nil {
			rows.Close()
			return out, fmt.Errorf("history: %w", err)
		}
		live := map[dayICAO]bool{}
		addDays(live, icao, adsb, max(f, lo), min(l, hi), loc, "")
		for k, a := range live {
			add(k.day, k.icao, a, fs)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("history: %w", err)
	}

	start, _ := time.ParseInLocation(time.DateOnly, first, loc)
	for d := start.Add(12 * time.Hour); ; d = d.AddDate(0, 0, 1) {
		day := d.Format(time.DateOnly)
		if day > last {
			break
		}
		r := DailyRow{Day: day, Learning: day < learnUntil, Partial: day == today, Approximate: day < exact}
		for _, a := range byDay[day] {
			r.Aircraft++
			isNew := dayOf(time.UnixMilli(a.first), loc) == day
			if a.adsb {
				r.ADSB++
			}
			if isNew {
				r.New++
				if a.adsb {
					r.NewADSB++
				}
			}
		}
		r.ModeS, r.NewModeS, r.Returning = r.Aircraft-r.ADSB, r.New-r.NewADSB, r.Aircraft-r.New
		out.Days = append(out.Days, r)
	}
	return out, nil
}

// NewOn returns up to limit aircraft first seen on day ('YYYY-MM-DD' in Options.Location), by first sighting.
func (s *Store) NewOn(ctx context.Context, day string, limit int) ([]NewAircraft, error) {
	loc := s.location()
	start, err := time.ParseInLocation(time.DateOnly, day, loc)
	if err != nil {
		return nil, fmt.Errorf("history: day: %w", err)
	}
	lo, hi := start.UnixMilli(), start.AddDate(0, 0, 1).UnixMilli()
	rows, err := s.ro.QueryContext(ctx, `WITH c AS (
			SELECT icao FROM aircraft_seen WHERE first_seen >= ?1 AND first_seen < ?2
			UNION SELECT icao FROM flights WHERE first_seen >= ?1 AND first_seen < ?2),
		e AS (SELECT c.icao, `+fmt.Sprintf(firstSeenSQL, "c.icao")+` AS fs FROM c)
		SELECT e.icao, e.fs,
			coalesce((SELECT f.callsign FROM flights f WHERE f.icao = e.icao AND f.first_seen < ?2
				AND f.last_seen >= ?1 AND f.callsign IS NOT NULL ORDER BY f.first_seen LIMIT 1), ''),
			coalesce((SELECT d.adsb FROM daily_aircraft d WHERE d.day = ?3 AND d.icao = e.icao),
				(SELECT max(f.adsb) FROM flights f WHERE f.icao = e.icao AND f.first_seen < ?2 AND f.last_seen >= ?1), 0)
		FROM e WHERE e.fs >= ?1 AND e.fs < ?2 ORDER BY e.fs, e.icao LIMIT ?4`, lo, hi, day, limit)
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	out := []NewAircraft{}
	for rows.Next() {
		var a NewAircraft
		var fs int64
		if err := rows.Scan(&a.ICAO, &fs, &a.Callsign, &a.ADSB); err != nil {
			return nil, fmt.Errorf("history: %w", err)
		}
		a.FirstSeen = time.UnixMilli(fs).UTC()
		a.Country = country(a.ICAO)
		out = append(out, a)
	}
	return out, rows.Err()
}
