// Package playback replays the tape through a separate decoder pipeline per session.
package playback

import (
	"sync"
	"time"
)

// Clock is a virtual clock: it stands still while paused and runs at speed × wall time while playing.
type Clock struct {
	mu       sync.Mutex
	wall     func() time.Time
	base     time.Time // virtual time at wallBase
	wallBase time.Time
	speed    float64
	playing  bool
}

// NewClock returns a paused clock at t, at speed 1; wall is time.Now when nil.
func NewClock(t time.Time, wall func() time.Time) *Clock {
	if wall == nil {
		wall = time.Now
	}
	return &Clock{wall: wall, base: t, wallBase: wall(), speed: 1}
}

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nowLocked()
}

func (c *Clock) nowLocked() time.Time {
	if !c.playing {
		return c.base
	}
	return c.base.Add(time.Duration(float64(c.wall().Sub(c.wallBase)) * c.speed))
}

// rebase restarts the wall reference at the current virtual time.
func (c *Clock) rebase() {
	c.base, c.wallBase = c.nowLocked(), c.wall()
}

// Set moves the clock to t, keeping it playing or paused.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.base, c.wallBase = t, c.wall()
}

func (c *Clock) Play() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rebase()
	c.playing = true
}

func (c *Clock) Pause() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rebase()
	c.playing = false
}

func (c *Clock) SetSpeed(speed float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rebase()
	c.speed = speed
}

func (c *Clock) Playing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.playing
}

func (c *Clock) Speed() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.speed
}
