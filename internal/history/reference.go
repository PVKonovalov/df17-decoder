package history

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNoReference is returned when no reference entry exists for the key.
var ErrNoReference = errors.New("history: no reference entry")

// AircraftInfo is what the user entered about an airframe, keyed by its ICAO address.
type AircraftInfo struct {
	ICAO         string    `json:"icao"`
	Registration string    `json:"registration,omitempty"`
	TypeCode     string    `json:"typeCode,omitempty"`
	TypeName     string    `json:"typeName,omitempty"`
	Operator     string    `json:"operator,omitempty"`
	Updated      time.Time `json:"updated"`
}

func (a AircraftInfo) empty() bool {
	return a.Registration == "" && a.TypeCode == "" && a.TypeName == "" && a.Operator == ""
}

// Route is what the user entered about a flight number, keyed by its callsign.
type Route struct {
	Callsign    string    `json:"callsign"`
	Origin      string    `json:"origin,omitempty"`
	Destination string    `json:"destination,omitempty"`
	Updated     time.Time `json:"updated"`
}

func (r Route) empty() bool { return r.Origin == "" && r.Destination == "" }

// AircraftInfo returns the entry for icao, or ErrNoReference.
func (s *Store) AircraftInfo(ctx context.Context, icao string) (AircraftInfo, error) {
	a := AircraftInfo{ICAO: icao}
	var reg, code, name, op sql.NullString
	var updated int64
	err := s.ro.QueryRowContext(ctx, `SELECT registration, type_code, type_name, operator, updated
		FROM aircraft_info WHERE icao = ?`, icao).Scan(&reg, &code, &name, &op, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNoReference
	}
	a.Registration, a.TypeCode, a.TypeName, a.Operator = reg.String, code.String, name.String, op.String
	a.Updated = time.UnixMilli(updated).UTC()
	return a, err
}

// PutAircraftInfo stores a (Updated is set to now) and returns it; an entry without any field is deleted instead.
func (s *Store) PutAircraftInfo(ctx context.Context, a AircraftInfo) (AircraftInfo, error) {
	if a.empty() {
		return a, s.DeleteAircraftInfo(ctx, a.ICAO)
	}
	a.Updated = s.opt.Now().UTC().Truncate(time.Millisecond)
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO aircraft_info(icao, registration, type_code, type_name,
		operator, updated) VALUES(?, ?, ?, ?, ?, ?)`, a.ICAO, nullString(a.Registration), nullString(a.TypeCode),
		nullString(a.TypeName), nullString(a.Operator), a.Updated.UnixMilli())
	return a, err
}

// DeleteAircraftInfo removes the entry for icao; a missing one is not an error.
func (s *Store) DeleteAircraftInfo(ctx context.Context, icao string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM aircraft_info WHERE icao = ?`, icao)
	return err
}

// Route returns the entry for callsign, or ErrNoReference.
func (s *Store) Route(ctx context.Context, callsign string) (Route, error) {
	r := Route{Callsign: callsign}
	var origin, dest sql.NullString
	var updated int64
	err := s.ro.QueryRowContext(ctx, `SELECT origin, destination, updated FROM routes WHERE callsign = ?`, callsign).
		Scan(&origin, &dest, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNoReference
	}
	r.Origin, r.Destination, r.Updated = origin.String, dest.String, time.UnixMilli(updated).UTC()
	return r, err
}

// PutRoute stores r (Updated is set to now) and returns it; an entry without any field is deleted instead.
func (s *Store) PutRoute(ctx context.Context, r Route) (Route, error) {
	if r.empty() {
		return r, s.DeleteRoute(ctx, r.Callsign)
	}
	r.Updated = s.opt.Now().UTC().Truncate(time.Millisecond)
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO routes(callsign, origin, destination, updated)
		VALUES(?, ?, ?, ?)`, r.Callsign, nullString(r.Origin), nullString(r.Destination), r.Updated.UnixMilli())
	return r, err
}

// DeleteRoute removes the entry for callsign; a missing one is not an error.
func (s *Store) DeleteRoute(ctx context.Context, callsign string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM routes WHERE callsign = ?`, callsign)
	return err
}
