package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"

	"github.com/PVKonovalov/df17-decoder/internal/config"
	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

// Counters summarise one Run.
type Counters struct {
	Frames      uint64
	Decoded     uint64
	CRCFailures uint64
	Fixed       uint64
	Unsupported uint64
	Invalid     uint64
	Positions   uint64 // accepted CPR positions
	CPRWaiting  uint64 // no pair and no usable reference yet
	CPRRejected uint64 // zone mismatch, out of range, impossible speed
	PerDF       map[int]uint64
	Source      source.StatsSnapshot
}

// NewLogger builds an slog logger for cfg writing to w.
func NewLogger(cfg config.Log, w io.Writer) *slog.Logger {
	var level slog.Level
	_ = level.UnmarshalText([]byte(cfg.Level))
	opts := &slog.HandlerOptions{Level: level}
	if cfg.Format == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

// Run reads frames from the configured source and decodes them until ctx is cancelled or the source
// ends (file replay). A cancelled ctx is not an error.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) (Counters, error) {
	c := Counters{PerDF: map[int]uint64{}}

	scheme, addr, err := cfg.Source.Endpoint()
	if err != nil {
		return c, err
	}
	src, err := source.New(scheme, addr, source.Options{
		ReconnectMin: cfg.Source.ReconnectMin.D(),
		ReconnectMax: cfg.Source.ReconnectMax.D(),
		ReplaySpeed:  cfg.Source.ReplaySpeed,
		Logger:       log,
	})
	if err != nil {
		return c, err
	}

	frames := make(chan source.Frame, 1024)
	errc := make(chan error, 1)
	go func() {
		errc <- src.Run(ctx, frames)
		close(frames)
	}()

	opt := modes.Options{FixSingleBitErrors: cfg.Decoder.FixSingleBitErrors}
	resolver := tracker.PositionResolver{
		Receiver:   geo.Point{Lat: cfg.Receiver.Lat, Lon: cfg.Receiver.Lon},
		MaxRangeNm: cfg.Receiver.MaxRangeNm,
		PairMaxAge: cfg.Decoder.CPRPairMaxAge.D(),
		RefMaxAge:  cfg.Tracker.TTL.D(),
	}
	callsigns := map[uint32]string{}
	cpr := map[uint32]*tracker.CPRState{}

	for f := range frames {
		c.Frames++
		m, err := modes.Decode(f.Data, f.Time, opt)
		c.PerDF[m.DF]++
		switch {
		case errors.Is(err, modes.ErrCRC):
			c.CRCFailures++
			continue
		case errors.Is(err, modes.ErrUnsupported), errors.Is(err, modes.ErrUnsupportedTC):
			c.Unsupported++
			continue
		case err != nil:
			c.Invalid++
			log.Debug("invalid frame", "frame", fmt.Sprintf("%X", f.Data), "err", err)
			continue
		}
		c.Decoded++
		if m.FixedBit != 0 {
			c.Fixed++
		}

		if id := m.Identification; id != nil && callsigns[m.ICAO] != id.Callsign {
			callsigns[m.ICAO] = id.Callsign
			log.Info("callsign", "icao", modes.ICAOString(m.ICAO), "callsign", id.Callsign, "category", id.Category)
		}

		if p := m.AirbornePosition; p != nil {
			handlePosition(log, &c, resolver, cpr, m, p)
		}
	}

	c.Source = src.Stats().Snapshot()
	err = <-errc
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	log.Info("source finished",
		"frames", c.Frames, "decoded", c.Decoded, "crcFailures", c.CRCFailures,
		"unsupported", c.Unsupported, "malformedLines", c.Source.Malformed,
		"positions", c.Positions, "cprRejected", c.CPRRejected)
	return c, err
}

// handlePosition resolves an airborne position, counts the outcome and logs the first fix of each aircraft
// at info level, later ones at debug.
func handlePosition(log *slog.Logger, c *Counters, r tracker.PositionResolver, states map[uint32]*tracker.CPRState, m modes.Message, p *modes.AirbornePosition) {
	st := states[m.ICAO]
	if st == nil {
		st = &tracker.CPRState{}
		states[m.ICAO] = st
	}
	firstFix := st.Last() == nil

	pos, err := r.Resolve(st, p.CPR, m.Time)
	icao := modes.ICAOString(m.ICAO)
	switch {
	case err == nil:
		c.Positions++
	case errors.Is(err, tracker.ErrOutOfRange), errors.Is(err, tracker.ErrImpossibleSpeed), errors.Is(err, modes.ErrCPRZoneMismatch):
		c.CPRRejected++
		log.Debug("position rejected", "icao", icao, "lat", pos.Lat, "lon", pos.Lon, "method", pos.Method, "err", err)
		return
	default:
		c.CPRWaiting++
		return
	}

	attrs := []any{
		"icao", icao, "lat", round(pos.Lat, 5), "lon", round(pos.Lon, 5), "method", pos.Method,
		"distanceNm", round(geo.DistanceNm(r.Receiver, pos.Point), 1),
		"bearing", round(geo.Bearing(r.Receiver, pos.Point), 0),
	}
	if p.Altitude != nil {
		attrs = append(attrs, "altitude", *p.Altitude, "gnss", p.GNSS)
	}
	if firstFix {
		log.Info("position", attrs...)
	} else {
		log.Debug("position", attrs...)
	}
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
