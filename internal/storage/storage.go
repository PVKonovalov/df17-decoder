// Package storage reports where the state directory's disk space goes, how fast it grows and how long it lasts.
package storage

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/history"
	"github.com/PVKonovalov/df17-decoder/internal/tape"
)

const (
	day      = 24 * time.Hour
	cacheTTL = 60 * time.Second
)

// History is what the report reads from the flight history.
type History interface {
	File() string
	DBBytes() int64
	Retention() time.Duration
	SizeSamples(ctx context.Context, since time.Time) ([]history.SizeSample, error)
	Oldest(ctx context.Context) (flights, rollups time.Time, err error)
}

// Tape is what the report reads from the recorder and its files.
type Tape interface {
	Stats() tape.Stats
	BytesSince(since time.Time) (int64, time.Time)
}

// Sources are the inputs of a report; Tape and History are nil when disabled.
type Sources struct {
	StateDir      string
	Volume        func(dir string) (total, free uint64, ok bool) // Statfs when nil
	Tape          Tape
	TapeRetention time.Duration
	MinFreeBytes  uint64 // tape.minFreeMB
	History       History
	Now           func() time.Time
}

type Volume struct {
	TotalBytes *uint64 `json:"totalBytes,omitempty"`
	FreeBytes  *uint64 `json:"freeBytes,omitempty"`
}

type Usage struct {
	TapeBytes  *int64 `json:"tapeBytes,omitempty"` // nil when the tape is disabled
	DBBytes    *int64 `json:"dbBytes,omitempty"`   // nil when history is disabled
	OtherBytes int64  `json:"otherBytes"`          // other files directly in the state dir
}

// Growth is the rate new data arrives at, in bytes per day; nil when unknown.
type Growth struct {
	TapeBytesPerDay *float64 `json:"tapeBytesPerDay,omitempty"`
	DBBytesPerDay   *float64 `json:"dbBytesPerDay,omitempty"`
}

// Projection: retention days are nil when the data is kept forever (or disabled); steady states exist only with a
// retention; the day counts are nil when the space never runs out at the current growth.
type Projection struct {
	TapeRetentionDays    *float64 `json:"tapeRetentionDays,omitempty"`
	DBRetentionDays      *float64 `json:"dbRetentionDays,omitempty"`
	TapeSteadyStateBytes *float64 `json:"tapeSteadyStateBytes,omitempty"`
	DBSteadyStateBytes   *float64 `json:"dbSteadyStateBytes,omitempty"`
	DaysUntilFull        *float64 `json:"daysUntilFull,omitempty"`
	DaysUntilGuard       *float64 `json:"daysUntilGuard,omitempty"`
}

type Horizon struct {
	TapeOldest    *time.Time `json:"tapeOldest,omitempty"`
	FlightsOldest *time.Time `json:"flightsOldest,omitempty"`
	RollupsOldest *time.Time `json:"rollupsOldest,omitempty"`
}

// Flags: GuardActive means recording is paused for space; Warning that free space is below twice the guard's minimum.
type Flags struct {
	GuardActive          bool   `json:"guardActive"`
	GuardDeletedSegments uint64 `json:"guardDeletedSegments"`
	Warning              bool   `json:"warning"`
	MinFreeBytes         uint64 `json:"minFreeBytes"`
}

// Report is GET /api/storage.
type Report struct {
	Volume     Volume     `json:"volume"`
	Usage      Usage      `json:"usage"`
	Growth     Growth     `json:"growth"`
	Projection Projection `json:"projection"`
	Horizon    Horizon    `json:"horizon"`
	Flags      Flags      `json:"flags"`
}

// Reporter builds reports and caches them for 60 s.
type Reporter struct {
	src Sources
	mu  sync.Mutex
	at  time.Time
	rep Report
}

func NewReporter(src Sources) *Reporter {
	if src.Volume == nil {
		src.Volume = Statfs
	}
	if src.Now == nil {
		src.Now = time.Now
	}
	return &Reporter{src: src}
}

// Report returns the cached report, rebuilt when older than 60 s.
func (r *Reporter) Report(ctx context.Context) Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.src.Now()
	if r.at.IsZero() || now.Sub(r.at) >= cacheTTL || now.Before(r.at) {
		r.rep, r.at = r.src.build(ctx, now), now
	}
	return r.rep
}

func (src Sources) build(ctx context.Context, now time.Time) Report {
	var rep Report
	var free uint64
	freeKnown := false
	if total, f, ok := src.Volume(src.StateDir); ok {
		rep.Volume = Volume{TotalBytes: &total, FreeBytes: &f}
		free, freeKnown = f, true
	}
	rep.Flags.MinFreeBytes = src.MinFreeBytes

	var samples []history.SizeSample
	if src.Tape != nil {
		st := src.Tape.Stats()
		rep.Usage.TapeBytes = new(st.Bytes)
		rep.Flags.GuardActive, rep.Flags.GuardDeletedSegments = st.Paused, st.GuardDeleted
		if !st.Oldest.IsZero() {
			rep.Horizon.TapeOldest = new(st.Oldest.UTC())
		}
	}
	if src.History != nil {
		rep.Usage.DBBytes = new(src.History.DBBytes())
		samples, _ = src.History.SizeSamples(ctx, now.Add(-day))
		if f, r, err := src.History.Oldest(ctx); err == nil {
			if !f.IsZero() {
				rep.Horizon.FlightsOldest = &f
			}
			if !r.IsZero() {
				rep.Horizon.RollupsOldest = &r
			}
		}
	}
	rep.Usage.OtherBytes = src.otherBytes()

	if src.Tape != nil {
		rep.Growth.TapeBytesPerDay = growth(samples, func(s history.SizeSample) *int64 { return s.Tape })
		if rep.Growth.TapeBytesPerDay == nil {
			rep.Growth.TapeBytesPerDay = segmentGrowth(src.Tape, now)
		}
	}
	if src.History != nil {
		rep.Growth.DBBytesPerDay = growth(samples, func(s history.SizeSample) *int64 { return s.DB })
	}

	var histRetention time.Duration
	if src.History != nil {
		histRetention = src.History.Retention()
	}
	p := &rep.Projection
	type part struct {
		rate      *float64
		retention time.Duration
		current   *int64
		days      **float64
		steady    **float64
	}
	var unbounded, growing float64
	for _, c := range []part{
		{rep.Growth.TapeBytesPerDay, src.TapeRetention, rep.Usage.TapeBytes, &p.TapeRetentionDays, &p.TapeSteadyStateBytes},
		{rep.Growth.DBBytesPerDay, histRetention, rep.Usage.DBBytes, &p.DBRetentionDays, &p.DBSteadyStateBytes},
	} {
		if c.current == nil {
			continue // disabled
		}
		if c.retention <= 0 {
			if c.rate != nil {
				unbounded += *c.rate
				growing += *c.rate
			}
			continue
		}
		days := c.retention.Hours() / 24
		*c.days = &days
		if c.rate != nil {
			steady := *c.rate * days
			*c.steady = &steady
			if float64(*c.current) < steady {
				growing += *c.rate
			}
		}
	}
	if freeKnown && unbounded > 0 {
		p.DaysUntilFull = new(float64(free) / unbounded)
	}
	if freeKnown && src.Tape != nil && src.MinFreeBytes > 0 {
		switch {
		case free <= src.MinFreeBytes:
			p.DaysUntilGuard = new(0.0)
		case growing > 0:
			p.DaysUntilGuard = new(float64(free-src.MinFreeBytes) / growing)
		}
	}
	rep.Flags.Warning = freeKnown && src.MinFreeBytes > 0 && free < 2*src.MinFreeBytes
	return rep
}

// growth returns bytes per day from consecutive size samples: the sum of the increases (decreases are retention
// deleting old data, not growth) over the time they span; nil with less than an hour of samples.
func growth(samples []history.SizeSample, value func(history.SizeSample) *int64) *float64 {
	var sum int64
	var first, last time.Time
	var prev *int64
	for _, s := range samples {
		v := value(s)
		if v == nil {
			continue
		}
		if prev == nil {
			first = s.Hour
		} else if *v > *prev {
			sum += *v - *prev
		}
		prev, last = v, s.Hour
	}
	span := last.Sub(first)
	if prev == nil || span < time.Hour {
		return nil
	}
	return new(float64(sum) * float64(day) / float64(span))
}

// segmentGrowth estimates the tape's bytes per day from the segments of the last 24 hours; nil with less than an
// hour of them.
func segmentGrowth(tp Tape, now time.Time) *float64 {
	since := now.Add(-day)
	n, oldest := tp.BytesSince(since)
	if oldest.IsZero() {
		return nil
	}
	span := now.Sub(oldest)
	if span < time.Hour {
		return nil
	}
	return new(float64(n) * float64(day) / float64(span))
}

// otherBytes sums the regular files directly in the state dir, except the history database files.
func (src Sources) otherBytes() int64 {
	entries, err := os.ReadDir(src.StateDir)
	if err != nil {
		return 0
	}
	skip := map[string]bool{}
	if src.History != nil {
		if db, err := filepath.Abs(src.History.File()); err == nil {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				skip[db+suffix] = true
			}
		}
	}
	var n int64
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		p, err := filepath.Abs(filepath.Join(src.StateDir, e.Name()))
		if err != nil || skip[p] {
			continue
		}
		if fi, err := e.Info(); err == nil {
			n += fi.Size()
		}
	}
	return n
}
