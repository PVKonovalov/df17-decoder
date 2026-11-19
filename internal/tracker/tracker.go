package tracker

import (
	"context"
	"errors"
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
}

type entry struct {
	a              Aircraft
	cpr            CPRState
	altitudeLogged bool
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
}

func New(opt Options) *Tracker {
	if opt.Logger == nil {
		opt.Logger = slog.New(slog.DiscardHandler)
	}
	if opt.Stats == nil {
		opt.Stats = stats.New(time.Now())
	}
	return &Tracker{
		opt:      opt,
		wall:     time.Now,
		aircraft: map[uint32]*entry{},
		known:    NewKnownAddresses(opt.KnownAddressTTL),
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
	a.Messages++
	a.MessagesByDF[m.DF]++
	if rssi != nil {
		a.RSSI = new(*rssi)
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
		t.updatePosition(e, m, p)
	}
	if v := m.Velocity; v != nil {
		t.updateVelocity(a, v)
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
	if s := m.OperationalStatus; s != nil && (a.ADSBVersion == nil || *a.ADSBVersion != s.Version) {
		a.ADSBVersion = new(s.Version)
		t.opt.Logger.Debug("operational status", "icao", a.ICAO, "adsbVersion", s.Version, "surface", s.Surface)
	}
	if s := m.Surveillance; s != nil {
		t.updateSurveillance(e, m, s)
	}

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

// updatePosition resolves an airborne position, counts the outcome and logs the first fix of each aircraft
// at info level, later ones at debug.
func (t *Tracker) updatePosition(e *entry, m modes.Message, p *modes.AirbornePosition) {
	a := &e.a
	firstFix := !a.HasPosition

	pos, err := t.opt.Resolver.Resolve(&e.cpr, p.CPR, m.Time)
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

	r := t.opt.Resolver.Receiver
	a.Lat, a.Lon, a.PosTime = new(pos.Lat), new(pos.Lon), new(pos.Time)
	a.DistanceNm = new(round(geo.DistanceNm(r, pos.Point), 2))
	a.Bearing = new(round(geo.Bearing(r, pos.Point), 1))
	a.HasPosition = true

	attrs := []any{
		"icao", a.ICAO, "lat", round(pos.Lat, 5), "lon", round(pos.Lon, 5), "method", pos.Method,
		"distanceNm", round(*a.DistanceNm, 1), "bearing", round(*a.Bearing, 0),
	}
	if p.Altitude != nil {
		attrs = append(attrs, "altitude", *p.Altitude, "gnss", p.GNSS)
	}
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

// Expire removes aircraft not heard for longer than TTL and returns how many were removed.
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
