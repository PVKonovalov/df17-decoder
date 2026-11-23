package tracker

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/modes"
)

const testICAO = 0x4840D6

type fakeSink struct {
	flights []FlightInfo
	points  map[int64][]TrackPoint
	samples map[int64][]Sample
	quality map[int64][]QualityChange
}

func newFakeSink() *fakeSink {
	return &fakeSink{points: map[int64][]TrackPoint{}, samples: map[int64][]Sample{}, quality: map[int64][]QualityChange{}}
}

func (s *fakeSink) Flight(f FlightInfo)          { s.flights = append(s.flights, f) }
func (s *fakeSink) Point(id int64, p TrackPoint) { s.points[id] = append(s.points[id], p) }
func (s *fakeSink) Sample(id int64, smp Sample)  { s.samples[id] = append(s.samples[id], smp) }
func (s *fakeSink) Quality(id int64, q QualityChange) {
	s.quality[id] = append(s.quality[id], q)
}
func (s *fakeSink) closed() (ids []int64) {
	for _, f := range s.flights {
		if f.Closed {
			ids = append(ids, f.ID)
		}
	}
	return ids
}

func newTrailTracker(t *testing.T, window time.Duration, sink FlightSink) (*Tracker, *fakeWall) {
	t.Helper()
	w := &fakeWall{t: t0}
	tr := New(Options{
		TTL:             60 * time.Second,
		KnownAddressTTL: 60 * time.Second,
		Resolver:        resolver(52.0, 4.4),
		TrailWindow:     window,
		Flights:         sink,
		Clock:           w.now,
	})
	return tr, w
}

func posMsg(at time.Time, lat, lon float64, alt int, odd bool) modes.Message {
	return modes.Message{DF: 17, ICAO: testICAO, Time: at, OnGround: new(false),
		AirbornePosition: &modes.AirbornePosition{Altitude: new(alt), CPR: modes.EncodeCPR(lat, lon, odd)}}
}

func velMsg(at time.Time, track float64) modes.Message {
	return modes.Message{DF: 17, ICAO: testICAO, Time: at,
		Velocity: &modes.Velocity{Subtype: 1, GroundSpeed: new(400.0), Track: new(track), VerticalRate: new(0)}}
}

func identMsg(at time.Time, callsign string) modes.Message {
	return modes.Message{DF: 17, ICAO: testICAO, Time: at, Identification: &modes.Identification{Callsign: callsign, Category: "A3"}}
}

// fly feeds one position per second for secs seconds from start, heading north; track and alt give the
// track and altitude at second i.
func fly(tr *Tracker, start time.Time, secs int, track func(i int) float64, alt func(i int) int) {
	for i := 0; i <= secs; i++ {
		at := start.Add(time.Duration(i) * time.Second)
		tr.Update(velMsg(at, track(i)), nil)
		tr.Update(posMsg(at, 52.1+float64(i)*0.001, 4.5, alt(i), i%2 == 1), nil)
	}
}

func straight(int) float64 { return 0 }
func level(int) int        { return 30000 }

func trailOf(t *testing.T, tr *Tracker) []TrackPoint {
	t.Helper()
	return tr.Trails()[modes.ICAOString(testICAO)]
}

func TestTrail_Thinning(t *testing.T) {
	cases := []struct {
		name  string
		track func(int) float64
		alt   func(int) int
		want  int
	}{
		{"straight leg keeps one point per minInterval slot", straight, level, 13},
		// slot starts at 0, 5, 10 s plus a point 3° after each: 0, 3, 5, 8, 10, …
		{"turn of 1°/s keeps a point every 3°", func(i int) float64 { return float64(i) }, level, 25},
		// slot starts plus a point 200 ft after each: 0, 2, 4, 5, 7, 9, 10, …
		{"climb of 100 ft/s keeps a point every 200 ft", straight, func(i int) int { return 30000 + 100*i }, 37},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr, _ := newTrailTracker(t, 0, nil)
			fly(tr, t0, 60, c.track, c.alt)
			if got := len(trailOf(t, tr)); got != c.want {
				t.Errorf("points = %d, want %d", got, c.want)
			}
		})
	}
}

func TestTrail_LateStartConverges(t *testing.T) {
	// a straight, level leg: only the interval rule decides, the case where an elapsed-time rule never re-syncs
	turn, climb := straight, level
	full, _ := newTrailTracker(t, time.Minute, nil)
	fly(full, t0, 300, turn, climb)
	late, _ := newTrailTracker(t, time.Minute, nil)
	for i := 97; i <= 300; i++ { // starts mid-slot, with a different last kept point
		at := t0.Add(time.Duration(i) * time.Second)
		late.Update(velMsg(at, turn(i)), nil)
		late.Update(posMsg(at, 52.1+float64(i)*0.001, 4.5, climb(i), i%2 == 1), nil)
	}
	if a, b := trailOf(t, full), trailOf(t, late); !reflect.DeepEqual(a, b) {
		t.Errorf("trails differ within the window:\n full %v\n late %v", a, b)
	}
}

func TestTrail_OnGroundChangeIsKept(t *testing.T) {
	tr, _ := newTrailTracker(t, 0, nil)
	for i, ground := range []bool{false, true, false} {
		m := posMsg(t0.Add(time.Duration(i)*time.Second), 52.1, 4.5+float64(i)*0.0001, 1000, i%2 == 1)
		m.OnGround = new(ground)
		tr.Update(m, nil)
	}
	trail := trailOf(t, tr)
	if len(trail) != 3 || !trail[1].OnGround || trail[2].OnGround || trail[1].AltSrc != AltUnknown || trail[0].AltSrc != AltBaro {
		t.Errorf("trail = %+v", trail)
	}
}

func TestTrail_WindowByDataTime(t *testing.T) {
	sink := newFakeSink()
	tr, _ := newTrailTracker(t, 30*time.Second, sink)
	fly(tr, t0, 120, straight, level)
	trail := trailOf(t, tr)
	if len(trail) != 7 || trail[0].T != t0.Add(90*time.Second).UnixMilli() {
		t.Errorf("trail has %d points from %d", len(trail), trail[0].T)
	}
	if f := tr.Flights()[modes.ICAOString(testICAO)]; f.Points != 25 || len(sink.points[f.ID]) != 25 {
		t.Errorf("flight points = %d, sink %d; want 25 (all kept points, before trimming)", f.Points, len(sink.points[f.ID]))
	}
}

func TestTrail_FastReplayMatchesRealTime(t *testing.T) {
	realTime, rw := newTrailTracker(t, 30*time.Second, nil)
	fast, _ := newTrailTracker(t, 30*time.Second, nil)
	for i := 0; i <= 90; i++ {
		at := t0.Add(time.Duration(i) * time.Second)
		rw.t = at
		for _, tr := range []*Tracker{realTime, fast} {
			tr.Update(velMsg(at, float64(i%20)), nil)
			tr.Update(posMsg(at, 52.1+float64(i)*0.001, 4.5, 30000+50*i, i%2 == 1), nil)
			tr.Expire()
		}
	}
	a, b := realTime.Trails(), fast.Trails()
	if len(a) != 1 || !reflect.DeepEqual(a, b) {
		t.Errorf("real time %v\nfast replay %v", a, b)
	}
}

func TestTrail_IsACopy(t *testing.T) {
	tr, _ := newTrailTracker(t, 0, nil)
	fly(tr, t0, 10, straight, level)
	trailOf(t, tr)[0].Lat = 0
	if trailOf(t, tr)[0].Lat == 0 {
		t.Error("Trails aliases tracker state")
	}
}

func TestFlight_ContinuesWithinGap(t *testing.T) {
	sink := newFakeSink()
	tr, w := newTrailTracker(t, 0, sink)
	fly(tr, t0, 10, straight, level)

	w.t = t0.Add(5 * time.Minute)
	if tr.Expire() != 1 || len(tr.Trails()) != 0 {
		t.Fatal("aircraft must expire after TTL")
	}
	fly(tr, w.t, 10, straight, level)

	if len(sink.closed()) != 0 {
		t.Errorf("closed %v, want none", sink.closed())
	}
	if f := tr.Flights()[modes.ICAOString(testICAO)]; f.ID != 1 || f.Points != 6 || !f.FirstSeen.Equal(t0) {
		t.Errorf("flight = %+v, want flight 1 with 6 points", f)
	}
	if n := len(trailOf(t, tr)); n != 6 {
		t.Errorf("trail has %d points, want 6 (it continues)", n)
	}
}

func TestFlight_NewAfterGap(t *testing.T) {
	sink := newFakeSink()
	tr, w := newTrailTracker(t, 0, sink)
	fly(tr, t0, 10, straight, level)

	// heard again after the gap, before Expire ran
	later := t0.Add(10*time.Second + time.Minute + 10*time.Minute + time.Second)
	fly(tr, later, 10, straight, level)
	if got := sink.closed(); !reflect.DeepEqual(got, []int64{1}) {
		t.Errorf("closed %v, want [1]", got)
	}
	if f := tr.Flights()[modes.ICAOString(testICAO)]; f.ID != 2 || f.Points != 3 {
		t.Errorf("flight = %+v, want flight 2 with 3 points", f)
	}

	// silence: Expire closes it after TTL+gap
	w.t = later.Add(10*time.Second + 11*time.Minute)
	tr.Expire()
	if got := sink.closed(); !reflect.DeepEqual(got, []int64{1, 2}) || len(tr.Flights()) != 0 {
		t.Errorf("closed %v, open %v", got, tr.Flights())
	}
}

func TestFlight_CallsignChangeSplits(t *testing.T) {
	sink := newFakeSink()
	tr, _ := newTrailTracker(t, 0, sink)
	tr.Update(identMsg(t0, ""), nil)
	tr.Update(identMsg(t0.Add(time.Second), "KLM1023"), nil)
	fly(tr, t0.Add(2*time.Second), 10, straight, level)
	tr.Update(identMsg(t0.Add(20*time.Second), "KLM1023"), nil)
	if len(sink.closed()) != 0 {
		t.Fatalf("first callsign or a repeat must not split: closed %v", sink.closed())
	}

	tr.Update(identMsg(t0.Add(30*time.Second), "KLM1024"), nil)
	if got := sink.closed(); !reflect.DeepEqual(got, []int64{1}) {
		t.Errorf("closed %v, want [1]", got)
	}
	f := tr.Flights()[modes.ICAOString(testICAO)]
	if f.ID != 2 || f.Callsign != "KLM1024" || !f.FirstSeen.Equal(t0.Add(30*time.Second)) || f.Points != 0 {
		t.Errorf("new flight = %+v", f)
	}
	if last := sink.flights[len(sink.flights)-2]; last.ID != 1 || last.Callsign != "KLM1023" || last.Points != 3 {
		t.Errorf("closed flight = %+v", last)
	}
	if len(trailOf(t, tr)) != 3 {
		t.Error("the live trail must carry over to the new flight")
	}
}

func TestFlight_ModeSOnlySamples(t *testing.T) {
	sink := newFakeSink()
	tr, _ := newTrailTracker(t, 0, sink)
	const icao = 0x738ABC
	reply := func(sec int, s modes.Surveillance) {
		tr.Update(modes.Message{DF: 4, ICAO: icao, Time: t0.Add(time.Duration(sec) * time.Second), Surveillance: &s}, nil)
	}
	reply(0, modes.Surveillance{Altitude: new(3000)})
	reply(1, modes.Surveillance{Altitude: new(3050)})
	reply(2, modes.Surveillance{Altitude: new(3100)})
	reply(3, modes.Surveillance{Squawk: "1234"})
	reply(3, modes.Surveillance{Altitude: new(3120)}) // same time: replaces the previous sample
	reply(3, modes.Surveillance{Altitude: new(3120)}) // unchanged: nothing
	reply(10, modes.Surveillance{Altitude: new(3150)})
	reply(40, modes.Surveillance{Altitude: new(3150)})

	f := tr.Flights()[modes.ICAOString(icao)]
	if f.ADSB || f.Squawk != "1234" || f.MaxAlt == nil || *f.MaxAlt != 3150 {
		t.Errorf("flight = %+v", f)
	}
	want := []Sample{
		{T: t0.UnixMilli(), Alt: 3000, HasAlt: true},
		{T: t0.Add(2 * time.Second).UnixMilli(), Alt: 3100, HasAlt: true},
		{T: t0.Add(3 * time.Second).UnixMilli(), Alt: 3100, HasAlt: true, Squawk: "1234"},
		{T: t0.Add(3 * time.Second).UnixMilli(), Alt: 3120, HasAlt: true, Squawk: "1234"},
		{T: t0.Add(40 * time.Second).UnixMilli(), Alt: 3150, HasAlt: true, Squawk: "1234"},
	}
	if got := sink.samples[f.ID]; !reflect.DeepEqual(got, want) {
		t.Errorf("samples\n got %+v\nwant %+v", got, want)
	}
}

func TestFlight_ResumedAfterRestart(t *testing.T) {
	// the first run flies 10 s and shuts down, leaving its flight open
	first := newFakeSink()
	tr, _ := newTrailTracker(t, 0, first)
	fly(tr, t0, 10, straight, level)
	tr.SyncFlights()
	if len(first.closed()) != 0 {
		t.Fatalf("SyncFlights closed %v", first.closed())
	}
	info := tr.Flights()[modes.ICAOString(testICAO)]
	resume := []ResumedFlight{{Info: info, Trail: trailOf(t, tr)}}

	start := func(at time.Time) (*Tracker, *fakeSink) {
		sink := newFakeSink()
		w := &fakeWall{t: at}
		return New(Options{TTL: 60 * time.Second, KnownAddressTTL: 60 * time.Second, Resolver: resolver(52.0, 4.4),
			Flights: sink, Clock: w.now, LastFlightID: 7, Resume: resume}), sink
	}

	// heard again 3 minutes later: the same flight continues, with the stored trail
	next, sink := start(t0.Add(3 * time.Minute))
	fly(next, t0.Add(3*time.Minute), 10, straight, level)
	if f := next.Flights()[modes.ICAOString(testICAO)]; f.ID != info.ID || !f.FirstSeen.Equal(t0) || f.Points != info.Points+3 {
		t.Errorf("resumed flight %+v, want flight %d continued", f, info.ID)
	}
	if n := len(trailOf(t, next)); n != len(resume[0].Trail)+3 {
		t.Errorf("trail has %d points, want the stored %d plus 3", n, len(resume[0].Trail))
	}
	if len(sink.closed()) != 0 {
		t.Errorf("closed %v", sink.closed())
	}

	// heard only after the gap: the resumed flight closes and a new one opens
	late, sink := start(t0.Add(time.Hour))
	fly(late, t0.Add(time.Hour), 2, straight, level)
	if f := late.Flights()[modes.ICAOString(testICAO)]; f.ID != 8 || !slices.Equal(sink.closed(), []int64{info.ID}) {
		t.Errorf("flight %+v, closed %v", f, sink.closed())
	}
}
