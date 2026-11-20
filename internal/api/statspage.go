package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"
	_ "time/tzdata" // IANA zones for range-daily also where the system has none (distroless)
)

const (
	defaultStatsRange = 24 * time.Hour
	maxStatsRange     = 400 * 24 * time.Hour // hours of rows per request
	defaultStatsLimit = 20
	defaultNotable    = 100
	maxStatsLimit     = 500
)

var tzRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+\-]*(/[A-Za-z0-9_+\-]+)*$`)

// storage serves GET /api/storage: disk usage, growth, projections and flags (cached for 60 s).
func (s *server) storage(w http.ResponseWriter, r *http.Request) {
	if s.Storage == nil {
		writeError(w, http.StatusServiceUnavailable, "storage report unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.Storage.Report(r.Context()))
}

// statsRange reads from/to (RFC 3339 or unix ms; default the last 24 h), clamps from to the rollup retention and
// rejects ranges longer than maxStatsRange.
func (s *server) statsRange(r *http.Request) (from, to time.Time, err error) {
	p := r.URL.Query()
	now := s.Now()
	var errs []error
	if from, err = parseTime("from", p.Get("from")); err != nil {
		errs = append(errs, err)
	}
	if to, err = parseTime("to", p.Get("to")); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return from, to, errors.Join(errs...)
	}
	if to.IsZero() {
		to = now
	}
	if from.IsZero() {
		from = to.Add(-defaultStatsRange)
	}
	if ret := s.History.StatsRetention(); ret > 0 && from.Before(now.Add(-ret)) {
		from = now.Add(-ret)
	}
	switch {
	case !to.After(from):
		return from, to, errors.New("to must be after from (and within stats.retention)")
	case to.Sub(from) > maxStatsRange:
		return from, to, fmt.Errorf("the range may span at most %d days", int(maxStatsRange/(24*time.Hour)))
	}
	return from, to, nil
}

// statsLimit reads limit (1–maxStatsLimit), def when absent.
func statsLimit(r *http.Request, def int) (int, error) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxStatsLimit {
		return 0, fmt.Errorf("limit must be 1–%d", maxStatsLimit)
	}
	return n, nil
}

// statsHourly serves GET /api/stats/hourly?from=&to=: the hourly rollups, plus the current hour (partial).
func (s *server) statsHourly(w http.ResponseWriter, r *http.Request) {
	if !s.historyStore(w) {
		return
	}
	from, to, err := s.statsRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.History.Hourly(r.Context(), from, to, s.Now())
	if err != nil {
		s.historyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// statsCountries serves GET /api/stats/countries?from=&to=&limit=: aircraft-hours per registration country.
func (s *server) statsCountries(w http.ResponseWriter, r *http.Request) {
	s.statsTop(w, r, func(from, to time.Time, limit int) (any, error) {
		return s.History.Countries(r.Context(), from, to, s.Now(), limit)
	})
}

// statsOperators serves GET /api/stats/operators?from=&to=&limit=: aircraft-hours per callsign prefix.
func (s *server) statsOperators(w http.ResponseWriter, r *http.Request) {
	s.statsTop(w, r, func(from, to time.Time, limit int) (any, error) {
		return s.History.Operators(r.Context(), from, to, s.Now(), limit)
	})
}

func (s *server) statsTop(w http.ResponseWriter, r *http.Request, query func(from, to time.Time, limit int) (any, error)) {
	if !s.historyStore(w) {
		return
	}
	from, to, err := s.statsRange(r)
	limit, lerr := statsLimit(r, defaultStatsLimit)
	if err = errors.Join(err, lerr); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := query(from, to, limit)
	if err != nil {
		s.historyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// statsAltitude serves GET /api/stats/altitude?from=&to=: stored positions per altitude band.
func (s *server) statsAltitude(w http.ResponseWriter, r *http.Request) {
	if !s.historyStore(w) {
		return
	}
	from, to, err := s.statsRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	bands, err := s.History.AltitudeBands(r.Context(), from, to, s.Now())
	if err != nil {
		s.historyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bands)
}

// statsNotable serves GET /api/stats/notable?from=&to=&limit=: emergencies and first sightings of ICAO addresses.
func (s *server) statsNotable(w http.ResponseWriter, r *http.Request) {
	if !s.historyStore(w) {
		return
	}
	from, to, err := s.statsRange(r)
	limit, lerr := statsLimit(r, defaultNotable)
	if err = errors.Join(err, lerr); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := s.History.NotableEvents(r.Context(), from, to, s.Now(), limit)
	if err != nil {
		s.historyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// statsRangeDaily serves GET /api/stats/range-daily?from=&to=&tz=: the furthest position per day in the IANA
// timezone tz (default UTC).
func (s *server) statsRangeDaily(w http.ResponseWriter, r *http.Request) {
	if !s.historyStore(w) {
		return
	}
	from, to, err := s.statsRange(r)
	loc, lerr := parseTZ(r.URL.Query().Get("tz"))
	if err = errors.Join(err, lerr); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	days, err := s.History.RangeDaily(r.Context(), from, to, s.Now(), loc)
	if err != nil {
		s.historyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, days)
}

// parseTZ loads an IANA timezone name; "" is UTC, and "Local" (the server's zone) is refused.
func parseTZ(v string) (*time.Location, error) {
	if v == "" {
		return time.UTC, nil
	}
	if len(v) > 64 || v == "Local" || !tzRe.MatchString(v) {
		return nil, fmt.Errorf("tz must be an IANA timezone name, got %q", v)
	}
	loc, err := time.LoadLocation(v)
	if err != nil {
		return nil, fmt.Errorf("tz: unknown timezone %q", v)
	}
	return loc, nil
}
