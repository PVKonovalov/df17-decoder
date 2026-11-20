package storage

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/history"
	"github.com/PVKonovalov/df17-decoder/internal/tape"
)

var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

const (
	mb = 1 << 20
	gb = 1 << 30
)

type fakeTape struct {
	st      tape.Stats
	since   int64
	sinceAt time.Time
}

func (f fakeTape) Stats() tape.Stats                       { return f.st }
func (f fakeTape) BytesSince(time.Time) (int64, time.Time) { return f.since, f.sinceAt }

type fakeHistory struct {
	file      string
	bytes     int64
	retention time.Duration
	samples   []history.SizeSample
	oldest    time.Time
}

func (f fakeHistory) File() string             { return f.file }
func (f fakeHistory) DBBytes() int64           { return f.bytes }
func (f fakeHistory) Retention() time.Duration { return f.retention }
func (f fakeHistory) SizeSamples(context.Context, time.Time) ([]history.SizeSample, error) {
	return f.samples, nil
}
func (f fakeHistory) Oldest(context.Context) (time.Time, time.Time, error) {
	return f.oldest, f.oldest, nil
}

func volume(total, free uint64) func(string) (uint64, uint64, bool) {
	return func(string) (uint64, uint64, bool) { return total, free, true }
}

// samples returns hourly samples from now-23h, with db and tape at the given values.
func samples(db, tp []int64) []history.SizeSample {
	var out []history.SizeSample
	for i := range db {
		out = append(out, history.SizeSample{Hour: now.Add(time.Duration(i-len(db)) * time.Hour), DB: &db[i], Tape: &tp[i]})
	}
	return out
}

func near(p *float64, want float64) bool { return p != nil && math.Abs(*p-want) < want*1e-9+1e-9 }

func TestReport_GrowthAndProjection(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "coverage.json"), make([]byte, 300), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"history.db", "history.db-wal"} {
		if err := os.WriteFile(filepath.Join(dir, f), make([]byte, 1000), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// tape: +10 MB per hour, the retention deletes 30 MB at 2 h; db: +2 MB per hour
	src := Sources{
		StateDir: dir, Volume: volume(100*gb, 20*gb), MinFreeBytes: 1024 * mb, TapeRetention: 14 * 24 * time.Hour,
		Tape: fakeTape{st: tape.Stats{Bytes: 600 * mb, Oldest: now.Add(-72 * time.Hour), GuardDeleted: 2}},
		History: fakeHistory{file: filepath.Join(dir, "history.db"), bytes: 50 * mb, oldest: now.Add(-48 * time.Hour),
			samples: samples([]int64{10 * mb, 12 * mb, 14 * mb, 16 * mb}, []int64{100 * mb, 110 * mb, 90 * mb, 100 * mb})},
		Now: func() time.Time { return now },
	}
	rep := NewReporter(src).Report(t.Context())

	if rep.Usage.OtherBytes != 300 || *rep.Usage.DBBytes != 50*mb || *rep.Usage.TapeBytes != 600*mb {
		t.Errorf("usage %+v", rep.Usage)
	}
	if !near(rep.Growth.TapeBytesPerDay, 20*mb*8) || !near(rep.Growth.DBBytesPerDay, 6*mb*8) {
		t.Errorf("growth tape %v db %v", *rep.Growth.TapeBytesPerDay, *rep.Growth.DBBytesPerDay)
	}
	p := rep.Projection
	if !near(p.TapeRetentionDays, 14) || !near(p.TapeSteadyStateBytes, 160*mb*14) || p.DBRetentionDays != nil || p.DBSteadyStateBytes != nil {
		t.Errorf("steady states %+v", p)
	}
	// history keeps everything: it alone fills the disk; both still grow towards the guard
	if !near(p.DaysUntilFull, float64(20*gb)/float64(48*mb)) || !near(p.DaysUntilGuard, float64(20*gb-1024*mb)/float64(208*mb)) {
		t.Errorf("days full %v guard %v", *p.DaysUntilFull, *p.DaysUntilGuard)
	}
	if rep.Flags.Warning || rep.Flags.GuardActive || rep.Flags.GuardDeletedSegments != 2 || rep.Horizon.TapeOldest == nil || rep.Horizon.FlightsOldest == nil {
		t.Errorf("flags %+v horizon %+v", rep.Flags, rep.Horizon)
	}
}

func TestReport_UnknownAndFallbacks(t *testing.T) {
	// no samples: the tape falls back to its segments (12 h of 30 MB), the db growth stays unknown
	src := Sources{
		StateDir: t.TempDir(), Volume: func(string) (uint64, uint64, bool) { return 0, 0, false },
		Tape:    fakeTape{st: tape.Stats{Bytes: 30 * mb}, since: 30 * mb, sinceAt: now.Add(-12 * time.Hour)},
		History: fakeHistory{bytes: 1 * mb, retention: 720 * time.Hour},
		Now:     func() time.Time { return now },
	}
	rep := NewReporter(src).Report(t.Context())
	if !near(rep.Growth.TapeBytesPerDay, 60*mb) || rep.Growth.DBBytesPerDay != nil {
		t.Errorf("growth %+v", rep.Growth)
	}
	if rep.Volume.TotalBytes != nil || rep.Projection.DaysUntilFull != nil || rep.Projection.DaysUntilGuard != nil || rep.Flags.Warning {
		t.Errorf("unknown volume gave %+v %+v", rep.Projection, rep.Flags)
	}
	if !near(rep.Projection.DBRetentionDays, 30) || rep.Projection.DBSteadyStateBytes != nil || rep.Projection.TapeSteadyStateBytes != nil {
		t.Errorf("projection %+v", rep.Projection)
	}

	// a tape younger than an hour: unknown; everything disabled: only the volume and other files
	src.Tape = fakeTape{since: mb, sinceAt: now.Add(-10 * time.Minute)}
	if rep := NewReporter(src).Report(t.Context()); rep.Growth.TapeBytesPerDay != nil {
		t.Errorf("young tape growth %v", *rep.Growth.TapeBytesPerDay)
	}
	off := NewReporter(Sources{StateDir: t.TempDir(), Volume: volume(gb, gb/2), Now: src.Now}).Report(t.Context())
	if off.Usage.TapeBytes != nil || off.Usage.DBBytes != nil || off.Projection != (Projection{}) || off.Volume.FreeBytes == nil {
		t.Errorf("disabled %+v", off)
	}
}

func TestReport_GuardAndWarning(t *testing.T) {
	base := Sources{StateDir: t.TempDir(), MinFreeBytes: 1024 * mb, Now: func() time.Time { return now },
		Tape: fakeTape{st: tape.Stats{Bytes: gb, Paused: true}, since: 24 * mb, sinceAt: now.Add(-24 * time.Hour)}}
	for _, c := range []struct {
		free         uint64
		warning      bool
		guardDays    float64
		hasGuardDays bool
	}{
		{4096 * mb, false, 3072, true}, // retention 0: the tape grows 24 MB/day until the guard
		{2000 * mb, true, 976, true},   // below twice the minimum
		{500 * mb, true, 0, true},      // already at the guard
	} {
		src := base
		src.Volume = volume(100*gb, c.free)
		rep := NewReporter(src).Report(t.Context())
		if rep.Flags.Warning != c.warning || !rep.Flags.GuardActive || (rep.Projection.DaysUntilGuard != nil) != c.hasGuardDays ||
			(c.hasGuardDays && !near(rep.Projection.DaysUntilGuard, c.guardDays/24)) {
			t.Errorf("free %d MB: %+v %+v", c.free/mb, rep.Flags, rep.Projection)
		}
	}
}

func TestReporter_Caches(t *testing.T) {
	clock := now
	calls := 0
	r := NewReporter(Sources{StateDir: t.TempDir(), Now: func() time.Time { return clock },
		Volume: func(string) (uint64, uint64, bool) { calls++; return gb, gb, true }})
	r.Report(t.Context())
	clock = clock.Add(59 * time.Second)
	r.Report(t.Context())
	if calls != 1 {
		t.Errorf("rebuilt within 60 s: %d", calls)
	}
	clock = clock.Add(time.Second)
	r.Report(t.Context())
	if calls != 2 {
		t.Errorf("not rebuilt after 60 s: %d", calls)
	}
}

func TestStatfs(t *testing.T) {
	total, free, ok := Statfs(t.TempDir())
	if ok && (total == 0 || free > total) {
		t.Errorf("total %d free %d", total, free)
	}
}
