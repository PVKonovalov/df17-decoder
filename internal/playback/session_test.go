package playback

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/stats"
	"github.com/PVKonovalov/df17-decoder/internal/tape"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

// dummyFrames returns one undecodable frame per second offset after t0.
func dummyFrames(secs ...int) []source.Frame {
	out := make([]source.Frame, len(secs))
	for i, s := range secs {
		out[i] = source.Frame{Data: make([]byte, 7), Time: t0.Add(time.Duration(s) * time.Second)}
	}
	return out
}

func newManager(tp *tape.Tape, frames []source.Frame, now time.Time) *Manager {
	return &Manager{
		Tape: tp, Tracker: liveOptions(), MaxSessions: 2, EndMargin: time.Second,
		Bounds: func() (time.Time, time.Time) { return frames[0].Time, frames[len(frames)-1].Time },
		Now:    func() time.Time { return now },
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSession_Limits(t *testing.T) {
	frames := dummyFrames(0, 1, 2, 3600)
	m := newManager(recordTape(t, frames), frames, t0.Add(2*time.Hour))
	ctx := context.Background()
	if _, err := m.Start(ctx, t0.Add(-time.Second)); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("before the tape: %v", err)
	}
	if _, err := m.Start(ctx, t0.Add(3*time.Hour)); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("in the future: %v", err)
	}
	a, err := m.Start(ctx, t0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Start(ctx, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(ctx, t0); !errors.Is(err, ErrTooManySessions) {
		t.Errorf("third session: %v", err)
	}
	a.Close()
	c, err := m.Start(ctx, t0)
	if err != nil {
		t.Fatalf("after a close: %v", err)
	}
	b.Close()
	c.Close()
	if m.Active() != 0 {
		t.Errorf("active = %d", m.Active())
	}
	if err := c.Play(); err == nil {
		t.Error("a closed session accepted a command")
	}
}

func TestSession_Commands(t *testing.T) {
	frames := dummyFrames(0, 1, 2, 30, 3600)
	m := newManager(recordTape(t, frames), frames, t0.Add(2*time.Hour))
	s, err := m.Start(context.Background(), t0.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Pause(); err != nil {
		t.Fatal(err)
	}
	st := s.Status()
	if st.Playing || st.Seq != 1 || st.Tracker == nil {
		t.Errorf("after pause: %+v", st)
	}
	paused := st.T
	time.Sleep(20 * time.Millisecond)
	if !s.Status().T.Equal(paused) {
		t.Error("a paused session's clock moved")
	}
	if err := s.Step(-5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); st.Seq != 2 || !st.T.Equal(paused.Add(-5*time.Second)) || st.Playing {
		t.Errorf("after step: %+v", st)
	}
	if err := s.Step(-time.Hour); err != nil || !s.Status().T.Equal(t0) {
		t.Errorf("step before the tape clamps to its start: %v %v", err, s.Status().T)
	}
	if err := s.SetSpeed(3); !errors.Is(err, ErrBadSpeed) {
		t.Errorf("speed 3: %v", err)
	}
	if err := s.SetSpeed(16); err != nil || s.Status().Speed != 16 {
		t.Errorf("speed 16: %v", err)
	}
	if err := s.Seek(t0.Add(5 * time.Hour)); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("seek past now: %v", err)
	}
	if err := s.Seek(t0.Add(20 * time.Second)); err != nil || s.Status().Seq != 4 {
		t.Errorf("seek: %v %+v", err, s.Status())
	}
	if err := s.Play(); err != nil || !s.Status().Playing {
		t.Errorf("play: %v", err)
	}
	waitFor(t, "the clock to run at 16×", func() bool { return s.Status().T.After(t0.Add(21 * time.Second)) })
}

func TestSession_SkipsGaps(t *testing.T) {
	frames := dummyFrames(0, 1, 2, 300, 301)
	m := newManager(recordTape(t, frames), frames, t0.Add(time.Hour))
	s, err := m.Start(context.Background(), t0.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	waitFor(t, "the gap skip", func() bool { return s.Status().GapSeq == 1 })
	st := s.Status()
	if !st.Gap.To.Equal(t0.Add(300*time.Second)) || st.Gap.From.After(t0.Add(3*time.Second)) || st.T.Before(st.Gap.To) {
		t.Errorf("gap %+v at %v", st.Gap, st.T)
	}
}

func TestSession_EndsAtNow(t *testing.T) {
	frames := dummyFrames(0, 1, 2, 3, 4, 5)
	m := newManager(recordTape(t, frames), frames, t0.Add(6*time.Second))
	s, err := m.Start(context.Background(), t0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetSpeed(60); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Changed(): // the speed change
	default:
	}
	waitFor(t, "the end", func() bool { return s.Status().Ended })
	if st := s.Status(); st.Tracker != nil || st.Playing || m.Active() != 0 {
		t.Errorf("ended session %+v, active %d", st, m.Active())
	}
	select {
	case <-s.Changed():
	default:
		t.Error("the end was not signalled")
	}
}

func TestSession_IdleTimeoutFreesAndPlayReseeks(t *testing.T) {
	frames := dummyFrames(0, 1, 2, 3600)
	m := newManager(recordTape(t, frames), frames, t0.Add(2*time.Hour))
	m.IdleTimeout = 50 * time.Millisecond
	s, err := m.Start(context.Background(), t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Pause(); err != nil {
		t.Fatal(err)
	}
	at := s.Status().T
	waitFor(t, "the idle session to be freed", func() bool { return m.Active() == 0 })
	if st := s.Status(); st.Tracker != nil || !st.T.Equal(at) {
		t.Errorf("freed session %+v", st)
	}
	if err := s.Play(); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); st.Tracker == nil || st.Seq != 2 || m.Active() != 1 || st.T.Before(at) {
		t.Errorf("after play: %+v, active %d", st, m.Active())
	}
}

type countingSink struct{ n atomic.Int64 }

func (c *countingSink) Flight(tracker.FlightInfo)                { c.n.Add(1) }
func (c *countingSink) Point(int64, tracker.TrackPoint)          { c.n.Add(1) }
func (c *countingSink) Sample(int64, tracker.Sample)             { c.n.Add(1) }
func (c *countingSink) Quality(int64, tracker.QualityChange)     { c.n.Add(1) }
func (c *countingSink) Add(geo.Point, *int, *float64, time.Time) { c.n.Add(1) }
func (c *countingSink) count() int64                             { return c.n.Load() }

func TestSession_NoSideEffects(t *testing.T) {
	frames := capture(t, 3)
	live := stats.New(time.Now())
	sink := &countingSink{}
	opt := liveOptions()
	opt.Stats, opt.Flights, opt.Coverage, opt.LastFlightID = live, sink, sink, 41
	m := &Manager{
		Tape: recordTape(t, frames), Tracker: opt, Decode: modes.Options{}, MaxSessions: 1, EndMargin: time.Second,
		Bounds: func() (time.Time, time.Time) { return frames[0].Time, frames[len(frames)-1].Time },
		Now:    func() time.Time { return frames[len(frames)-1].Time.Add(time.Hour) },
	}
	s, err := m.Start(context.Background(), frames[0].Time.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetSpeed(60); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aircraft", func() bool { return len(s.Status().Tracker.Snapshot()) > 5 })
	if sink.count() != 0 || live.Snapshot(time.Now()).Messages != 0 || live.Snapshot(time.Now()).Positions != 0 {
		t.Errorf("playback wrote to the live sinks: %d calls, stats %+v", sink.count(), live.Snapshot(time.Now()))
	}
}
