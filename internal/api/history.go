package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/history"
)

const (
	defaultHistoryLimit = 100
	maxHistoryLimit     = 1000
)

// historyStore writes a JSON 503 and returns false when history is disabled.
func (s *server) historyStore(w http.ResponseWriter) bool {
	if s.History == nil {
		writeError(w, http.StatusServiceUnavailable, "history is disabled (history.enabled)")
		return false
	}
	return true
}

// parseTime parses v as unix milliseconds or RFC 3339; "" gives the zero time.
func parseTime(name, v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.UnixMilli(ms), nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return t, fmt.Errorf("%s must be unix ms or RFC 3339, got %q", name, v)
	}
	return t, nil
}

func validICAO(v string) bool {
	_, err := strconv.ParseUint(v, 16, 32)
	return err == nil && len(v) == 6
}

// historyFlights serves GET /api/history/flights?from=&to=&icao=&q=&before=&beforeId=&limit=: flights overlapping
// [from, to], newest first, at most limit (default 100, max 1000); q matches the ICAO address or the start of the
// callsign; before (unix ms, a first_seen) and beforeId (that row's id) page on. onTape tells whether the tape
// covers the flight.
func (s *server) historyFlights(w http.ResponseWriter, r *http.Request) {
	if !s.historyStore(w) {
		return
	}
	p := r.URL.Query()
	q := history.FlightQuery{ICAO: p.Get("icao"), Text: p.Get("q"), Limit: defaultHistoryLimit}
	var errs []error
	var err error
	if q.From, err = parseTime("from", p.Get("from")); err != nil {
		errs = append(errs, err)
	}
	if q.To, err = parseTime("to", p.Get("to")); err != nil {
		errs = append(errs, err)
	}
	if !q.From.IsZero() && !q.To.IsZero() && q.To.Before(q.From) {
		errs = append(errs, errors.New("to must not be before from"))
	}
	if q.ICAO != "" && !validICAO(q.ICAO) {
		errs = append(errs, errors.New("icao must be 6 hex digits"))
	}
	if len(q.Text) > 16 {
		errs = append(errs, errors.New("q must be at most 16 characters"))
	}
	if v := p.Get("before"); v != "" {
		if q.Before, err = strconv.ParseInt(v, 10, 64); err != nil || q.Before < 1 {
			errs = append(errs, errors.New("before must be unix ms"))
		}
	}
	if v := p.Get("beforeId"); v != "" {
		if q.BeforeID, err = strconv.ParseInt(v, 10, 64); err != nil || q.BeforeID < 1 || q.Before == 0 {
			errs = append(errs, errors.New("beforeId must be a positive integer, with before"))
		}
	}
	if v := p.Get("limit"); v != "" {
		if q.Limit, err = strconv.Atoi(v); err != nil || q.Limit < 1 || q.Limit > maxHistoryLimit {
			errs = append(errs, fmt.Errorf("limit must be 1–%d", maxHistoryLimit))
		}
	}
	if len(errs) > 0 {
		writeError(w, http.StatusBadRequest, errors.Join(errs...).Error())
		return
	}
	list, err := s.History.Flights(r.Context(), q)
	if err != nil {
		s.historyError(w, err)
		return
	}
	s.markOnTape(list)
	writeJSON(w, http.StatusOK, list)
}

// historyFlight serves GET /api/history/flights/{id}: one flight with its positions and samples.
func (s *server) historyFlight(w http.ResponseWriter, r *http.Request) {
	if !s.historyStore(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return
	}
	f, err := s.History.FlightByID(r.Context(), id)
	switch {
	case errors.Is(err, history.ErrNotFound):
		writeError(w, http.StatusNotFound, "flight not found")
	case err != nil:
		s.historyError(w, err)
	default:
		writeJSON(w, http.StatusOK, f)
	}
}

// historyAt serves GET /api/history/at?t=: every flight's last position and sample at or before t, not older than
// the tracker TTL.
func (s *server) historyAt(w http.ResponseWriter, r *http.Request) {
	if !s.historyStore(w) {
		return
	}
	t, err := parseTime("t", r.URL.Query().Get("t"))
	if err == nil && t.IsZero() {
		err = errors.New("t is required")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	states, err := s.History.At(r.Context(), t, s.Tracker.TTL())
	if err != nil {
		s.historyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, states)
}

// historyTimeline serves GET /api/history/timeline?from=&to=&bucket=1m: flights active per bucket ([ADS-B, Mode-S
// only]) and emergency events, for the timeline's density strip. At most history.MaxTimelineBuckets buckets.
func (s *server) historyTimeline(w http.ResponseWriter, r *http.Request) {
	if !s.historyStore(w) {
		return
	}
	p := r.URL.Query()
	var errs []error
	from, err := parseTime("from", p.Get("from"))
	if err != nil {
		errs = append(errs, err)
	}
	to, err := parseTime("to", p.Get("to"))
	if err != nil {
		errs = append(errs, err)
	}
	if err == nil && (from.IsZero() || to.IsZero() || !to.After(from)) {
		errs = append(errs, errors.New("from and to are required, with to after from"))
	}
	bucket := time.Minute
	if v := p.Get("bucket"); v != "" {
		if bucket, err = time.ParseDuration(v); err != nil || bucket < time.Second {
			errs = append(errs, fmt.Errorf("bucket must be a duration of at least 1s, got %q", v))
		}
	}
	if len(errs) == 0 && to.Sub(from.Truncate(bucket)) > time.Duration(history.MaxTimelineBuckets)*bucket {
		errs = append(errs, fmt.Errorf("at most %d buckets: use a larger bucket", history.MaxTimelineBuckets))
	}
	if len(errs) > 0 {
		writeError(w, http.StatusBadRequest, errors.Join(errs...).Error())
		return
	}
	tl, err := s.History.Timeline(r.Context(), from, to, bucket)
	if err != nil {
		s.historyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tl)
}

func (s *server) historyError(w http.ResponseWriter, err error) {
	s.Logger.Error("history read", "err", err)
	writeError(w, http.StatusInternalServerError, "history read failed")
}
