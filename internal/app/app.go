package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/api"
	"github.com/PVKonovalov/df17-decoder/internal/config"
	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/stats"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

const (
	expireInterval  = time.Second
	shutdownTimeout = 5 * time.Second
)

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

// Service wires source → decoder → tracker → HTTP API.
type Service struct {
	cfg     config.Config
	log     *slog.Logger
	src     source.Source
	opt     modes.Options
	ln      net.Listener
	Stats   *stats.Stats
	Tracker *tracker.Tracker
}

func New(cfg config.Config, log *slog.Logger) (*Service, error) {
	scheme, addr, err := cfg.Source.Endpoint()
	if err != nil {
		return nil, err
	}
	src, err := source.New(scheme, addr, source.Options{
		ReconnectMin: cfg.Source.ReconnectMin.D(),
		ReconnectMax: cfg.Source.ReconnectMax.D(),
		ReplaySpeed:  cfg.Source.ReplaySpeed,
		Logger:       log,
	})
	if err != nil {
		return nil, err
	}

	st := stats.New(time.Now())
	return &Service{
		cfg:   cfg,
		log:   log,
		src:   src,
		opt:   modes.Options{FixSingleBitErrors: cfg.Decoder.FixSingleBitErrors},
		Stats: st,
		Tracker: tracker.New(tracker.Options{
			TTL:             cfg.Tracker.TTL.D(),
			KnownAddressTTL: cfg.Tracker.KnownAddressTTL.D(),
			Resolver: tracker.PositionResolver{
				Receiver:   geo.Point{Lat: cfg.Receiver.Lat, Lon: cfg.Receiver.Lon},
				MaxRangeNm: cfg.Receiver.MaxRangeNm,
				PairMaxAge: cfg.Decoder.CPRPairMaxAge.D(),
				RefMaxAge:  cfg.Tracker.TTL.D(),
			},
			Logger: log,
			Stats:  st,
		}),
	}, nil
}

// SourceStats returns the source's live counters.
func (s *Service) SourceStats() source.StatsSnapshot { return s.src.Stats().Snapshot() }

// Listen opens the API listener (api.listen) and returns its address. Run calls it if needed.
func (s *Service) Listen() (net.Addr, error) {
	if s.ln == nil {
		ln, err := net.Listen("tcp", s.cfg.API.Listen)
		if err != nil {
			return nil, fmt.Errorf("api: %w", err)
		}
		s.ln = ln
	}
	return s.ln.Addr(), nil
}

// Run serves the API and ingests frames until ctx is cancelled. When a file replay ends, the API keeps
// serving the (ageing) aircraft table until ctx is cancelled.
func (s *Service) Run(ctx context.Context) error {
	addr, err := s.Listen()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	srv := &http.Server{
		Handler: api.New(api.Deps{
			Tracker:        s.Tracker,
			Stats:          s.Stats,
			Source:         s.SourceStats,
			SourceURL:      s.cfg.Source.URL,
			StreamInterval: s.cfg.API.StreamInterval.D(),
			Logger:         s.log,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx }, // ends SSE streams on shutdown
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		s.log.Info("api listening", "addr", addr.String())
		if err := srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("api server failed", "err", err)
			cancel()
		}
	})
	wg.Go(func() {
		ticker := time.NewTicker(expireInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.Tracker.Expire()
			case <-ctx.Done():
				return
			}
		}
	})

	ingestErr := s.Ingest(ctx)
	if ingestErr == nil && ctx.Err() == nil {
		s.log.Info("source finished, API keeps serving until interrupted")
		<-ctx.Done()
	}
	cancel()

	shutdownCtx, stop := context.WithTimeout(context.Background(), shutdownTimeout)
	defer stop()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		s.log.Warn("api shutdown", "err", err)
	}
	wg.Wait()
	return ingestErr
}

// Ingest reads frames from the source and feeds the tracker until ctx is cancelled or the source ends
// (file replay). A cancelled ctx is not an error.
func (s *Service) Ingest(ctx context.Context) error {
	frames := make(chan source.Frame, 1024)
	errc := make(chan error, 1)
	go func() {
		errc <- s.src.Run(ctx, frames)
		close(frames)
	}()

	for f := range frames {
		s.handle(f)
	}

	err := <-errc
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	st := s.Stats.Snapshot(time.Now())
	s.log.Info("source stopped",
		"frames", st.Frames, "messages", st.Messages, "crcFailures", st.CRCFailures,
		"unsupported", st.Unsupported, "malformedLines", s.SourceStats().Malformed,
		"positions", st.Positions, "cprRejected", st.CPRRejected, "addressParityDrops", st.AddressParityDrops,
		"aircraft", s.Tracker.Len())
	return err
}

func (s *Service) handle(f source.Frame) {
	m, err := modes.Decode(f.Data, f.Time, s.opt)
	s.Stats.AddFrame(m.DF)
	switch {
	case errors.Is(err, modes.ErrCRC):
		s.Stats.CRCFailures.Add(1)
		return
	case errors.Is(err, modes.ErrUnsupported), errors.Is(err, modes.ErrUnsupportedTC):
		s.Stats.Unsupported.Add(1)
		return
	case err != nil:
		s.Stats.Invalid.Add(1)
		s.log.Debug("invalid frame", "frame", fmt.Sprintf("%X", f.Data), "err", err)
		return
	}
	if s.Tracker.Update(m, f.RSSI) && m.FixedBit != 0 {
		s.Stats.FixedBitErrors.Add(1)
	}
}
