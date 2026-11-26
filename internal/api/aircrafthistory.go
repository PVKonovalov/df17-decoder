package api

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/PVKonovalov/df17-decoder/internal/history"
	"github.com/PVKonovalov/df17-decoder/internal/tape"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 50
)

// liveAircraft returns the tracker's aircraft by ICAO address.
func (s *server) liveAircraft() map[string]tracker.Aircraft {
	m := map[string]tracker.Aircraft{}
	for _, a := range s.Tracker.Snapshot() {
		m[a.ICAO] = a
	}
	return m
}

// historySearch serves GET /api/history/search?q=&limit=20: aircraft (not flights) whose address starts with q (hex)
// or whose callsign starts with or, from 3 characters, contains q; live first, then by last seen. Live aircraft
// not yet written to the history are included.
func (s *server) historySearch(w http.ResponseWriter, r *http.Request) {
	if !s.historyStore(w) {
		return
	}
	p := r.URL.Query()
	q := history.NormalizeSearch(p.Get("q"))
	var errs []error
	if q == "" || len(q) > 16 {
		errs = append(errs, errors.New("q must be 1–16 characters"))
	}
	limit := defaultSearchLimit
	if v := p.Get("limit"); v != "" {
		var err error
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > maxSearchLimit {
			errs = append(errs, fmt.Errorf("limit must be 1–%d", maxSearchLimit))
		}
	}
	if len(errs) > 0 {
		writeError(w, http.StatusBadRequest, errors.Join(errs...).Error())
		return
	}
	live := s.liveAircraft()
	found, err := s.History.Search(r.Context(), q, limit, func(icao string) bool { _, ok := live[icao]; return ok })
	if err != nil {
		s.historyError(w, err)
		return
	}
	known := map[string]bool{}
	for _, m := range found {
		known[m.ICAO] = true
	}
	for _, a := range live {
		if known[a.ICAO] || !history.MatchesSearch(q, a.ICAO, a.Callsign) {
			continue
		}
		m := history.AircraftMatch{ICAO: a.ICAO, LastCallsign: a.Callsign, Callsigns: []string{}, Country: a.Country,
			LastSeen: a.LastSeen, Flights: 1, ADSB: a.ADSB, Live: true}
		if a.Callsign != "" {
			m.Callsigns = append(m.Callsigns, a.Callsign)
		}
		found = append(found, m)
	}
	slices.SortStableFunc(found, func(a, b history.AircraftMatch) int {
		if a.Live != b.Live {
			if a.Live {
				return -1
			}
			return 1
		}
		return cmp.Or(b.LastSeen.Compare(a.LastSeen), cmp.Compare(a.ICAO, b.ICAO))
	})
	writeJSON(w, http.StatusOK, found[:min(len(found), limit)])
}

// historyAircraft serves GET /api/history/aircraft/{icao}: the address's summary over all time and over the
// flights still stored, with days counted in stats.timezone; 404 when it was never seen.
func (s *server) historyAircraft(w http.ResponseWriter, r *http.Request) {
	if !s.historyStore(w) {
		return
	}
	icao := strings.ToUpper(r.PathValue("icao"))
	if !validICAO(icao) {
		writeError(w, http.StatusBadRequest, "icao must be 6 hex digits")
		return
	}
	a, err := s.History.Aircraft(r.Context(), icao)
	switch {
	case errors.Is(err, history.ErrNoAircraft):
		writeError(w, http.StatusNotFound, "aircraft not in the history")
		return
	case err != nil:
		s.historyError(w, err)
		return
	}
	if v, err := strconv.ParseUint(icao, 16, 24); err == nil {
		_, a.Live = s.Tracker.Get(uint32(v))
	}
	writeJSON(w, http.StatusOK, a)
}

// TracksResponse is GET /api/history/tracks.
type TracksResponse struct {
	Tracks []history.Track `json:"tracks"`
}

// historyTracks serves GET /api/history/tracks?ids=1,2,3: the positions of up to history.MaxTrackIDs flights as
// parallel arrays, in the order asked; unknown ids are left out.
func (s *server) historyTracks(w http.ResponseWriter, r *http.Request) {
	if !s.historyStore(w) {
		return
	}
	var ids []int64
	for v := range strings.SplitSeq(r.URL.Query().Get("ids"), ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || id < 1 {
			writeError(w, http.StatusBadRequest, "ids must be a comma-separated list of flight ids")
			return
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) > history.MaxTrackIDs {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("at most %d ids", history.MaxTrackIDs))
		return
	}
	tracks, err := s.History.Tracks(r.Context(), ids)
	if err != nil {
		s.historyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, TracksResponse{Tracks: tracks})
}

// markOnTape sets OnTape on the flights a recorded tape span overlaps.
func (s *server) markOnTape(list []history.Flight) {
	if s.TapeData == nil || len(list) == 0 {
		return
	}
	spans := s.TapeData.Extent().Spans
	for i := range list {
		f := &list[i]
		f.OnTape = slices.ContainsFunc(spans, func(sp tape.Span) bool {
			return !sp.From.After(f.LastSeen) && !sp.To.Before(f.FirstSeen)
		})
	}
}
