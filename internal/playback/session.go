package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/tape"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

// Speeds are the allowed playback speeds.
var Speeds = []float64{1, 2, 4, 8, 16, 60}

var (
	ErrTooManySessions = errors.New("too many playback sessions")
	ErrOutOfRange      = errors.New("time is outside the tape")
	ErrBadSpeed        = fmt.Errorf("speed must be one of %v", Speeds)
)

// Manager hands out playback sessions over one tape and enforces the session limit.
type Manager struct {
	Tape        *tape.Tape
	Tracker     tracker.Options // the live tracker's options; sessions drop their side effects
	Decode      modes.Options
	MaxSessions int
	IdleTimeout time.Duration // a session paused this long frees its tracker; the next play re-seeks
	EndMargin   time.Duration // playback within this of now switches back to live
	Bounds      func() (oldest, newest time.Time)
	Logger      *slog.Logger
	Now         func() time.Time // wall clock; time.Now when nil

	mu     sync.Mutex
	active int
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) acquire() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active >= m.MaxSessions {
		return fmt.Errorf("%w (playback.maxSessions is %d)", ErrTooManySessions, m.MaxSessions)
	}
	m.active++
	return nil
}

func (m *Manager) release() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active--
}

// Active returns the number of sessions holding a tracker.
func (m *Manager) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

// Gap is a stretch of silence the playback jumped over.
type Gap struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Status is a session's state for the "playback" message.
type Status struct {
	T       time.Time
	Speed   float64
	Playing bool
	Gap     *Gap   // the last gap skipped
	GapSeq  uint64 // increments with every skipped gap
	Seq     uint64 // increments with every seek: the tracker was rebuilt, send full trails
	Ended   bool   // caught up with now: back to live
	Tracker *tracker.Tracker
}

type command struct {
	action string
	t      time.Time
	speed  float64
	step   time.Duration
	reply  chan error
}

// Session plays the tape through its own pipeline. Its methods are safe for concurrent use; Close ends it.
type Session struct {
	m       *Manager
	cmds    chan command
	changed chan struct{}
	done    chan struct{}
	cancel  context.CancelFunc

	mu      sync.Mutex
	st      Status
	p       *Pipeline // nil while freed (idle) or ended
	holding bool      // counts against MaxSessions
}

// Start seeks a new session to t and starts playing at speed 1. It fails when the session limit is reached or
// t is outside the tape.
func (m *Manager) Start(ctx context.Context, t time.Time) (*Session, error) {
	if err := m.inRange(t); err != nil {
		return nil, err
	}
	if err := m.acquire(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Session{
		m: m, cmds: make(chan command), changed: make(chan struct{}, 1), done: make(chan struct{}), cancel: cancel,
		st: Status{Speed: 1, Playing: true}, holding: true,
	}
	r, next, ok, err := s.seek(t)
	if err != nil {
		cancel()
		m.release()
		return nil, err
	}
	go s.run(ctx, r, next, ok)
	return s, nil
}

func (m *Manager) inRange(t time.Time) error {
	oldest, _ := m.Bounds()
	if oldest.IsZero() || t.Before(oldest) || t.After(m.now()) {
		return fmt.Errorf("%w: %s", ErrOutOfRange, t.UTC().Format(time.RFC3339))
	}
	return nil
}

// Changed signals (coalesced) that the state changed outside the regular cadence: a seek, a pause, the end.
func (s *Session) Changed() <-chan struct{} { return s.changed }

// Status returns the current state; Tracker is nil while the session is freed or ended.
func (s *Session) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.st
	if s.p != nil {
		st.T = s.p.Clock.Now()
		st.Tracker = s.p.Tracker
	}
	return st
}

// Close ends the session and frees its slot.
func (s *Session) Close() {
	s.cancel()
	<-s.done
}

func (s *Session) Play() error                  { return s.do(command{action: "play"}) }
func (s *Session) Pause() error                 { return s.do(command{action: "pause"}) }
func (s *Session) Seek(t time.Time) error       { return s.do(command{action: "seek", t: t}) }
func (s *Session) Step(d time.Duration) error   { return s.do(command{action: "step", step: d}) }
func (s *Session) SetSpeed(speed float64) error { return s.do(command{action: "speed", speed: speed}) }

func (s *Session) do(c command) error {
	c.reply = make(chan error, 1)
	select {
	case s.cmds <- c:
		return <-c.reply
	case <-s.done:
		return errors.New("the playback session has ended")
	}
}

func (s *Session) notify() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// seek rebuilds the pipeline at t, keeping speed and play state.
func (s *Session) seek(t time.Time) (*tape.Reader, source.Frame, bool, error) {
	p, r, next, ok, err := Rebuild(s.m.Tape, t, s.m.Tracker, s.m.Decode)
	if err != nil {
		return nil, source.Frame{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Clock.SetSpeed(s.st.Speed)
	if s.st.Playing {
		p.Clock.Play()
	}
	s.p = p
	s.st.Seq++
	return r, next, ok, nil
}

func (s *Session) run(ctx context.Context, r *tape.Reader, next source.Frame, ok bool) {
	defer close(s.done)
	defer func() {
		if r != nil {
			r.Close()
		}
		s.mu.Lock()
		s.p = nil
		holding := s.holding
		s.holding = false
		s.mu.Unlock()
		if holding {
			s.m.release()
		}
	}()

	var pausedAt time.Time  // wall time of the pause, for the idle timeout
	reseekAt := time.Time{} // wall time to look for newly flushed tape after the end of the reader
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		s.mu.Lock()
		p, playing := s.p, s.st.Playing
		s.mu.Unlock()

		wait := time.Second
		if p != nil && playing {
			now := p.Clock.Now()
			if !ok && !time.Now().Before(reseekAt) {
				r.Close()
				r = s.m.Tape.Seek(now.Add(time.Nanosecond))
				next, ok = s.readNext(r)
				reseekAt = time.Now().Add(time.Second)
			}
			if ok && next.Time.Sub(now) > tape.GapThreshold {
				s.mu.Lock()
				s.st.Gap, s.st.GapSeq = &Gap{From: now.UTC(), To: next.Time.UTC()}, s.st.GapSeq+1
				p.Clock.Set(next.Time)
				s.mu.Unlock()
				now = next.Time
				s.notify()
			}
			for ok && !next.Time.After(now) {
				p.Feed(next)
				next, ok = s.readNext(r)
			}
			p.ExpireAt(now)
			if !now.Before(s.m.now().Add(-s.m.EndMargin)) {
				s.mu.Lock()
				s.st.Ended, s.st.Playing = true, false
				s.mu.Unlock()
				s.notify()
				return
			}
			if ok {
				wait = min(wait, time.Duration(float64(next.Time.Sub(now))/p.Clock.Speed())+time.Millisecond)
			}
		} else if p != nil && s.m.IdleTimeout > 0 && !pausedAt.IsZero() {
			if idle := time.Since(pausedAt); idle >= s.m.IdleTimeout {
				s.free()
				r.Close()
				r, ok = nil, false
			} else {
				wait = s.m.IdleTimeout - idle
			}
		}
		timer.Reset(wait)

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case c := <-s.cmds:
			var err error
			r, next, ok, err = s.apply(c, r, next, ok)
			pausedAt = time.Time{}
			if !s.Status().Playing {
				pausedAt = time.Now() // any command restarts the idle timeout
			}
			c.reply <- err
			s.notify()
		}
	}
}

func (s *Session) readNext(r *tape.Reader) (source.Frame, bool) {
	if r == nil {
		return source.Frame{}, false
	}
	f, _, err := r.Next()
	if err != nil {
		if !errors.Is(err, io.EOF) && s.m.Logger != nil {
			s.m.Logger.Warn("playback: tape read failed", "err", err)
		}
		return source.Frame{}, false
	}
	return f, true
}

// free drops the tracker of an idle session and its slot; the clock time is kept for the next play.
func (s *Session) free() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p != nil {
		s.st.T = s.p.Clock.Now()
	}
	s.p = nil
	if s.holding {
		s.holding = false
		s.m.release()
	}
}

func (s *Session) apply(c command, r *tape.Reader, next source.Frame, ok bool) (*tape.Reader, source.Frame, bool, error) {
	s.mu.Lock()
	p, cur := s.p, s.st.T
	if p != nil {
		cur = p.Clock.Now()
	}
	s.mu.Unlock()

	target, reseek := cur, p == nil
	switch c.action {
	case "play":
		s.mu.Lock()
		s.st.Playing = true
		if p != nil {
			p.Clock.Play()
		}
		s.mu.Unlock()
	case "pause":
		s.mu.Lock()
		s.st.Playing = false
		if p != nil {
			p.Clock.Pause()
		}
		s.mu.Unlock()
		return r, next, ok, nil // a freed session stays freed while paused
	case "speed":
		if !slices.Contains(Speeds, c.speed) {
			return r, next, ok, ErrBadSpeed
		}
		s.mu.Lock()
		s.st.Speed = c.speed
		if p != nil {
			p.Clock.SetSpeed(c.speed)
		}
		s.mu.Unlock()
		return r, next, ok, nil
	case "seek":
		if err := s.m.inRange(c.t); err != nil {
			return r, next, ok, err
		}
		target, reseek = c.t, true
	case "step":
		oldest, _ := s.m.Bounds()
		target, reseek = cur.Add(c.step), true
		if target.Before(oldest) {
			target = oldest
		}
		if now := s.m.now(); target.After(now) {
			target = now
		}
	default:
		return r, next, ok, fmt.Errorf("unknown action %q", c.action)
	}
	if !reseek {
		return r, next, ok, nil
	}
	if p == nil {
		if err := s.m.acquire(); err != nil {
			return r, next, ok, err
		}
		s.mu.Lock()
		s.holding = true
		s.mu.Unlock()
	}
	nr, nnext, nok, err := s.seek(target)
	if err != nil {
		return r, next, ok, err
	}
	if r != nil {
		r.Close()
	}
	return nr, nnext, nok, nil
}
