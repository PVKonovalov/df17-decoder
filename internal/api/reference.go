package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/PVKonovalov/df17-decoder/internal/history"
)

const (
	maxReferenceBody  = 4 << 10
	maxReferenceField = 64
)

var callsignRe = regexp.MustCompile(`^[A-Z0-9]{1,8}$`)

// referenceKey reads the {icao} or {callsign} path value, upper-cased, and writes a 400 when it is invalid.
func (s *server) referenceKey(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	v := strings.ToUpper(r.PathValue(name))
	switch {
	case name == "icao" && !validICAO(v):
		writeError(w, http.StatusBadRequest, "icao must be 6 hex digits")
	case name == "callsign" && !callsignRe.MatchString(v):
		writeError(w, http.StatusBadRequest, "callsign must be 1–8 letters or digits")
	default:
		return v, s.historyStore(w)
	}
	return "", false
}

// decodeReference reads a JSON object of string fields into dst (at most 4 KiB, no unknown fields), trims every
// field of fields and writes a 400 when the body or a field is invalid.
func decodeReference(w http.ResponseWriter, r *http.Request, dst any, fields map[string]*string) bool {
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReferenceBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return false
	}
	var errs []error
	for name, f := range fields {
		*f = strings.TrimSpace(*f)
		if utf8.RuneCountInString(*f) > maxReferenceField {
			errs = append(errs, fmt.Errorf("%s must be at most %d characters", name, maxReferenceField))
		}
	}
	if len(errs) > 0 {
		writeError(w, http.StatusBadRequest, errors.Join(errs...).Error())
		return false
	}
	return true
}

// writeReference answers a reference read or write: 404 for no entry, 204 for a deletion (nothing to return).
func (s *server) writeReference(w http.ResponseWriter, v any, deleted bool, err error) {
	switch {
	case errors.Is(err, history.ErrNoReference):
		writeError(w, http.StatusNotFound, "no reference entry")
	case err != nil:
		s.Logger.Error("reference", "err", err)
		writeError(w, http.StatusInternalServerError, "reference data failed")
	case deleted:
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusOK, v)
	}
}

// aircraftInfo serves GET /api/reference/aircraft/{icao}: the user's entry for the airframe, 404 when none.
func (s *server) aircraftInfo(w http.ResponseWriter, r *http.Request) {
	if icao, ok := s.referenceKey(w, r, "icao"); ok {
		a, err := s.History.AircraftInfo(r.Context(), icao)
		s.writeReference(w, a, false, err)
	}
}

// putAircraftInfo serves PUT /api/reference/aircraft/{icao} with {registration, typeCode, typeName, operator}:
// 200 with the stored entry, or 204 when every field is empty (the entry is deleted).
func (s *server) putAircraftInfo(w http.ResponseWriter, r *http.Request) {
	icao, ok := s.referenceKey(w, r, "icao")
	if !ok {
		return
	}
	var a history.AircraftInfo
	if !decodeReference(w, r, &a, map[string]*string{"registration": &a.Registration, "typeCode": &a.TypeCode,
		"typeName": &a.TypeName, "operator": &a.Operator}) {
		return
	}
	a.ICAO = icao
	a.TypeCode = strings.ToUpper(a.TypeCode)
	a.Registration = strings.ToUpper(a.Registration)
	a, err := s.History.PutAircraftInfo(r.Context(), a)
	s.writeReference(w, a, a.Updated.IsZero(), err)
}

// deleteAircraftInfo serves DELETE /api/reference/aircraft/{icao}: 204, also when there was no entry.
func (s *server) deleteAircraftInfo(w http.ResponseWriter, r *http.Request) {
	if icao, ok := s.referenceKey(w, r, "icao"); ok {
		s.writeReference(w, nil, true, s.History.DeleteAircraftInfo(r.Context(), icao))
	}
}

// route serves GET /api/reference/routes/{callsign}: the user's entry for the flight number, 404 when none.
func (s *server) route(w http.ResponseWriter, r *http.Request) {
	if cs, ok := s.referenceKey(w, r, "callsign"); ok {
		rt, err := s.History.Route(r.Context(), cs)
		s.writeReference(w, rt, false, err)
	}
}

// putRoute serves PUT /api/reference/routes/{callsign} with {origin, destination}: 200 with the stored entry, or
// 204 when both are empty (the entry is deleted).
func (s *server) putRoute(w http.ResponseWriter, r *http.Request) {
	cs, ok := s.referenceKey(w, r, "callsign")
	if !ok {
		return
	}
	var rt history.Route
	if !decodeReference(w, r, &rt, map[string]*string{"origin": &rt.Origin, "destination": &rt.Destination}) {
		return
	}
	rt.Callsign = cs
	rt, err := s.History.PutRoute(r.Context(), rt)
	s.writeReference(w, rt, rt.Updated.IsZero(), err)
}

// deleteRoute serves DELETE /api/reference/routes/{callsign}: 204, also when there was no entry.
func (s *server) deleteRoute(w http.ResponseWriter, r *http.Request) {
	if cs, ok := s.referenceKey(w, r, "callsign"); ok {
		s.writeReference(w, nil, true, s.History.DeleteRoute(r.Context(), cs))
	}
}
