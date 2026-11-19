package app

import (
	"errors"
	"fmt"
	"log/slog"
	"math"

	"github.com/PVKonovalov/df17-decoder/internal/config"
	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

// aircraftLog is the per-aircraft memory used to decide what to log. Replaced by the tracker in M5.
type aircraftLog struct {
	callsign       string
	squawk         string
	adsb           bool
	altitudeLogged bool
	emergency      modes.Emergency
	version        *int
	cpr            tracker.CPRState
}

// pipeline validates and decodes frames and logs what changed. It is used by a single goroutine.
type pipeline struct {
	log      *slog.Logger
	c        *Counters
	opt      modes.Options
	resolver tracker.PositionResolver
	known    *tracker.KnownAddresses
	aircraft map[uint32]*aircraftLog
}

func newPipeline(cfg config.Config, log *slog.Logger, c *Counters) *pipeline {
	return &pipeline{
		log: log,
		c:   c,
		opt: modes.Options{FixSingleBitErrors: cfg.Decoder.FixSingleBitErrors},
		resolver: tracker.PositionResolver{
			Receiver:   geo.Point{Lat: cfg.Receiver.Lat, Lon: cfg.Receiver.Lon},
			MaxRangeNm: cfg.Receiver.MaxRangeNm,
			PairMaxAge: cfg.Decoder.CPRPairMaxAge.D(),
			RefMaxAge:  cfg.Tracker.TTL.D(),
		},
		known:    tracker.NewKnownAddresses(cfg.Tracker.KnownAddressTTL.D()),
		aircraft: map[uint32]*aircraftLog{},
	}
}

func (p *pipeline) handle(f source.Frame) {
	c := p.c
	c.Frames++
	m, err := modes.Decode(f.Data, f.Time, p.opt)
	c.PerDF[m.DF]++
	switch {
	case errors.Is(err, modes.ErrCRC):
		c.CRCFailures++
		return
	case errors.Is(err, modes.ErrUnsupported), errors.Is(err, modes.ErrUnsupportedTC):
		c.Unsupported++
		return
	case err != nil:
		c.Invalid++
		p.log.Debug("invalid frame", "frame", fmt.Sprintf("%X", f.Data), "err", err)
		return
	}

	if m.AddressParity {
		if !p.known.Known(m.ICAO, m.Time) {
			c.AddressParityDrops++
			return
		}
	} else if m.FixedBit == 0 && !m.NonICAO {
		p.known.Add(m.ICAO, m.Time)
	}

	c.Decoded++
	if m.FixedBit != 0 {
		c.Fixed++
	}

	a := p.aircraft[m.ICAO]
	if a == nil {
		a = &aircraftLog{}
		p.aircraft[m.ICAO] = a
	}
	if m.DF == 17 || m.DF == 18 {
		a.adsb = true
	}
	icao := modes.ICAOString(m.ICAO)

	if id := m.Identification; id != nil {
		p.setCallsign(a, icao, id.Callsign, "category", id.Category)
	}
	if pos := m.AirbornePosition; pos != nil {
		p.handlePosition(a, icao, m, pos)
	}
	if v := m.Velocity; v != nil {
		p.log.Debug("velocity", velocityAttrs(icao, v)...)
	}
	if s := m.AircraftStatus; s != nil {
		e := s.Emergency
		if sq, ok := modes.SquawkEmergency(s.Squawk); ok && e == modes.EmergencyNone {
			e = sq
		}
		p.setEmergency(a, icao, e, s.Squawk)
		if s.Squawk != "" {
			p.setSquawk(a, icao, s.Squawk, false)
		}
	}
	if s := m.OperationalStatus; s != nil && (a.version == nil || *a.version != s.Version) {
		a.version = &s.Version
		p.log.Debug("operational status", "icao", icao, "adsbVersion", s.Version, "surface", s.Surface)
	}
	if s := m.Surveillance; s != nil {
		p.handleSurveillance(a, icao, m, s)
	}
}

func (p *pipeline) handleSurveillance(a *aircraftLog, icao string, m modes.Message, s *modes.Surveillance) {
	if s.BDS20 != "" {
		p.setCallsign(a, icao, s.BDS20, "source", "bds20")
	}
	if s.Squawk != "" {
		p.setSquawk(a, icao, s.Squawk, true)
	}
	if s.Altitude != nil {
		attrs := []any{"icao", icao, "df", m.DF, "altitude", *s.Altitude}
		if m.OnGround != nil {
			attrs = append(attrs, "onGround", *m.OnGround)
		}
		if !a.adsb && !a.altitudeLogged {
			a.altitudeLogged = true
			p.log.Info("mode-s altitude", attrs...)
		} else {
			p.log.Debug("mode-s altitude", attrs...)
		}
	}
}

// setCallsign logs a new or changed callsign at info level.
func (p *pipeline) setCallsign(a *aircraftLog, icao, callsign string, extra ...any) {
	if a.callsign == callsign {
		return
	}
	a.callsign = callsign
	p.log.Info("callsign", append([]any{"icao", icao, "callsign", callsign}, extra...)...)
}

// setSquawk logs a new or changed squawk; a squawk from a surveillance reply also drives the emergency
// state for 7500/7600/7700 (aircraft without ADS-B have no TC 28).
func (p *pipeline) setSquawk(a *aircraftLog, icao, squawk string, fromSurveillance bool) {
	if a.squawk == squawk {
		return
	}
	a.squawk = squawk
	p.log.Info("squawk", "icao", icao, "squawk", squawk, "adsb", a.adsb)
	if fromSurveillance {
		e, _ := modes.SquawkEmergency(squawk)
		p.setEmergency(a, icao, e, squawk)
	}
}

// setEmergency logs emergency state changes; the initial "none" is not logged.
func (p *pipeline) setEmergency(a *aircraftLog, icao string, e modes.Emergency, squawk string) {
	if e == a.emergency {
		return
	}
	a.emergency = e
	p.log.Info("emergency", "icao", icao, "state", e, "squawk", squawk)
}

// handlePosition resolves an airborne position, counts the outcome and logs the first fix of each aircraft
// at info level, later ones at debug.
func (p *pipeline) handlePosition(a *aircraftLog, icao string, m modes.Message, ap *modes.AirbornePosition) {
	firstFix := a.cpr.Last() == nil

	pos, err := p.resolver.Resolve(&a.cpr, ap.CPR, m.Time)
	switch {
	case err == nil:
		p.c.Positions++
	case errors.Is(err, tracker.ErrOutOfRange), errors.Is(err, tracker.ErrImpossibleSpeed), errors.Is(err, modes.ErrCPRZoneMismatch):
		p.c.CPRRejected++
		p.log.Debug("position rejected", "icao", icao, "lat", pos.Lat, "lon", pos.Lon, "method", pos.Method, "err", err)
		return
	default:
		p.c.CPRWaiting++
		return
	}

	r := p.resolver.Receiver
	attrs := []any{
		"icao", icao, "lat", round(pos.Lat, 5), "lon", round(pos.Lon, 5), "method", pos.Method,
		"distanceNm", round(geo.DistanceNm(r, pos.Point), 1),
		"bearing", round(geo.Bearing(r, pos.Point), 0),
	}
	if ap.Altitude != nil {
		attrs = append(attrs, "altitude", *ap.Altitude, "gnss", ap.GNSS)
	}
	if firstFix {
		p.log.Info("position", attrs...)
	} else {
		p.log.Debug("position", attrs...)
	}
}

func velocityAttrs(icao string, v *modes.Velocity) []any {
	attrs := []any{"icao", icao, "subtype", v.Subtype}
	if v.GroundSpeed != nil {
		attrs = append(attrs, "groundSpeed", round(*v.GroundSpeed, 1), "track", round(*v.Track, 1))
	}
	if v.Heading != nil {
		attrs = append(attrs, "heading", round(*v.Heading, 1))
	}
	if v.IAS != nil {
		attrs = append(attrs, "ias", *v.IAS)
	}
	if v.TAS != nil {
		attrs = append(attrs, "tas", *v.TAS)
	}
	if v.VerticalRate != nil {
		attrs = append(attrs, "verticalRate", *v.VerticalRate, "vrBaro", v.VerticalRateBaro)
	}
	return attrs
}

func round(v float64, places int) float64 {
	pw := math.Pow(10, float64(places))
	return math.Round(v*pw) / pw
}
