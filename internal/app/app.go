package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/api"
	"github.com/PVKonovalov/df17-decoder/internal/config"
	"github.com/PVKonovalov/df17-decoder/internal/coverage"
	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/history"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/playback"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/stats"
	"github.com/PVKonovalov/df17-decoder/internal/storage"
	"github.com/PVKonovalov/df17-decoder/internal/tape"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
	"github.com/PVKonovalov/df17-decoder/internal/version"
	"github.com/PVKonovalov/df17-decoder/web"
)

const (
	expireInterval       = time.Second
	shutdownTimeout      = 5 * time.Second
	coverageSaveInterval = time.Minute
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
	cfg        config.Config
	log        *slog.Logger
	saveFailed bool // the last coverage save failed; only the coverage goroutine touches it
	resume     bool // live source: open flights stay open over a restart and the next run resumes them
	src        source.Source
	opt        modes.Options
	ln         net.Listener
	Stats      *stats.Stats
	Tracker    *tracker.Tracker
	Coverage   *coverage.Coverage
	History    *history.Store // nil when history is disabled
	Tape       *tape.Recorder // nil when the tape is disabled
	TapeData   *tape.Tape     // the recording read back; nil when the tape is disabled
}

func New(cfg config.Config, log *slog.Logger) (*Service, error) {
	scheme, addr, err := cfg.Source.Endpoint()
	if err != nil {
		return nil, err
	}
	var src source.Source
	if scheme == "tape" {
		dir, from, speed, _ := tape.ParseURL(cfg.Source.URL)
		src = &tape.Source{Tape: tape.Open(dir), From: from, Speed: speed}
		if cfg.Tape.Enabled {
			cfg.Tape.Enabled = false
			log.Info("tape recording is off while the input is a tape", "source", cfg.Source.URL)
		}
	} else if src, err = source.New(scheme, addr, source.Options{
		ReconnectMin: cfg.Source.ReconnectMin.D(),
		ReconnectMax: cfg.Source.ReconnectMax.D(),
		ReplaySpeed:  cfg.Source.ReplaySpeed,
		Logger:       log,
	}); err != nil {
		return nil, err
	}

	receiver := geo.Point{Lat: cfg.Receiver.Lat, Lon: cfg.Receiver.Lon}
	cov := coverage.New(receiver)
	if cfg.Coverage.File != "" {
		var discarded bool
		if cov, discarded, err = coverage.Load(cfg.Coverage.File, receiver); err != nil {
			return nil, err
		}
		file, _ := filepath.Abs(cfg.Coverage.File)
		if discarded {
			log.Warn("coverage file is for another receiver position, starting a new outline", "file", file)
		} else {
			log.Info("coverage loaded", "file", file, "buckets", len(cov.Snapshot().Points))
		}
	}

	var (
		hist     *history.Store
		sink     tracker.FlightSink
		lastID   int64
		histFile string
		resumed  []tracker.ResumedFlight
	)
	live := scheme == "tcp" || scheme == "beast"
	if cfg.History.Enabled {
		resumeWithin := time.Duration(0)
		if live {
			resumeWithin = cfg.Tracker.TTL.D() + cfg.History.FlightGap.D()
		}
		if hist, err = history.Open(history.Options{
			File:           cfg.History.File,
			ResumeWithin:   resumeWithin,
			ResumeTrail:    cfg.Trails.Window.D(),
			FlushInterval:  cfg.History.FlushInterval.D(),
			Retention:      cfg.History.Retention.D(),
			Logger:         log,
			Receiver:       &receiver,
			StatsRetention: cfg.Stats.Retention.D(),
		}); err != nil {
			return nil, err
		}
		sink, lastID, resumed = hist, hist.LastFlightID(), hist.ResumeFlights()
		histFile, _ = filepath.Abs(cfg.History.File)
		log.Info("history enabled", "file", histFile, "lastFlight", lastID)
	}

	var (
		rec      *tape.Recorder
		tapeData *tape.Tape
	)
	if cfg.Tape.Enabled {
		tapeData = tape.Open(cfg.Tape.Dir)
		if rec, err = tape.New(tape.Options{
			Dir:           cfg.Tape.Dir,
			Segment:       cfg.Tape.Segment.D(),
			FlushInterval: cfg.Tape.FlushInterval.D(),
			Retention:     cfg.Tape.Retention.D(),
			MinFreeBytes:  uint64(cfg.Tape.MinFreeMB) << 20,
			Logger:        log,
		}); err != nil {
			return nil, err
		}
		dir, _ := filepath.Abs(cfg.Tape.Dir)
		log.Info("tape recording enabled", "dir", dir, "segment", cfg.Tape.Segment.D(), "retention", cfg.Tape.Retention.D())
	}

	st := stats.New(time.Now())
	return &Service{
		cfg:      cfg,
		log:      log,
		src:      src,
		opt:      modes.Options{FixSingleBitErrors: cfg.Decoder.FixSingleBitErrors},
		Stats:    st,
		Coverage: cov,
		History:  hist,
		Tape:     rec,
		TapeData: tapeData,
		resume:   live,
		Tracker: tracker.New(tracker.Options{
			TTL:             cfg.Tracker.TTL.D(),
			KnownAddressTTL: cfg.Tracker.KnownAddressTTL.D(),
			Resolver: tracker.PositionResolver{
				Receiver:   receiver,
				MaxRangeNm: cfg.Receiver.MaxRangeNm,
				PairMaxAge: cfg.Decoder.CPRPairMaxAge.D(),
				RefMaxAge:  cfg.Tracker.TTL.D(),
			},
			Logger:           log,
			Stats:            st,
			Coverage:         cov,
			TrailWindow:      cfg.Trails.Window.D(),
			TrailMinInterval: cfg.Trails.MinInterval.D(),
			FlightGap:        cfg.History.FlightGap.D(),
			Flights:          sink,
			LastFlightID:     lastID,
			Resume:           resumed,
		}),
	}, nil
}

// tapeFiles joins the recorder's counters and the recorded files for the storage report.
type tapeFiles struct {
	*tape.Recorder
	*tape.Tape
}

// storageReporter reports on the state dir (the working directory) and what the tape and history keep there.
func (s *Service) storageReporter() *storage.Reporter {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	src := storage.Sources{
		StateDir:      dir,
		TapeRetention: s.cfg.Tape.Retention.D(),
		MinFreeBytes:  uint64(s.cfg.Tape.MinFreeMB) << 20,
	}
	if s.Tape != nil {
		src.Tape = tapeFiles{s.Tape, s.TapeData}
	}
	if s.History != nil {
		src.History = s.History
	}
	return storage.NewReporter(src)
}

// rollupSources are the live values the hourly rollups sample: the decoding and receiver counters and the tape size.
func (s *Service) rollupSources() history.RollupSources {
	src := history.RollupSources{Counters: func() history.Counters {
		return history.Counters{
			Messages: s.Stats.Messages.Load(), Frames: s.Stats.Frames.Load(), CRCFailures: s.Stats.CRCFailures.Load(),
			FixedBitErrors: s.Stats.FixedBitErrors.Load(), CPRRejected: s.Stats.CPRRejected.Load(),
			Reconnects: s.SourceStats().Reconnects, SignalFrames: s.Stats.SignalFrames.Load(),
			SignalPower: s.Stats.SignalPower(),
		}
	}}
	if s.Tape != nil {
		src.TapeBytes = func() (int64, bool) { return s.Tape.Stats().Bytes, true }
	}
	return src
}

// playback returns the playback manager over the recorded tape, nil when the tape or playback is disabled.
func (s *Service) playback() *playback.Manager {
	if s.Tape == nil || s.cfg.Playback.MaxSessions == 0 {
		return nil
	}
	return &playback.Manager{
		Tape:        s.TapeData,
		Tracker:     s.Tracker.Options(),
		Decode:      s.opt,
		MaxSessions: s.cfg.Playback.MaxSessions,
		IdleTimeout: s.cfg.Playback.IdleTimeout.D(),
		EndMargin:   s.cfg.API.StreamInterval.D(),
		Bounds: func() (time.Time, time.Time) {
			st := s.Tape.Stats()
			return st.Oldest, st.Newest
		},
		Logger: s.log,
	}
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
			Receiver: api.Receiver{
				Lat: s.cfg.Receiver.Lat, Lon: s.cfg.Receiver.Lon, MaxRangeNm: s.cfg.Receiver.MaxRangeNm,
			},
			Map: api.MapConfig{
				TileURL: s.cfg.Map.TileURL, Attribution: s.cfg.Map.Attribution,
				MaxZoom: s.cfg.Map.MaxZoom, DarkFilter: s.cfg.Map.DarkFilter,
			},
			UI:             web.Dist(),
			Tracker:        s.Tracker,
			Coverage:       s.Coverage,
			History:        s.History,
			Tape:           s.Tape,
			TapeData:       s.TapeData,
			Playback:       s.playback(),
			Storage:        s.storageReporter(),
			Stats:          s.Stats,
			Source:         s.SourceStats,
			SourceURL:      s.cfg.Source.URL,
			Version:        version.Get().Version,
			StreamInterval: s.cfg.API.StreamInterval.D(),
			Logger:         s.log,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx }, // ends SSE streams on shutdown
	}

	var hist sync.WaitGroup
	histCtx, stopHistory := context.WithCancel(context.WithoutCancel(ctx))
	defer stopHistory()
	if s.History != nil {
		hist.Go(func() { s.History.Run(histCtx) })
		hist.Go(func() { s.History.RunRollups(histCtx, s.rollupSources()) })
	}
	var rec sync.WaitGroup
	recCtx, stopTape := context.WithCancel(context.WithoutCancel(ctx))
	defer stopTape()
	if s.Tape != nil {
		rec.Go(func() { s.Tape.Run(recCtx) })
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		s.log.Info("api listening", "addr", addr.String())
		if err := srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("api server failed", "err", err)
			cancel()
		}
	})
	if s.cfg.Coverage.File != "" {
		wg.Go(func() {
			ticker := time.NewTicker(coverageSaveInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					s.saveCoverage()
				case <-ctx.Done():
					s.saveCoverage()
					return
				}
			}
		})
	}
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
	stopTape()
	rec.Wait()
	if s.History != nil {
		if s.resume {
			s.Tracker.SyncFlights()
		} else {
			s.Tracker.CloseFlights()
		}
		stopHistory()
		hist.Wait()
		if err := s.History.Close(); err != nil {
			s.log.Warn("history close", "err", err)
		}
	}
	return ingestErr
}

// saveCoverage saves the coverage file, logging a failure only when it starts and again when saving recovers.
func (s *Service) saveCoverage() {
	err := s.Coverage.Save(s.cfg.Coverage.File)
	switch {
	case err != nil && !s.saveFailed:
		s.log.Warn("coverage save failed, retrying every minute without further warnings", "file", s.cfg.Coverage.File, "err", err)
	case err == nil && s.saveFailed:
		s.log.Info("coverage save works again", "file", s.cfg.Coverage.File)
	}
	s.saveFailed = err != nil
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
		if s.Tape != nil {
			s.Tape.Record(f)
		}
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
	if f.RSSI != nil {
		s.Stats.AddSignal(*f.RSSI)
	}
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
