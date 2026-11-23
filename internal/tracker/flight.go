package tracker

import (
	"cmp"
	"slices"
	"time"
)

const (
	// sampleAltFt and sampleInterval decide when a Mode-S-only aircraft gets a new altitude/squawk sample.
	sampleAltFt    = 100
	sampleInterval = 30 * time.Second

	defaultFlightGap = 10 * time.Minute
)

// FlightInfo describes one flight: a continuous presence of one ICAO address. Times are data time.
type FlightInfo struct {
	ID            int64
	ICAO          string
	Callsign      string
	Squawk        string
	Category      string
	Country       string
	ADSB          bool
	FirstSeen     time.Time
	LastSeen      time.Time
	Closed        bool
	Points        int
	MaxAlt        *int
	MinDistanceNm *float64
}

// Sample is one altitude/squawk record of a Mode-S-only aircraft; T is unix ms in data time.
type Sample struct {
	T      int64
	Alt    int // ft, valid with HasAlt
	HasAlt bool
	Squawk string
}

// due reports whether s is recorded after prev; a changed sample at the same time replaces prev.
func (s Sample) due(prev Sample) bool {
	switch {
	case s.T < prev.T:
		return false
	case s.T == prev.T:
		return s != prev
	case s.Squawk != prev.Squawk, s.HasAlt != prev.HasAlt:
		return true
	}
	return abs(s.Alt-prev.Alt) >= sampleAltFt || s.T-prev.T >= sampleInterval.Milliseconds()
}

// FlightSink receives flight events from the tracker's writer goroutine, under its lock: implementations must
// not block. Flight is sent when a flight opens, when its callsign, squawk, category or ADS-B flag changes,
// and when it closes (Closed set). A Sample with the time of the previous one replaces it. Quality is sent when an
// ADS-B flight's position quality changes, and first when it becomes known on the flight.
type FlightSink interface {
	Flight(f FlightInfo)
	Point(flightID int64, p TrackPoint)
	Sample(flightID int64, s Sample)
	Quality(flightID int64, q QualityChange)
}

// ResumedFlight is a flight the previous run left open, with its stored trail (oldest first).
type ResumedFlight struct {
	Info  FlightInfo
	Trail []TrackPoint
}

type flight struct {
	info    FlightInfo
	trail   []TrackPoint
	sample  Sample
	sampled bool
	quality Quality // the last one sent
}

// flightFor returns the open flight of icao at data time at, closing one that ended more than TTL+FlightGap ago
// and opening a new one when needed.
func (t *Tracker) flightFor(icao uint32, a *Aircraft, at time.Time) *flight {
	f := t.flights[icao]
	if f != nil && at.Sub(f.info.LastSeen) > t.opt.TTL+t.opt.FlightGap {
		t.closeFlight(icao, f)
		f = nil
	}
	if f == nil {
		f = t.openFlight(icao, a, at, nil)
	}
	if at.After(f.info.LastSeen) {
		f.info.LastSeen = at
	}
	return f
}

// openFlight starts a flight for icao at data time at, with the aircraft's current identity and an inherited trail.
func (t *Tracker) openFlight(icao uint32, a *Aircraft, at time.Time, trail []TrackPoint) *flight {
	t.nextFlightID++
	f := &flight{
		info: FlightInfo{
			ID: t.nextFlightID, ICAO: a.ICAO, Callsign: a.Callsign, Squawk: a.Squawk, Category: a.Category,
			Country: a.Country, ADSB: a.ADSB, FirstSeen: at, LastSeen: at,
		},
		trail: trail,
	}
	t.flights[icao] = f
	t.opt.Logger.Debug("flight opened", "icao", a.ICAO, "flight", f.info.ID, "callsign", a.Callsign)
	t.emitFlight(f)
	return f
}

func (t *Tracker) closeFlight(icao uint32, f *flight) {
	f.info.Closed = true
	delete(t.flights, icao)
	t.opt.Logger.Debug("flight closed", "icao", f.info.ICAO, "flight", f.info.ID, "callsign", f.info.Callsign, "points", f.info.Points)
	t.emitFlight(f)
}

func (t *Tracker) emitFlight(f *flight) {
	if t.opt.Flights != nil {
		t.opt.Flights.Flight(f.info)
	}
}

// syncFlight applies the aircraft's identity to its flight after a message at data time at: a different non-empty
// callsign splits the flight (the trail carries over), other changes are announced, and Mode-S-only aircraft
// get altitude/squawk samples.
func (t *Tracker) syncFlight(icao uint32, e *entry, at time.Time) {
	a, f := &e.a, e.fl
	changed := false
	if a.Callsign != "" && a.Callsign != f.info.Callsign {
		if f.info.Callsign != "" {
			t.closeFlight(icao, f)
			f = t.openFlight(icao, a, at, f.trail)
			e.fl = f
		} else {
			f.info.Callsign, changed = a.Callsign, true
		}
	}
	if a.Squawk != "" && a.Squawk != f.info.Squawk {
		f.info.Squawk, changed = a.Squawk, true
	}
	if a.Category != "" && a.Category != f.info.Category {
		f.info.Category, changed = a.Category, true
	}
	if a.ADSB && !f.info.ADSB {
		f.info.ADSB, changed = true, true
	}
	if changed {
		t.emitFlight(f)
	}

	if a.ADSB {
		if q := a.PositionQuality; q != "" && q != f.quality {
			f.quality = q
			if t.opt.Flights != nil {
				t.opt.Flights.Quality(f.info.ID, QualityChange{T: at.UnixMilli(), Quality: q})
			}
		}
		return
	}
	s := Sample{T: at.UnixMilli(), Squawk: a.Squawk}
	if a.AltBaro != nil {
		s.Alt, s.HasAlt = *a.AltBaro, true
		t.noteAltitude(f, s.Alt)
	}
	if (!s.HasAlt && s.Squawk == "") || (f.sampled && !s.due(f.sample)) {
		return
	}
	f.sample, f.sampled = s, true
	if t.opt.Flights != nil {
		t.opt.Flights.Sample(f.info.ID, s)
	}
}

// addTrackPoint appends an accepted position to the aircraft's trail if thinning keeps it.
func (t *Tracker) addTrackPoint(e *entry, pos Position) {
	f := e.fl
	p := newTrackPoint(&e.a, pos)
	if n := len(f.trail); n > 0 && !p.keep(f.trail[n-1], t.opt.TrailMinInterval) {
		return
	}
	f.trail = trimTrail(append(f.trail, p), t.opt.TrailWindow)
	f.info.Points++
	if p.AltSrc != AltUnknown {
		t.noteAltitude(f, p.Alt)
	}
	if d := e.a.DistanceNm; d != nil && (f.info.MinDistanceNm == nil || *d < *f.info.MinDistanceNm) {
		f.info.MinDistanceNm = d
	}
	if t.opt.Flights != nil {
		t.opt.Flights.Point(f.info.ID, p)
	}
}

func (t *Tracker) noteAltitude(f *flight, alt int) {
	if f.info.MaxAlt == nil || alt > *f.info.MaxAlt {
		f.info.MaxAlt = new(alt)
	}
}

// expireFlights closes flights not heard for longer than TTL+FlightGap at data time now.
func (t *Tracker) expireFlights(now time.Time) {
	for icao, f := range t.flights {
		if now.Sub(f.info.LastSeen) > t.opt.TTL+t.opt.FlightGap {
			t.closeFlight(icao, f)
		}
	}
}

// CloseFlights closes every open flight. Aircraft heard again start new flights.
func (t *Tracker) CloseFlights() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for icao, f := range t.flights {
		t.closeFlight(icao, f)
	}
}

// SyncFlights sends every open flight to the sink without closing it, for a shutdown after which the next run
// resumes them (Options.Resume).
func (t *Tracker) SyncFlights() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, f := range t.flights {
		t.emitFlight(f)
	}
}

// Trails returns copies of the trails of all aircraft heard within TTL, by ICAO address.
func (t *Tracker) Trails() map[string][]TrackPoint {
	return t.TrailPointsSince(map[string]int64{})
}

// TrailPointsSince returns copies of the trail points newer than sent[icao] (unix ms) of all aircraft heard within
// TTL, by ICAO address, and records the newest returned point in sent. Aircraft no longer heard are removed from
// sent, so their whole trail is returned again when they reappear.
func (t *Tracker) TrailPointsSince(sent map[string]int64) map[string][]TrackPoint {
	t.mu.RLock()
	defer t.mu.RUnlock()
	now := t.nowLocked()
	out := map[string][]TrackPoint{}
	live := make(map[string]bool, len(t.aircraft))
	for _, e := range t.aircraft {
		if now.Sub(e.a.LastSeen) > t.opt.TTL || e.fl == nil {
			continue
		}
		live[e.a.ICAO] = true
		trail := e.fl.trail
		last, ok := sent[e.a.ICAO]
		i := 0
		if ok {
			i, _ = slices.BinarySearchFunc(trail, last+1, func(p TrackPoint, t int64) int { return cmp.Compare(p.T, t) })
		}
		if i < len(trail) {
			out[e.a.ICAO] = slices.Clone(trail[i:])
			sent[e.a.ICAO] = trail[len(trail)-1].T
		}
	}
	for icao := range sent {
		if !live[icao] {
			delete(sent, icao)
		}
	}
	return out
}

// TTL returns how long an aircraft stays in the table without messages.
func (t *Tracker) TTL() time.Duration { return t.opt.TTL }

// TrailWindow returns the length of the in-memory trails (data time).
func (t *Tracker) TrailWindow() time.Duration { return t.opt.TrailWindow }

// Flights returns the open flights, by ICAO address.
func (t *Tracker) Flights() map[string]FlightInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]FlightInfo, len(t.flights))
	for _, f := range t.flights {
		out[f.info.ICAO] = f.info
	}
	return out
}
