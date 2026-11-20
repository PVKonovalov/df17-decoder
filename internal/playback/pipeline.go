package playback

import (
	"errors"
	"io"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/stats"
	"github.com/PVKonovalov/df17-decoder/internal/tape"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

// Pipeline decodes frames into its own tracker the way the live service does, aged by a virtual clock. It has no
// side effects: no flight sink, no coverage recorder, its own stats.
type Pipeline struct {
	Tracker    *tracker.Tracker
	Clock      *Clock
	decode     modes.Options
	nextExpire time.Time
}

// NewPipeline builds a pipeline with the live tracker options opt (minus their side effects) and decoder options
// dec, with its clock paused at at.
func NewPipeline(opt tracker.Options, dec modes.Options, at time.Time) *Pipeline {
	clock := NewClock(at, nil)
	opt.Flights, opt.Coverage, opt.LastFlightID = nil, nil, 0
	opt.Stats = stats.New(time.Now())
	opt.Clock = clock.Now
	return &Pipeline{Tracker: tracker.New(opt), Clock: clock, decode: dec}
}

// Feed decodes f into the tracker. The caller sets the clock first.
func (p *Pipeline) Feed(f source.Frame) {
	if m, err := modes.Decode(f.Data, f.Time, p.decode); err == nil {
		p.Tracker.Update(m, f.RSSI)
	}
	p.ExpireAt(f.Time)
}

// ExpireAt runs the tracker's expiry once per second of data time, as the live service's one-second ticker does.
func (p *Pipeline) ExpireAt(t time.Time) {
	if !t.Before(p.nextExpire) {
		p.Tracker.Expire()
		p.nextExpire = t.Truncate(time.Second).Add(time.Second)
	}
}

// Preroll is how much tape is decoded before a seek point so aircraft and trails there look as they did live.
func Preroll(opt tracker.Options) time.Duration {
	return max(opt.TrailWindow, opt.TTL) + time.Minute
}

// Rebuild returns a pipeline holding the state at t: it decodes the tape from t - Preroll(opt) up to t at full
// speed, leaves the clock paused at t, and returns the reader with the first frame after t (ok false at the end
// of the tape).
func Rebuild(tp *tape.Tape, t time.Time, opt tracker.Options, dec modes.Options) (p *Pipeline, r *tape.Reader, next source.Frame, ok bool, err error) {
	if opt.TrailWindow <= 0 {
		opt.TrailWindow = 10 * time.Minute
	}
	p = NewPipeline(opt, dec, t.Add(-Preroll(opt)))
	r = tp.Seek(t.Add(-Preroll(opt)))
	for {
		f, _, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			r.Close()
			return nil, nil, source.Frame{}, false, err
		}
		if f.Time.After(t) {
			next, ok = f, true
			break
		}
		p.Clock.Set(f.Time)
		p.Feed(f)
	}
	p.Clock.Set(t)
	p.ExpireAt(t)
	return p, r, next, ok, nil
}
