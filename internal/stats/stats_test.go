package stats

import (
	"sync"
	"testing"
	"time"
)

func TestRateWindow(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	s := New(t0)
	for i := range 120 { // 2 messages per second for 60 s
		s.AddMessage(t0.Add(time.Duration(i) * 500 * time.Millisecond))
	}
	now := t0.Add(59 * time.Second) // window = seconds 0..59
	if r := s.Snapshot(now).MessagesPerSecond; r != 2 {
		t.Errorf("rate = %v, want 2", r)
	}
	if r := s.Snapshot(now.Add(30 * time.Second)).MessagesPerSecond; r != 1 {
		t.Errorf("rate after 30 s idle = %v, want 1 (half the window)", r)
	}
	if r := s.Snapshot(now.Add(5 * time.Minute)).MessagesPerSecond; r != 0 {
		t.Errorf("rate long after = %v, want 0", r)
	}
}

func TestCountersAndSnapshotCopy(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	s := New(t0)
	s.AddFrame(17)
	s.AddFrame(17)
	s.AddFrame(4)
	s.CRCFailures.Add(1)

	snap := s.Snapshot(t0.Add(time.Minute))
	if snap.Frames != 3 || snap.FramesPerDF[17] != 2 || snap.FramesPerDF[4] != 1 || snap.CRCFailures != 1 || snap.Uptime != time.Minute {
		t.Errorf("snapshot = %+v", snap)
	}
	snap.FramesPerDF[17] = 99
	if s.Snapshot(t0).FramesPerDF[17] != 2 {
		t.Error("snapshot map aliases internal state")
	}
}

func TestConcurrentUse(t *testing.T) {
	s := New(time.Now())
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 1000 {
			s.AddFrame(17)
			s.AddMessage(time.Now())
		}
	})
	wg.Go(func() {
		for range 1000 {
			_ = s.Snapshot(time.Now())
		}
	})
	wg.Wait()
	if s.Frames.Load() != 1000 {
		t.Errorf("frames = %d", s.Frames.Load())
	}
}
