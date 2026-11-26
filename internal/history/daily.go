package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	dailyTZKey        = "daily_tz"         // meta: the timezone daily_aircraft was built in
	dailyExactFromKey = "daily_exact_from" // meta: days before it may be incomplete or in a previous timezone
	dailyChunk        = 1000               // flights per transaction of the build
	dailyLogEvery     = 50                 // chunks between progress logs
)

// testDailyChunk, when set by a test, runs before every chunk of the build.
var testDailyChunk func()

type dayICAO struct{ day, icao string }

func (s *Store) location() *time.Location {
	if s.opt.Location != nil {
		return s.opt.Location
	}
	return time.Local
}

// dayOf is the 'YYYY-MM-DD' of t in loc.
func dayOf(t time.Time, loc *time.Location) string {
	return t.In(loc).Format(time.DateOnly)
}

// addDays marks icao on every day in loc from `from` to `to` (unix ms, inclusive), skipping days before
// skipBefore; adsb is OR-ed.
func addDays(m map[dayICAO]bool, icao string, adsb bool, from, to int64, loc *time.Location, skipBefore string) {
	last := dayOf(time.UnixMilli(to), loc)
	y, mo, d := time.UnixMilli(from).In(loc).Date()
	for t := time.Date(y, mo, d, 12, 0, 0, 0, loc); ; t = t.AddDate(0, 0, 1) {
		day := t.Format(time.DateOnly)
		if day > last {
			return
		}
		if day >= skipBefore {
			k := dayICAO{day, icao}
			m[k] = m[k] || adsb
		}
	}
}

// writeDays upserts the rows; adsb stays true once set for the day.
func writeDays(tx *sql.Tx, m map[dayICAO]bool) error {
	for k, adsb := range m {
		if _, err := tx.Exec(`INSERT INTO daily_aircraft (day, icao, adsb) VALUES (?, ?, ?)
			ON CONFLICT (day, icao) DO UPDATE SET adsb = max(adsb, excluded.adsb)`, k.day, k.icao, adsb); err != nil {
			return err
		}
	}
	return nil
}

// tzKey names loc for meta; the server's local zone also gets its January and July offsets, so a changed TZ of
// the host is noticed.
func tzKey(loc *time.Location, year int) string {
	if loc.String() != "Local" {
		return loc.String()
	}
	n1, o1 := time.Date(year, 1, 1, 0, 0, 0, 0, loc).Zone()
	n2, o2 := time.Date(year, 7, 1, 0, 0, 0, 0, loc).Zone()
	return fmt.Sprintf("Local %s%+d %s%+d", n1, o1, n2, o2)
}

func (s *Store) meta(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// DailyExactFrom returns the first day of daily_aircraft known to be complete in the current timezone; earlier
// days are approximate. Empty when every day is.
func (s *Store) DailyExactFrom(ctx context.Context) (string, error) {
	var v string
	err := s.ro.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, dailyExactFromKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// buildDaily (re)builds daily_aircraft from the stored flights when it was never built or was built in another
// timezone. Flight retention may have deleted the flights of old days: those are kept as they are (from the
// previous timezone) and the first complete day is stored as daily_exact_from. daily_tz is written last, so an
// interrupted build starts over.
func (s *Store) buildDaily(ctx context.Context) error {
	loc := s.location()
	key := tzKey(loc, s.opt.Now().Year())
	prev, built, err := s.meta(ctx, dailyTZKey)
	if err != nil || prev == key {
		return err
	}
	var oldestFlight, oldestSeen sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT min(first_seen) FROM flights),
		(SELECT min(first_seen) FROM aircraft_seen)`).Scan(&oldestFlight, &oldestSeen); err != nil {
		return err
	}
	exact := ""
	if oldestFlight.Valid && oldestSeen.Valid && oldestSeen.Int64 < oldestFlight.Int64 { // flights were deleted
		exact = dayOf(time.UnixMilli(oldestFlight.Int64).AddDate(0, 0, 1), loc)
	}
	skip := ""
	if built {
		skip = exact
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM daily_aircraft WHERE day >= ?`, skip); err != nil {
		return err
	}
	if exact == "" {
		_, err = tx.Exec(`DELETE FROM meta WHERE key = ?`, dailyExactFromKey)
	} else {
		_, err = tx.Exec(`INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			dailyExactFromKey, exact)
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM flights`).Scan(&total); err != nil {
		return err
	}
	switch {
	case built && exact != "":
		s.opt.Logger.Warn("daily aircraft: the timezone changed; days before the oldest stored flight can't be rebuilt and stay approximate",
			"from", prev, "to", key, "approximateBefore", exact)
	case built:
		s.opt.Logger.Info("daily aircraft: the timezone changed, rebuilding", "from", prev, "to", key)
	}
	s.opt.Logger.Info("daily aircraft: building from the stored flights", "timezone", key, "flights", total)

	var after int64
	for chunk := 1; ; chunk++ {
		if testDailyChunk != nil {
			testDailyChunk()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		n, last, err := s.buildDailyChunk(ctx, after, loc, skip)
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
		after = last
		if chunk%dailyLogEvery == 0 {
			s.opt.Logger.Info("daily aircraft: progress", "flights", chunk*dailyChunk, "of", total)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, dailyTZKey, key); err != nil {
		return err
	}
	s.opt.Logger.Info("daily aircraft: built", "flights", total)
	return nil
}

// buildDailyChunk adds the days of up to dailyChunk flights with ids after `after`; it returns how many it read
// and the last id.
func (s *Store) buildDailyChunk(ctx context.Context, after int64, loc *time.Location, skip string) (int, int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, icao, adsb, first_seen, last_seen FROM flights WHERE id > ?
		ORDER BY id LIMIT ?`, after, dailyChunk)
	if err != nil {
		return 0, after, err
	}
	days := map[dayICAO]bool{}
	n := 0
	for rows.Next() {
		var icao string
		var adsb bool
		var first, last int64
		if err := rows.Scan(&after, &icao, &adsb, &first, &last); err != nil {
			rows.Close()
			return 0, after, err
		}
		addDays(days, icao, adsb, first, last, loc, skip)
		n++
	}
	rows.Close()
	if err := rows.Err(); err != nil || n == 0 {
		return n, after, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, after, err
	}
	defer tx.Rollback()
	if err := writeDays(tx, days); err != nil {
		return 0, after, err
	}
	return n, after, tx.Commit()
}
