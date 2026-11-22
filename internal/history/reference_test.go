package history

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestReference_PutGetDelete(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{Now: func() time.Time { return now }})
	ctx := context.Background()

	if _, err := s.AircraftInfo(ctx, "4840D6"); !errors.Is(err, ErrNoReference) {
		t.Fatalf("missing entry: err %v, want ErrNoReference", err)
	}
	want := AircraftInfo{ICAO: "4840D6", Registration: "PH-BXA", TypeCode: "B738", Operator: "KLM"}
	if _, err := s.PutAircraftInfo(ctx, want); err != nil {
		t.Fatal(err)
	}
	want.Updated = now
	if got, err := s.AircraftInfo(ctx, "4840D6"); err != nil || got != want {
		t.Errorf("AircraftInfo = %+v, %v; want %+v", got, err, want)
	}
	if _, err := s.PutAircraftInfo(ctx, AircraftInfo{ICAO: "4840D6"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AircraftInfo(ctx, "4840D6"); !errors.Is(err, ErrNoReference) {
		t.Errorf("empty put: err %v, want the entry deleted", err)
	}

	r := Route{Callsign: "KLM1023", Origin: "EHAM", Destination: "LEMD"}
	if _, err := s.PutRoute(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Updated = now
	if got, err := s.Route(ctx, "KLM1023"); err != nil || got != r {
		t.Errorf("Route = %+v, %v; want %+v", got, err, r)
	}
	if err := s.DeleteRoute(ctx, "KLM1023"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Route(ctx, "KLM1023"); !errors.Is(err, ErrNoReference) {
		t.Errorf("deleted route: err %v", err)
	}
}
