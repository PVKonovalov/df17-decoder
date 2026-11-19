package modes

import (
	"errors"
	"slices"
	"testing"
	"time"
)

// The 1090 MHz Riddle, "Target state and status" example.
const vecTargetState = "8DA05629EA21485CBF3F8CADAEEB"

func TestDecode_TargetStateVector(t *testing.T) {
	m, err := Decode(mustHex(t, vecTargetState), time.Time{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ts := m.TargetState
	if m.TC != 29 || ts == nil {
		t.Fatalf("tc=%d target state %+v", m.TC, ts)
	}
	if ts.SelectedAltitude == nil || *ts.SelectedAltitude != 16992 || ts.SelectedAltitudeFMS {
		t.Errorf("selected altitude %v fms=%v, want 16992 MCP", ts.SelectedAltitude, ts.SelectedAltitudeFMS)
	}
	if ts.BaroSetting == nil || !near(*ts.BaroSetting, 1012.8, 1e-9) {
		t.Errorf("baro %v, want 1012.8", ts.BaroSetting)
	}
	if ts.SelectedHeading == nil || !near(*ts.SelectedHeading, 66.796875, 1e-9) {
		t.Errorf("heading %v, want 66.8", ts.SelectedHeading)
	}
	if ts.Modes == nil || !slices.Equal(ts.Modes.Names(), []string{"autopilot", "vnav", "lnav", "tcas"}) {
		t.Errorf("modes %+v", ts.Modes)
	}
}

// targetME builds a TC 29 subtype st ME field.
func targetME(st, fms, alt, baro, hdgOK, hdg, modesOK, ap, vnav, hold, appr, tcas, lnav uint64) uint64 {
	return me([2]uint64{29, 5}, [2]uint64{st, 2}, [2]uint64{0, 1}, [2]uint64{fms, 1}, [2]uint64{alt, 11},
		[2]uint64{baro, 9}, [2]uint64{hdgOK, 1}, [2]uint64{hdg, 9}, [2]uint64{0, 4}, [2]uint64{0, 1}, [2]uint64{0, 2},
		[2]uint64{modesOK, 1}, [2]uint64{ap, 1}, [2]uint64{vnav, 1}, [2]uint64{hold, 1}, [2]uint64{0, 1},
		[2]uint64{appr, 1}, [2]uint64{tcas, 1}, [2]uint64{lnav, 1})
}

func TestDecode_TargetState(t *testing.T) {
	decodeTS := func(meField uint64) (*TargetState, error) {
		m, err := Decode(makeES(0x738062, meField), time.Time{}, Options{})
		return m.TargetState, err
	}

	ts, err := decodeTS(targetME(1, 1, 1001, 267, 1, 256, 1, 1, 0, 1, 1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if *ts.SelectedAltitude != 32000 || !ts.SelectedAltitudeFMS || !near(*ts.BaroSetting, 1012.8, 1e-9) || *ts.SelectedHeading != 180 {
		t.Errorf("values: alt=%v fms=%v baro=%v hdg=%v", *ts.SelectedAltitude, ts.SelectedAltitudeFMS, *ts.BaroSetting, *ts.SelectedHeading)
	}
	if !slices.Equal(ts.Modes.Names(), []string{"autopilot", "althold", "approach"}) {
		t.Errorf("modes %v", ts.Modes.Names())
	}

	ts, err = decodeTS(targetME(1, 0, 0, 0, 0, 100, 0, 1, 1, 1, 1, 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if ts.SelectedAltitude != nil || ts.BaroSetting != nil || ts.SelectedHeading != nil || ts.Modes != nil {
		t.Errorf("no data: %+v", ts)
	}

	ts, err = decodeTS(targetME(1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0))
	if err != nil || ts.Modes == nil || len(ts.Modes.Names()) != 0 || ts.Modes.Names() == nil {
		t.Errorf("modes valid, none engaged: %+v %v", ts, err)
	}

	if _, err := decodeTS(targetME(0, 0, 100, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)); !errors.Is(err, ErrUnsupportedTC) {
		t.Errorf("subtype 0: err = %v", err)
	}
}
