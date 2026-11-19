package tracker

import (
	"encoding/hex"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
)

var t0 = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

func resolver(lat, lon float64) PositionResolver {
	return PositionResolver{
		Receiver:   geo.Point{Lat: lat, Lon: lon},
		MaxRangeNm: 300,
		PairMaxAge: 10 * time.Second,
		RefMaxAge:  60 * time.Second,
	}
}

func near(p geo.Point, lat, lon float64) bool {
	return math.Abs(p.Lat-lat) < 1e-3 && math.Abs(p.Lon-lon) < 1e-3
}

func vectorFrame(t *testing.T, s string) modes.CPRFrame {
	t.Helper()
	b, _ := hex.DecodeString(s)
	m, err := modes.Decode(b, t0, modes.Options{})
	if err != nil || m.AirbornePosition == nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return m.AirbornePosition.CPR
}

func TestResolve_GlobalVectorNearDelft(t *testing.T) {
	r := resolver(52.0, 4.4)
	var st CPRState

	odd := vectorFrame(t, "8D40621D58C386435CC412692AD6")
	even := vectorFrame(t, "8D40621D58C382D690C8AC2863A7")

	p, err := r.Resolve(&st, odd, t0)
	if err != nil || p.Method != MethodLocalReceiver {
		t.Fatalf("first frame: %+v %v, want local-receiver", p, err)
	}
	p, err = r.Resolve(&st, even, t0.Add(3*time.Second))
	if err != nil || p.Method != MethodGlobal || !near(p.Point, 52.2572, 3.9194) {
		t.Fatalf("pair: %+v %v, want global 52.2572, 3.9194", p, err)
	}
}

func TestResolve_VectorOutOfRangeFromIsrael(t *testing.T) {
	r := resolver(32.09, 34.96)
	var st CPRState
	odd := vectorFrame(t, "8D40621D58C386435CC412692AD6")
	even := vectorFrame(t, "8D40621D58C382D690C8AC2863A7")

	r.Resolve(&st, odd, t0)
	st.last = nil // ignore whatever the receiver-local first fix aliased to
	if _, err := r.Resolve(&st, even, t0.Add(time.Second)); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("err = %v, want ErrOutOfRange", err)
	}
}

func TestResolve_LocalUsesAircraftReference(t *testing.T) {
	r := resolver(32.09, 34.96)
	var st CPRState

	// global fix over LLBG, then a single odd frame 40 s later and ~5 NM further south
	r.Resolve(&st, modes.EncodeCPR(32.01, 34.88, false), t0)
	p, err := r.Resolve(&st, modes.EncodeCPR(32.01, 34.88, true), t0.Add(time.Second))
	if err != nil || p.Method != MethodGlobal {
		t.Fatalf("global: %+v %v", p, err)
	}

	p, err = r.Resolve(&st, modes.EncodeCPR(31.93, 34.85, true), t0.Add(41*time.Second))
	if err != nil || p.Method != MethodLocalAircraft || !near(p.Point, 31.93, 34.85) {
		t.Errorf("local: %+v %v, want local-aircraft 31.93, 34.85", p, err)
	}
}

func TestResolve_StalePairFallsBackToLocal(t *testing.T) {
	r := resolver(32.09, 34.96)
	var st CPRState
	r.Resolve(&st, modes.EncodeCPR(32.5, 34.5, false), t0)
	p, err := r.Resolve(&st, modes.EncodeCPR(32.5, 34.5, true), t0.Add(11*time.Second))
	if err != nil || p.Method != MethodLocalAircraft {
		t.Errorf("%+v %v, want local-aircraft (pair too old)", p, err)
	}
}

func TestResolve_ImpossibleJumpRejected(t *testing.T) {
	r := resolver(32.09, 34.96)
	var st CPRState
	r.Resolve(&st, modes.EncodeCPR(32.0, 34.8, false), t0)
	if _, err := r.Resolve(&st, modes.EncodeCPR(32.0, 34.8, true), t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	good := *st.Last()

	// 2 s later a pair that resolves ~100 NM away: >1500 kt
	r.Resolve(&st, modes.EncodeCPR(33.6, 35.2, false), t0.Add(2*time.Second))
	_, err := r.Resolve(&st, modes.EncodeCPR(33.6, 35.2, true), t0.Add(3*time.Second))
	if !errors.Is(err, ErrImpossibleSpeed) {
		t.Fatalf("err = %v, want ErrImpossibleSpeed", err)
	}
	if st.Last() != nil {
		t.Errorf("rejected global fix must drop the last position, still %+v (was %+v)", st.Last(), good)
	}

	// next global pair is accepted again, so a bad stored position cannot lock the aircraft out
	p, err := r.Resolve(&st, modes.EncodeCPR(33.6, 35.2, false), t0.Add(4*time.Second))
	if err != nil || p.Method != MethodGlobal {
		t.Errorf("recovery: %+v %v", p, err)
	}
}

func TestResolve_ImpossibleLocalJumpKeepsLastPosition(t *testing.T) {
	r := resolver(32.09, 34.96)
	var st CPRState
	last := Position{Point: geo.Point{Lat: 32.0, Lon: 34.8}, Time: t0, Method: MethodGlobal}
	st.last = &last

	// 1 s later a local decode ~60 NM away
	if _, err := r.Resolve(&st, modes.EncodeCPR(33.0, 34.8, true), t0.Add(time.Second)); !errors.Is(err, ErrImpossibleSpeed) {
		t.Fatalf("err = %v, want ErrImpossibleSpeed", err)
	}
	if st.Last() == nil || *st.Last() != last {
		t.Errorf("last position changed to %+v", st.Last())
	}
}

func TestResolve_MaxRange(t *testing.T) {
	r := resolver(32.09, 34.96)
	r.MaxRangeNm = 50
	var st CPRState
	r.Resolve(&st, modes.EncodeCPR(33.5, 34.5, false), t0) // ~88 NM north
	if _, err := r.Resolve(&st, modes.EncodeCPR(33.5, 34.5, true), t0.Add(time.Second)); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("err = %v, want ErrOutOfRange", err)
	}
}

func TestResolve_ReceiverLocalLimitedTo180Nm(t *testing.T) {
	r := resolver(32.09, 34.96)
	var st CPRState
	// one frame at 35.0 N (~175 NM) decodes fine, 35.2 N (~187 NM) aliases or exceeds the local limit
	if p, err := r.Resolve(&st, modes.EncodeCPR(35.0, 34.96, false), t0); err != nil || !near(p.Point, 35.0, 34.96) {
		t.Errorf("175 NM: %+v %v", p, err)
	}
	st = CPRState{}
	if p, err := r.Resolve(&st, modes.EncodeCPR(35.2, 34.96, false), t0); err == nil && near(p.Point, 35.2, 34.96) {
		t.Errorf("187 NM: unexpectedly decoded %+v", p)
	}
}
