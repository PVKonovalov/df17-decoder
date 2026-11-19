package main

import (
	"bufio"
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/stats"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

func decode(t *testing.T, msg []byte) modes.Message {
	t.Helper()
	m, err := modes.Decode(msg, time.Time{}, modes.Options{})
	if err != nil {
		t.Fatalf("decode %X: %v", msg, err)
	}
	return m
}

func TestEncoders_RoundTrip(t *testing.T) {
	const icao = 0x738ABC

	if m := decode(t, identification(icao, "ELY001")); m.ICAO != icao || m.Identification.Callsign != "ELY001" {
		t.Errorf("identification: %+v", m.Identification)
	}

	m := decode(t, airbornePosition(icao, 25.08, 22.81, 12000, true))
	if p := m.AirbornePosition; p == nil || p.Altitude == nil || *p.Altitude != 12000 || p.CPR != modes.EncodeCPR(25.08, 22.81, true) {
		t.Errorf("position: %+v", p)
	}
	lat, lon := modes.CPRLocal(m.AirbornePosition.CPR, 25, 23)
	if math.Abs(lat-25.08) > 1e-3 || math.Abs(lon-22.81) > 1e-3 {
		t.Errorf("CPR local = %f, %f", lat, lon)
	}

	v := decode(t, velocity(icao, 300, 225, -1280)).Velocity
	if v == nil || math.Abs(*v.GroundSpeed-300) > 1 || math.Abs(*v.Track-225) > 0.5 || *v.VerticalRate != -1280 {
		t.Errorf("velocity: gs=%v trk=%v vr=%v", *v.GroundSpeed, *v.Track, *v.VerticalRate)
	}

	if s := decode(t, emergencyStatus(icao, 1, "7700")).AircraftStatus; s == nil || s.Squawk != "7700" || s.Emergency.String() != "general" {
		t.Errorf("status: %+v", s)
	}

	ts := decode(t, targetState(icao, 35000, 271)).TargetState
	if ts == nil || math.Abs(float64(*ts.SelectedAltitude-35000)) > 16 || ts.SelectedAltitudeFMS || math.Abs(*ts.BaroSetting-1013.6) > 1e-9 ||
		math.Abs(*ts.SelectedHeading-271) > 0.36 || ts.Modes == nil || !ts.Modes.Autopilot || !ts.Modes.VNAV || !ts.Modes.LNAV || !ts.Modes.TCAS ||
		ts.Modes.AltitudeHold || ts.Modes.Approach {
		t.Errorf("target state: %+v %+v", ts, ts.Modes)
	}
	for _, c := range []struct{ alt, vr, want float64 }{{10200, 1500, 14000}, {39500, 2000, 41000}, {6300, -1200, 2000}, {20000, -800, 16000}, {35040, 0, 35000}} {
		if got := (&aircraft{alt: c.alt, vr: c.vr}).selectedAltitude(); got != c.want {
			t.Errorf("selectedAltitude(alt %v, vr %v) = %v, want %v", c.alt, c.vr, got, c.want)
		}
	}

	if m := decode(t, allCall(icao)); m.DF != 11 || m.ICAO != icao {
		t.Errorf("all-call: DF%d %06X", m.DF, m.ICAO)
	}
	if m := decode(t, altitudeReply(icao, 3500)); m.DF != 4 || m.ICAO != icao || *m.Surveillance.Altitude != 3500 {
		t.Errorf("DF4: %06X %+v", m.ICAO, m.Surveillance)
	}
	for _, sq := range []string{"1200", "7700", "0243", "7777", "0001"} {
		if m := decode(t, identityReply(icao, sq)); m.DF != 5 || m.ICAO != icao || m.Surveillance.Squawk != sq {
			t.Errorf("DF5 %s: %06X %+v", sq, m.ICAO, m.Surveillance)
		}
	}
}

func TestGenerate(t *testing.T) {
	o := options{lat: 25.07, lon: 22.87, count: 40, modeSOnly: 0.25, duration: 30, step: 0.5, maxRangeNm: 150, seed: 7, emergencySqk: "7700"}
	var buf bytes.Buffer
	n, err := generate(&buf, o)
	if err != nil {
		t.Fatal(err)
	}

	icaos := map[uint32]bool{}
	dfs := map[int]int{}
	var last uint64
	sc := bufio.NewScanner(&buf)
	lines := 0
	for sc.Scan() {
		data, ts, err := source.ParseAVR(sc.Bytes())
		if err != nil {
			t.Fatalf("line %d: %v", lines+1, err)
		}
		if ts <= last {
			t.Fatalf("line %d: timestamp %d not after %d", lines+1, ts, last)
		}
		last = ts
		m := decode(t, data)
		dfs[m.DF]++
		icaos[m.ICAO] = true
		lines++
	}
	if lines != n || len(icaos) != o.count {
		t.Errorf("lines %d (reported %d), aircraft %d", lines, n, len(icaos))
	}
	for _, df := range []int{4, 5, 11, 17} {
		if dfs[df] == 0 {
			t.Errorf("no DF%d frames: %v", df, dfs)
		}
	}
}

func TestMove_StaysInRange(t *testing.T) {
	o := options{lat: 25.07, lon: 22.87, maxRangeNm: 50, step: 0.5}
	a := &aircraft{lat: 25.07, lon: 22.87, gs: 450, track: 90, alt: 40000, vr: 3000}
	for range 7200 { // one hour
		a.move(o, o.step)
		dLat := (a.lat - o.lat) * 60
		dLon := (a.lon - o.lon) * 60 * math.Cos(o.lat*math.Pi/180)
		if d := math.Hypot(dLat, dLon); d > o.maxRangeNm*1.1 {
			t.Fatalf("drifted to %.1f nm", d)
		}
		if a.alt > 42000 || a.alt < 0 {
			t.Fatalf("altitude %.0f", a.alt)
		}
	}
}

func TestGenerate_ArrivalsAndDepartures(t *testing.T) {
	o := options{lat: 25.07, lon: 22.87, count: 200, modeSOnly: 0.15, duration: 600, step: 0.5, maxRangeNm: 150, seed: 3,
		emergencySqk: "7700", arrivals: 0.4, departures: 0.3}
	var buf bytes.Buffer
	if _, err := generate(&buf, o); err != nil {
		t.Fatal(err)
	}
	first, last := map[uint32]uint64{}, map[uint32]uint64{}
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		data, ts, err := source.ParseAVR(sc.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		m := decode(t, data)
		if _, ok := first[m.ICAO]; !ok {
			first[m.ICAO] = ts
		}
		last[m.ICAO] = ts
	}
	const sec = clockHz
	end := uint64(o.duration+1) * sec
	var atStart, arrived, departed int
	for icao, f := range first {
		switch {
		case f < 5*sec:
			atStart++
		case f > 60*sec:
			arrived++
		}
		if last[icao] < end-60*sec {
			departed++
		}
	}
	if len(first) != o.count || atStart < 90 || atStart > 150 || arrived < 50 || departed < 30 {
		t.Errorf("aircraft %d, at start %d, arrived after 60 s %d, departed 60 s before end %d", len(first), atStart, arrived, departed)
	}
}

func TestApplyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("receiver:\n  lat: 25.07\n  lon: 22.87\n  maxRangeNm: 100\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	o := options{lat: 25.07, lon: 22.87, maxRangeNm: 150}
	if err := applyConfig(&o, path, nil); err != nil {
		t.Fatal(err)
	}
	if o.lat != 25.07 || o.lon != 22.87 || o.maxRangeNm != 90 {
		t.Errorf("from config: %+v", o)
	}

	o = options{lat: 1, lon: 2, maxRangeNm: 150}
	if err := applyConfig(&o, path, map[string]bool{"lat": true, "range": true}); err != nil {
		t.Fatal(err)
	}
	if o.lat != 1 || o.lon != 22.87 || o.maxRangeNm != 150 {
		t.Errorf("explicit flags must win: %+v", o)
	}

	if err := applyConfig(&o, filepath.Join(t.TempDir(), "missing.yaml"), nil); err == nil {
		t.Error("missing config: expected error")
	}
}

func TestSurfaceEncoder_RoundTrip(t *testing.T) {
	m := decode(t, surfacePosition(0x738ABC, 25.12, 22.93, 15, 271, true))
	p := m.SurfacePosition
	if p == nil || m.TC != 6 || m.OnGround == nil || !*m.OnGround || p.CPR != modes.EncodeSurfaceCPR(25.12, 22.93, true) {
		t.Fatalf("surface: tc=%d %+v onGround=%v", m.TC, p, m.OnGround)
	}
	if math.Abs(*p.GroundSpeed-15) > 0.5 || math.Abs(*p.Track-271) > 360.0/128 {
		t.Errorf("gs=%v track=%v", *p.GroundSpeed, *p.Track)
	}
	if m := decode(t, onGround(identification(0x738ABC, "ELY1"))); m.OnGround == nil || !*m.OnGround || m.Identification.Callsign != "ELY1" {
		t.Errorf("ground identification: %+v", m)
	}
	for _, kt := range []float64{0, 0.125, 0.5, 1, 1.75, 2, 14.5, 15, 19, 69, 70, 98, 100, 170, 175, 300} {
		if got := modes.Movement(int(movement(kt))); got == nil || math.Abs(*got-min(kt, 175)) > 0.07 {
			t.Errorf("movement(%v) decodes to %v", kt, got)
		}
	}
}

func TestGenerate_Taxiing(t *testing.T) {
	o := options{lat: 25.07, lon: 22.87, count: 5, duration: 120, step: 0.5, maxRangeNm: 150, seed: 5, taxiing: 4}
	var buf bytes.Buffer
	if _, err := generate(&buf, o); err != nil {
		t.Fatal(err)
	}
	tr := tracker.New(tracker.Options{
		TTL:             time.Minute,
		KnownAddressTTL: time.Minute,
		Resolver: tracker.PositionResolver{Receiver: geo.Point{Lat: o.lat, Lon: o.lon}, MaxRangeNm: 300,
			PairMaxAge: 10 * time.Second, RefMaxAge: time.Minute},
		Stats: stats.New(time.Time{}),
	})
	t0 := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		data, ts, err := source.ParseAVR(sc.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		m := decode(t, data)
		m.Time = t0.Add(time.Duration(ts) * time.Second / clockHz)
		tr.Update(m, nil)
	}
	ground := 0
	for _, a := range tr.Snapshot() {
		if a.OnGround == nil || !*a.OnGround {
			continue
		}
		ground++
		if !a.HasPosition || *a.DistanceNm < 4 || *a.DistanceNm > 8 || a.GroundSpeed == nil || *a.GroundSpeed > 25 || a.AltBaro != nil {
			t.Errorf("%s: pos=%v dist=%v gs=%v alt=%v", a.ICAO, a.HasPosition, a.DistanceNm, a.GroundSpeed, a.AltBaro)
		}
	}
	if ground != o.taxiing {
		t.Errorf("on ground: %d, want %d", ground, o.taxiing)
	}
}
