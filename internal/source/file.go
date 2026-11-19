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

	var pace func(uint64) error
	if f.Opt.ReplaySpeed > 0 {
		pace = newPacer(ctx, f.Opt.ReplaySpeed)
	}
	return readAVR(ctx, fh, out, &f.stats, pace)
}

// newPacer returns a function that sleeps until a frame with 12 MHz timestamp ts is due, relative to the
// first timestamped frame. A timestamp going backwards (receiver restart) re-anchors the clock.
func newPacer(ctx context.Context, speed float64) func(ts uint64) error {
	var first uint64
	var start time.Time
	return func(ts uint64) error {
		if ts == 0 {
			return nil
		}
		if start.IsZero() || ts < first {
			first, start = ts, time.Now()
			return nil
		}
		due := start.Add(time.Duration(float64(ts-first) / clockHz / speed * float64(time.Second)))
		wait := time.Until(due)
		if wait <= 0 {
			return nil
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
