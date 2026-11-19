package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/PVKonovalov/df17-decoder/internal/config"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/source"
)

// Counters summarise one Run.
type Counters struct {
	Frames      uint64
	Decoded     uint64
	CRCFailures uint64
	Fixed       uint64
	Unsupported uint64
	Invalid     uint64
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
	callsigns := map[uint32]string{}

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
	}

	c.Source = src.Stats().Snapshot()
	err = <-errc
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	log.Info("source finished",
		"frames", c.Frames, "decoded", c.Decoded, "crcFailures", c.CRCFailures,
		"unsupported", c.Unsupported, "malformedLines", c.Source.Malformed)
	return c, err
}
