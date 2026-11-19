package source

import (
	"context"
	"os"
	"time"
)

const clockHz = 12_000_000 // dump1090 AVR/Beast timestamp clock

// File replays AVR lines from a file. Lines with @-timestamps are paced by Opt.ReplaySpeed;
// lines without timestamps are emitted immediately.
type File struct {
	Path  string
	Opt   Options
	stats Stats
}

func (f *File) Stats() *Stats { return &f.stats }

// Run returns nil at end of file.
func (f *File) Run(ctx context.Context, out chan<- Frame) error {
	fh, err := os.Open(f.Path)
	if err != nil {
		return err
	}
	defer fh.Close()

	f.stats.connected.Store(true)
	defer f.stats.connected.Store(false)

	return readAVR(ctx, fh, out, &f.stats, newReplayClock(ctx, f.Opt.ReplaySpeed))
}

// newReplayClock returns a function giving each frame its time in capture time: the wall clock at the first
// @-timestamped frame plus the elapsed 12 MHz ticks since then, independent of replay speed, so CPR pair ages
// and speed checks see the original timing. Lines without a timestamp get the time of the last timestamped one
// (or the wall clock before any). With speed > 0 it also sleeps until the frame is due; a timestamp going
// backwards (receiver restart) re-anchors the clock.
func newReplayClock(ctx context.Context, speed float64) func(ts uint64) (time.Time, error) {
	var first uint64
	var start, last time.Time
	return func(ts uint64) (time.Time, error) {
		if ts == 0 {
			if last.IsZero() {
				return time.Now(), nil
			}
			return last, nil
		}
		if start.IsZero() || ts < first {
			first, start = ts, time.Now()
			last = start
			return last, nil
		}
		elapsed := time.Duration(float64(ts-first) / clockHz * float64(time.Second))
		last = start.Add(elapsed)
		if speed <= 0 {
			return last, nil
		}

		wait := time.Until(start.Add(time.Duration(float64(elapsed) / speed)))
		if wait <= 0 {
			return last, nil
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
			return last, nil
		case <-ctx.Done():
			return time.Time{}, ctx.Err()
		}
	}
}
