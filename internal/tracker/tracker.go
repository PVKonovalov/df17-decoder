package tracker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/stats"
)

type Options struct {
	TTL             time.Duration // forget an aircraft after this long without messages
	KnownAddressTTL time.Duration
	Resolver        PositionResolver
	Logger          *slog.Logger
	Stats           *stats.Stats
	Coverage        CoverageRecorder // optional; receives every accepted position

	TrailWindow      time.Duration // trail length kept in memory, data time; 0 = 10m
	TrailMinInterval time.Duration // minimum time between kept points on a straight, level leg; 0 = 5s
	FlightGap        time.Duration // a flight stays open this long after its aircraft expires; 0 = 10m
	Flights          FlightSink    // optional; receives flights, track points and samples
	LastFlightID     int64         // flight IDs continue after this one
}

// CoverageRecorder records accepted positions (altitude in ft and frame signal in dBFS, both may be nil) at
// data time t.
type CoverageRecorder interface {
	Add(p geo.Point, alt *int, rssi *float64, t time.Time)
}

type entry struct {
	a              Aircraft
	cpr            CPRState
	altitudeLogged bool
	velocityTime   time.Time           // data time of the last ADS-B velocity (TC 19)
	commBTime      time.Time           // data time of the last accepted Comm-B 5,0 or 6,0
	signal         [rssiWindow]float64 // linear power of the last frames, ring buffer
	signalN        int                 // frames stored in signal so far
	fl             *flight             // the aircraft's open flight
}

// rssiWindow is how many frames Aircraft.RSSI averages, as dump1090 does.
const rssiWindow = 8

// addSignal stores one frame's RSSI (dBFS) and returns the average of the last rssiWindow frames in dBFS,
// averaged as linear power.
func (e *entry) addSignal(dbfs float64) float64 {
	e.signal[e.signalN%rssiWindow] = math.Pow(10, dbfs/10)
	e.signalN++
	n := min(e.signalN, rssiWindow)
	var sum float64
	for _, p := range e.signal[:n] {
		sum += p
	}
	return 10 * math.Log10(sum/float64(n))
}

// Tracker keeps the live aircraft table. Update is called by one decoding goroutine; Snapshot, Get and Len
// may be called concurrently from any number of readers and return copies.
//
// Ages are measured in data time: the time of the newest frame plus the wall-clock time elapsed since it
// arrived. Live, that is the wall clock; for a fast replay it follows the capture's own timeline and keeps
// running after the replay ends.
type Tracker struct {
	opt  Options
	wall func() time.Time

	mu       sync.RWMutex
	aircraft map[uint32]*entry
	known    *KnownAddresses
	lastData time.Time
	lastWall time.Time

	flights      map[uint32]*flight // open flights, outliving their aircraft entries by FlightGap
	nextFlightID int64
}

func New(opt Options) *Tracker {
	if opt.Logger == nil {
		opt.Logger = slog.New(slog.DiscardHandler)
	}
	if opt.Stats == nil {
		opt.Stats = stats.New(time.Now())
	}
	if opt.TrailWindow <= 0 {
		opt.TrailWindow = defaultTrailWindow
	}
	if opt.TrailMinInterval <= 0 {
		opt.TrailMinInterval = defaultTrailMinInterval
	}
	if opt.FlightGap <= 0 {
		opt.FlightGap = defaultFlightGap
	}
	return &Tracker{
		opt:          opt,
		wall:         time.Now,
		aircraft:     map[uint32]*entry{},
		known:        NewKnownAddresses(opt.KnownAddressTTL),
		flights:      map[uint32]*flight{},
		nextFlightID: opt.LastFlightID,
	}
}

// Now returns the current data time.
func (t *Tracker) Now() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.nowLocked()
}

func (t *Tracker) nowLocked() time.Time {
	if t.lastData.IsZero() {
		return t.wall()
	}
	return t.lastData.Add(t.wall().Sub(t.lastWall))
}

// Update applies one decoded message. It returns false when the message is dropped: an address/parity
// reply whose address has not been seen recently in a DF11/17/18 frame.
func (t *Tracker) Update(m modes.Message, rssi *float64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if m.Time.After(t.lastData) {
		t.lastData = m.Time
	}
	t.lastWall = t.wall()

	if m.AddressParity {
		if !t.known.Known(m.ICAO, m.Time) {
			t.opt.Stats.AddressParityDrops.Add(1)
			return false
		}
	} else if m.FixedBit == 0 && !m.NonICAO {
		t.known.Add(m.ICAO, m.Time)
	}

	e := t.aircraft[m.ICAO]
	if e == nil {
		e = &entry{a: Aircraft{
			ICAO:         modes.ICAOString(m.ICAO),
			Country:      Country(m.ICAO),
			FirstSeen:    m.Time,
			MessagesByDF: map[int]uint64{},
		}}
		t.aircraft[m.ICAO] = e
	}
	a := &e.a
	if m.Time.After(a.LastSeen) {
		a.LastSeen = m.Time
	}
	e.fl = t.flightFor(m.ICAO, a, m.Time)
	a.Messages++
	a.MessagesByDF[m.DF]++
	if rssi != nil {
		a.RSSI = new(round(e.addSignal(*rssi), 1))
	}
	if m.OnGround != nil {
		a.OnGround = m.OnGround
	}
	if m.DF == 17 || m.DF == 18 {
		a.ADSB = true
	}

	if id := m.Identification; id != nil {
		a.Category = id.Category
		t.setCallsign(a, id.Callsign, "category", id.Category)
	}
	if p := m.AirbornePosition; p != nil {
		if p.Altitude != nil {
			if p.GNSS {
				a.AltGNSS = p.Altitude
			} else {
				a.AltBaro = p.Altitude
			}
		}
		pos, err := t.opt.Resolver.Resolve(&e.cpr, p.CPR, m.Time)
		var attrs []any
		if p.Altitude != nil {
			attrs = append(attrs, "altitude", *p.Altitude, "gnss", p.GNSS)
		}
		t.updatePosition(e, pos, err, rssi, attrs...)
	}
	if p := m.SurfacePosition; p != nil {
		if p.GroundSpeed != nil {
			a.GroundSpeed = new(round(*p.GroundSpeed, 1))
		}
		if p.Track != nil {
			a.Track = new(round(*p.Track, 1))
		}
		pos, err := t.opt.Resolver.ResolveSurface(&e.cpr, p.CPR, m.Time)
		t.updatePosition(e, pos, err, rssi, "surface", true)
	}
	if v := m.Velocity; v != nil {
		t.updateVelocity(a, v)
		e.velocityTime = m.Time
	}
	if s := m.AircraftStatus; s != nil {
		em := s.Emergency
		if sq, ok := modes.SquawkEmergency(s.Squawk); ok && em == modes.EmergencyNone {
			em = sq
		}
		t.setEmergency(a, em, s.Squawk)
		if s.Squawk != "" {
			t.setSquawk(a, s.Squawk, false)
		}
	}
	if ts := m.TargetState; ts != nil {
		t.updateTargetState(a, ts)
	}
	if s := m.OperationalStatus; s != nil && (a.ADSBVersion == nil || *a.ADSBVersion != s.Version) {
		a.ADSBVersion = new(s.Version)
		t.opt.Logger.Debug("operational status", "icao", a.ICAO, "adsbVersion", s.Version, "surface", s.Surface)
	}
	if s := m.Surveillance; s != nil {
		t.updateSurveillance(e, m, s)
	}
	t.syncFlight(m.ICAO, e, m.Time)

	t.opt.Stats.AddMessage(t.lastWall)
	return true
}

func (t *Tracker) updateSurveillance(e *entry, m modes.Message, s *modes.Surveillance) {
	a := &e.a
	if s.BDS20 != "" {
		t.setCallsign(a, s.BDS20, "source", "bds20")
	}
	if s.Squawk != "" {
		t.setSquawk(a, s.Squawk, true)
	}
	if c := s.CommB; c != nil {
		t.updateCommB(e, m.Time, c)
	}
	if s.Altitude != nil {
		a.AltBaro = s.Altitude
		attrs := []any{"icao", a.ICAO, "df", m.DF, "altitude", *s.Altitude}
		if a.OnGround != nil {
			attrs = append(attrs, "onGround", *a.OnGround)
		}
		if !a.ADSB && !e.altitudeLogged {
			e.altitudeLogged = true
			t.opt.Logger.Info("mode-s altitude", attrs...)
		} else {
			t.opt.Logger.Debug("mode-s altitude", attrs...)
		}
	}
}

// commBFresh is how recent ADS-B velocity or an accepted Comm-B report must be to serve as a cross-check reference.
const commBFresh = 30 * time.Second

// commBMatch compares an inferred register with the aircraft's recent state: agree means at least one value could
// be compared and all compared values are close; contradict means some value is far off.
type commBMatch struct{ agree, contradict bool }

func (m *commBMatch) compare(close bool) {
	if close {
		m.agree = true
	} else {
		m.contradict = true
	}
}

// updateCommB applies inferred Comm-B registers received at data time at. A 5,0 or 6,0 report that contradicts
// recent ADS-B velocity or the previous Comm-B report (ground speed or IAS by more than 30 kt, track or heading by
// more than 20°, vertical rates over 1000 ft/min in opposite directions) is counted as rejected. An ambiguous reply is applied
// only when exactly one candidate agrees with the aircraft's recent state, otherwise it is counted and dropped.
// While ADS-B velocity is fresh it owns ground speed, track and vertical rate.
func (t *Tracker) updateCommB(e *entry, at time.Time, c *modes.CommB) {
	a := &e.a
	adsbFresh := !e.velocityTime.IsZero() && at.Sub(e.velocityTime) <= commBFresh
	refFresh := adsbFresh || (!e.commBTime.IsZero() && at.Sub(e.commBTime) <= commBFresh)

	var m40, m50, m60 commBMatch
	if b := c.BDS40; b != nil {
		if b.MCPAltitude != nil && a.SelectedAlt != nil {
			m40.compare(*b.MCPAltitude == *a.SelectedAlt)
		}
		if b.BaroSetting != nil && a.BaroSetting != nil {
			m40.compare(math.Abs(*b.BaroSetting-*a.BaroSetting) < 1)
		}
	}
	if b := c.BDS50; b != nil && refFresh {
		if b.GroundSpeed != nil && a.GroundSpeed != nil {
			m50.compare(math.Abs(*b.GroundSpeed-*a.GroundSpeed) <= 30)
		}
		if b.Track != nil && a.Track != nil {
			m50.compare(angleDiff(*b.Track, *a.Track) <= 20)
		}
	}
	vr60 := (*int)(nil)
	if b := c.BDS60; b != nil {
		vr60 = b.VerticalRateBaro
		if vr60 == nil {
			vr60 = b.VerticalRateINS
		}
		if refFresh {
			if vr60 != nil && a.VerticalRate != nil && min(abs(*vr60), abs(*a.VerticalRate)) > 1000 {
				m60.compare(*vr60**a.VerticalRate > 0)
			}
			if b.IAS != nil && a.IAS != nil {
				m60.compare(abs(*b.IAS-*a.IAS) <= 30)
			}
			if b.Heading != nil && a.Heading != nil {
				m60.compare(angleDiff(*b.Heading, *a.Heading) <= 20)
			}
		}
	}

	use40, use50, use60 := c.BDS40 != nil, c.BDS50 != nil, c.BDS60 != nil
	if c.Ambiguous {
		agree := func(m commBMatch) bool { return m.agree && !m.contradict }
		use40, use50, use60 = use40 && agree(m40), use50 && agree(m50), use60 && agree(m60)
		n := 0
		for _, u := range []bool{use40, use50, use60} {
			if u {
				n++
			}
		}
		if n != 1 {
			t.opt.Stats.CommBAmbiguous.Add(1)
			return
		}
	} else if m50.contradict || m60.contradict {
		t.opt.Stats.CommBRejected.Add(1)
		t.opt.Logger.Debug("comm-b rejected", "icao", a.ICAO, "bds50", c.BDS50 != nil, "bds60", c.BDS60 != nil)
		return
	}

	switch {
	case use40:
		b := c.BDS40
		switch {
		case b.MCPAltitude != nil:
			a.SelectedAlt, a.SelectedAltSource = b.MCPAltitude, "mcp"
		case b.FMSAltitude != nil:
			a.SelectedAlt, a.SelectedAltSource = b.FMSAltitude, "fms"
		}
		if b.BaroSetting != nil {
			a.BaroSetting = b.BaroSetting
		}
		t.opt.Logger.Debug("comm-b 4,0", "icao", a.ICAO, "selectedAlt", val(a.SelectedAlt), "baro", val(b.BaroSetting))
	case use50:
		b := c.BDS50
		if b.Roll != nil {
			a.Roll = new(round(*b.Roll, 1))
		}
		if b.TAS != nil {
			a.TAS = b.TAS
		}
		if !adsbFresh {
			if b.GroundSpeed != nil {
				a.GroundSpeed = new(round(*b.GroundSpeed, 1))
			}
			if b.Track != nil {
				a.Track = new(round(*b.Track, 1))
			}
		}
		e.commBTime = at
		t.opt.Logger.Debug("comm-b 5,0", "icao", a.ICAO, "roll", val(a.Roll), "track", val(b.Track), "groundSpeed", val(b.GroundSpeed), "tas", val(b.TAS))
	case use60:
		b := c.BDS60
		if b.Heading != nil {
			a.Heading = new(round(*b.Heading, 1))
		}
		if b.IAS != nil {
			a.IAS = b.IAS
		}
		if b.Mach != nil {
			a.Mach = new(round(*b.Mach, 3))
		}
		if vr60 != nil && !adsbFresh {
			a.VerticalRate = vr60
		}
		e.commBTime = at
		t.opt.Logger.Debug("comm-b 6,0", "icao", a.ICAO, "heading", val(a.Heading), "ias", val(b.IAS), "mach", val(a.Mach), "verticalRate", val(vr60))
	}
}

// angleDiff is the absolute difference between two bearings in degrees, 0–180.
func angleDiff(x, y float64) float64 {
	d := math.Mod(math.Abs(x-y), 360)
	return min(d, 360-d)
}

func abs(v int) int { return max(v, -v) }

func (t *Tracker) updateVelocity(a *Aircraft, v *modes.Velocity) {
	if v.GroundSpeed != nil {
		a.GroundSpeed = new(round(*v.GroundSpeed, 1))
		a.Track = new(round(*v.Track, 1))
	}
	if v.Heading != nil {
		a.Heading = new(round(*v.Heading, 2))
	}
	if v.IAS != nil {
		a.IAS = v.IAS
	}
	if v.TAS != nil {
		a.TAS = v.TAS
	}
	if v.VerticalRate != nil {
		a.VerticalRate = v.VerticalRate
	}

	if !t.opt.Logger.Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	attrs := []any{"icao", a.ICAO, "subtype", v.Subtype}
	if v.GroundSpeed != nil {
		attrs = append(attrs, "groundSpeed", *a.GroundSpeed, "track", *a.Track)
	}
	if v.Heading != nil {
		attrs = append(attrs, "heading", *a.Heading)
	}
	if v.IAS != nil {
		attrs = append(attrs, "ias", *v.IAS)
	}
	if v.TAS != nil {
		attrs = append(attrs, "tas", *v.TAS)
	}
	if v.VerticalRate != nil {
		attrs = append(attrs, "verticalRate", *v.VerticalRate)
	}
	t.opt.Logger.Debug("velocity", attrs...)
}

// updateTargetState applies TC 29 values that are present and logs at debug level when any of them changes.
func (t *Tracker) updateTargetState(a *Aircraft, ts *modes.TargetState) {
	state := func() []any {
		return []any{"selectedAlt", val(a.SelectedAlt), "source", a.SelectedAltSource, "selectedHeading", val(a.SelectedHeading),
			"baro", val(a.BaroSetting), "modes", a.AutopilotModes}
	}
	before := fmt.Sprint(state()...)
	if ts.SelectedAltitude != nil {
		a.SelectedAlt = ts.SelectedAltitude
		a.SelectedAltSource = "mcp"
		if ts.SelectedAltitudeFMS {
			a.SelectedAltSource = "fms"
		}
	}
	if ts.SelectedHeading != nil {
		a.SelectedHeading = new(round(*ts.SelectedHeading, 1))
	}
	if ts.BaroSetting != nil {
		a.BaroSetting = new(round(*ts.BaroSetting, 1))
	}
	if ts.Modes != nil {
		a.AutopilotModes = ts.Modes.Names()
	}
	if t.opt.Logger.Enabled(context.Background(), slog.LevelDebug) {
		if after := state(); fmt.Sprint(after...) != before {
			t.opt.Logger.Debug("target state", append([]any{"icao", a.ICAO}, after...)...)
		}
	}
}

// val returns *p, or nil when p is nil.
func val[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// updatePosition applies a resolved position (pos, err) from a frame with signal rssi (dBFS, may be nil) to the
// aircraft, counts the outcome and logs the first fix of each aircraft at info level, later ones at debug, with
// extra log attributes.
func (t *Tracker) updatePosition(e *entry, pos Position, err error, rssi *float64, extra ...any) {
	a := &e.a
	firstFix := !a.HasPosition

	switch {
	case err == nil:
		t.opt.Stats.Positions.Add(1)
	case errors.Is(err, ErrOutOfRange), errors.Is(err, ErrImpossibleSpeed), errors.Is(err, modes.ErrCPRZoneMismatch):
		t.opt.Stats.CPRRejected.Add(1)
		t.opt.Logger.Debug("position rejected", "icao", a.ICAO, "lat", pos.Lat, "lon", pos.Lon, "method", pos.Method, "err", err)
		return
	default:
		return
	}

	if t.opt.Coverage != nil {
		alt := a.AltBaro
		if alt == nil {
			alt = a.AltGNSS
		}
		if a.OnGround != nil && *a.OnGround {
			alt = new(0)
		}
		t.opt.Coverage.Add(pos.Point, alt, rssi, pos.Time)
	}

	r := t.opt.Resolver.Receiver
	a.Lat, a.Lon, a.PosTime = new(pos.Lat), new(pos.Lon), new(pos.Time)
	a.DistanceNm = new(round(geo.DistanceNm(r, pos.Point), 2))
	a.Bearing = new(round(geo.Bearing(r, pos.Point), 1))
	a.HasPosition = true
	t.addTrackPoint(e, pos)

	attrs := []any{
		"icao", a.ICAO, "lat", round(pos.Lat, 5), "lon", round(pos.Lon, 5), "method", pos.Method,
		"distanceNm", round(*a.DistanceNm, 1), "bearing", round(*a.Bearing, 0),
	}
	attrs = append(attrs, extra...)
	if firstFix {
		t.opt.Logger.Info("position", attrs...)
	} else {
		t.opt.Logger.Debug("position", attrs...)
	}
}

func (t *Tracker) setCallsign(a *Aircraft, callsign string, extra ...any) {
	if a.Callsign == callsign {
		return
	}
	a.Callsign = callsign
	t.opt.Logger.Info("callsign", append([]any{"icao", a.ICAO, "callsign", callsign}, extra...)...)
}

// setSquawk records a new squawk; one from a surveillance reply also drives the emergency state for
// 7500/7600/7700, since aircraft without ADS-B never send TC 28.
func (t *Tracker) setSquawk(a *Aircraft, squawk string, fromSurveillance bool) {
	if a.Squawk == squawk {
		return
	}
	a.Squawk = squawk
	t.opt.Logger.Info("squawk", "icao", a.ICAO, "squawk", squawk, "adsb", a.ADSB)
	if fromSurveillance {
		em, _ := modes.SquawkEmergency(squawk)
		t.setEmergency(a, em, squawk)
	}
}

func (t *Tracker) setEmergency(a *Aircraft, em modes.Emergency, squawk string) {
	state := ""
	if em != modes.EmergencyNone {
		state = em.String()
	}
	if a.Emergency == state {
		return
	}
	a.Emergency = state
	if state == "" {
		state = modes.EmergencyNone.String()
	}
	t.opt.Logger.Info("emergency", "icao", a.ICAO, "state", state, "squawk", squawk)
}

// Expire removes aircraft not heard for longer than TTL, closes flights not heard for longer than TTL+FlightGap,
// and returns how many aircraft were removed.
func (t *Tracker) Expire() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.nowLocked()
	n := 0
	for icao, e := range t.aircraft {
		if now.Sub(e.a.LastSeen) > t.opt.TTL {
			delete(t.aircraft, icao)
			t.opt.Logger.Debug("aircraft expired", "icao", e.a.ICAO, "callsign", e.a.Callsign)
			n++
		}
	}
	t.expireFlights(now)
	t.known.Expire(now)
	return n
}

// Snapshot returns copies of all aircraft heard within TTL, in no particular order.
func (t *Tracker) Snapshot() []Aircraft {
	t.mu.RLock()
	defer t.mu.RUnlock()
	now := t.nowLocked()
	out := make([]Aircraft, 0, len(t.aircraft))
	for _, e := range t.aircraft {
		if now.Sub(e.a.LastSeen) <= t.opt.TTL {
			out = append(out, e.a.clone())
		}
	}
	return out
}

// Get returns a copy of one aircraft heard within TTL.
func (t *Tracker) Get(icao uint32) (Aircraft, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	e := t.aircraft[icao]
	if e == nil || t.nowLocked().Sub(e.a.LastSeen) > t.opt.TTL {
		return Aircraft{}, false
	}
	return e.a.clone(), true
}

// Len returns the number of aircraft currently stored (expired ones count until the next Expire).
func (t *Tracker) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.aircraft)
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
