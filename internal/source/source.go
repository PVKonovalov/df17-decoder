package source

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// Frame is one raw Mode S frame as delivered by a source.
type Frame struct {
	Data      []byte
	Time      time.Time // local receive (or replay emission) time
	Timestamp uint64    // 12 MHz receiver clock, 0 when the input has none
	RSSI      *float64  // dBFS, Beast only
}

// Source delivers frames to out until ctx is cancelled or the input ends (file replay).
type Source interface {
	Run(ctx context.Context, out chan<- Frame) error
	Stats() *Stats
}

type Options struct {
	ReconnectMin time.Duration
	ReconnectMax time.Duration
	ReplaySpeed  float64
	Logger       *slog.Logger
}

// New creates a source for scheme ("tcp", "beast", "file") and address (host:port or path), as returned by
// config.Source.Endpoint.
func New(scheme, address string, opt Options) (Source, error) {
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	switch scheme {
	case "tcp":
		return &TCP{Address: address, Opt: opt}, nil
	case "file":
		return &File{Path: address, Opt: opt}, nil
	case "beast":
		return &TCP{Address: address, Format: Beast, Opt: opt}, nil
	default:
		return nil, fmt.Errorf("source: unknown scheme %q", scheme)
	}
}

// Stats are live counters shared by the source goroutine and readers.
type Stats struct {
	connected  atomic.Bool
	frames     atomic.Uint64
	malformed  atomic.Uint64
	reconnects atomic.Uint64
	lastFrame  atomic.Int64 // unix nanoseconds
}

type StatsSnapshot struct {
	Connected  bool
	Frames     uint64
	Malformed  uint64
	Reconnects uint64
	LastFrame  time.Time // zero if no frame yet
}

func (s *Stats) Snapshot() StatsSnapshot {
	snap := StatsSnapshot{
		Connected:  s.connected.Load(),
		Frames:     s.frames.Load(),
		Malformed:  s.malformed.Load(),
		Reconnects: s.reconnects.Load(),
	}
	if ns := s.lastFrame.Load(); ns != 0 {
		snap.LastFrame = time.Unix(0, ns)
	}
	return snap
}
