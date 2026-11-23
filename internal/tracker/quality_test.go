package tracker

import (
	"slices"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/modes"
)

func opStatus(at time.Time, nacp int) modes.Message {
	return modes.Message{DF: 17, ICAO: testICAO, TC: 31, Time: at,
		OperationalStatus: &modes.OperationalStatus{Version: 2, NACp: new(nacp), SIL: new(3)}}
}

func noPosMsg(at time.Time) modes.Message {
	return modes.Message{DF: 17, ICAO: testICAO, Time: at, NoPosition: &modes.NoPosition{Altitude: new(36000)}}
}

func TestQuality(t *testing.T) {
	sink := newFakeSink()
	tr, _ := newTrailTracker(t, 0, sink)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	pos := func(s int) modes.Message {
		m := posMsg(at(s), 52.1, 4.5, 30000, s%2 == 1)
		m.TC = 11
		return m
	}
	quality := func() (Quality, Aircraft) {
		a, _ := tr.Get(testICAO)
		return a.PositionQuality, a
	}

	tr.Update(velMsg(at(0), 0), nil)
	if q, _ := quality(); q != "" {
		t.Errorf("before a position: %q", q)
	}
	tr.Update(pos(1), nil)
	if q, a := quality(); q != QualityOK || !ptrEqual(a.NIC, 8) {
		t.Errorf("TC 11: %q nic %v", q, a.NIC)
	}
	tr.Update(opStatus(at(2), 9), nil)
	tr.Update(opStatus(at(3), 4), nil)
	if q, a := quality(); q != QualityDegraded || !ptrEqual(a.NACp, 4) {
		t.Errorf("NACp 4: %q", q)
	}
	tr.Update(noPosMsg(at(4)), nil)
	if q, a := quality(); q != QualityLost || !ptrEqual(a.AltBaro, 36000) {
		t.Errorf("TC 0: %q alt %v", q, a.AltBaro)
	}
	tr.Update(opStatus(at(5), 10), nil)
	tr.Update(pos(6), nil)
	if q, _ := quality(); q != QualityOK {
		t.Errorf("position again: %q", q)
	}
	tr.Update(velMsg(at(6+30), 0), nil)
	if q, _ := quality(); q != QualityOK {
		t.Errorf("30 s without a position: %q", q)
	}
	tr.Update(velMsg(at(6+31), 0), nil)
	if q, _ := quality(); q != QualityLost {
		t.Errorf("31 s without a position: %q", q)
	}
	if n := tr.opt.Stats.NoPosition.Load(); n != 1 {
		t.Errorf("no position messages %d", n)
	}

	var got []Quality
	for _, c := range sink.quality[1] {
		got = append(got, c.Quality)
	}
	want := []Quality{QualityOK, QualityDegraded, QualityLost, QualityOK, QualityLost}
	if !slices.Equal(got, want) {
		t.Errorf("flight quality changes %v, want %v", got, want)
	}

	// a new flight (callsign change) starts with the current quality
	tr.Update(identMsg(at(40), "KLM1"), nil)
	tr.Update(identMsg(at(41), "KLM2"), nil)
	if c := sink.quality[2]; len(c) != 1 || c[0].Quality != QualityLost || c[0].T != at(41).UnixMilli() {
		t.Errorf("new flight quality %+v", c)
	}
}

func TestQuality_ModeSOnlyHasNone(t *testing.T) {
	tr, _, _ := newTestTracker(t)
	feed(t, tr, t0, frameDF11, frameDF4)
	if a, _ := tr.Get(0x738ABC); a.PositionQuality != "" || a.NIC != nil {
		t.Errorf("%+v", a)
	}
}

func ptrEqual[T comparable](p *T, v T) bool { return p != nil && *p == v }
