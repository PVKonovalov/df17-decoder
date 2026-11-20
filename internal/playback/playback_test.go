package playback

import (
	"cmp"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/source"
	"github.com/PVKonovalov/df17-decoder/internal/tape"
	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

type fakeWall struct {
	mu sync.Mutex
	t  time.Time
}

func (w *fakeWall) now() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.t
}

func (w *fakeWall) add(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.t = w.t.Add(d)
}

var t0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

func TestClock(t *testing.T) {
	w := &fakeWall{t: t0}
	c := NewClock(t0, w.now)
	w.add(time.Minute)
	if !c.Now().Equal(t0) {
		t.Error("a paused clock moved")
	}
	c.Play()
	w.add(10 * time.Second)
	if !c.Now().Equal(t0.Add(10 * time.Second)) {
		t.Errorf("1×: %v", c.Now())
	}
	c.SetSpeed(16)
	w.add(10 * time.Second)
	if !c.Now().Equal(t0.Add(170 * time.Second)) {
		t.Errorf("16×: %v", c.Now())
	}
	c.Pause()
	w.add(time.Hour)
	if !c.Now().Equal(t0.Add(170*time.Second)) || c.Playing() {
		t.Errorf("paused: %v", c.Now())
	}
	c.Set(t0)
	if !c.Now().Equal(t0) || c.Speed() != 16 {
		t.Errorf("set: %v", c.Now())
	}
}

func liveOptions() tracker.Options {
	return tracker.Options{
		TTL:             time.Minute,
		KnownAddressTTL: time.Minute,
		Resolver: tracker.PositionResolver{
			Receiver: geo.Point{Lat: 25.07, Lon: 22.87}, MaxRangeNm: 300, PairMaxAge: 10 * time.Second, RefMaxAge: time.Minute,
		},
		TrailWindow: 10 * time.Minute,
	}
}

func TestVirtualClockAgesTheTracker(t *testing.T) {
	w := &fakeWall{t: t0}
	p := NewPipeline(liveOptions(), modes.Options{}, t0)
	p.Clock = NewClock(t0, w.now)
	opt := liveOptions()
	opt.Clock = p.Clock.Now
	p.Tracker = tracker.New(opt)

	p.Tracker.Update(modes.Message{DF: 11, ICAO: 0x738ABC, Time: t0, AllCall: &modes.AllCall{}}, nil)
	w.add(2 * time.Hour) // paused far longer than the TTL
	p.Tracker.Expire()
	if len(p.Tracker.Snapshot()) != 1 {
		t.Fatal("pausing expired the aircraft")
	}

	p.Clock.SetSpeed(16)
	p.Clock.Play()
	w.add(3 * time.Second) // 48 s of data time
	p.Tracker.Expire()
	if len(p.Tracker.Snapshot()) != 1 {
		t.Fatal("expired before the TTL")
	}
	w.add(1 * time.Second) // 64 s
	if p.Tracker.Expire(); len(p.Tracker.Snapshot()) != 0 {
		t.Error("at 16× the aircraft must expire after TTL/16 of wall time")
	}
}

// capture generates a busy synthetic Beast capture with cmd/replaygen and returns its frames.
func capture(t *testing.T, minutes int) []source.Frame {
	t.Helper()
	if testing.Short() {
		t.Skip("generates a capture with go run")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	file := filepath.Join(t.TempDir(), "busy.bin")
	cmd := exec.Command(goBin, "run", "../../cmd/replaygen", "-n", "40", "-duration", strconv.Itoa(minutes*60), "-format", "beast", "-o", file)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("replaygen: %v\n%s", err, out)
	}
	src := &source.File{Path: file}
	out := make(chan source.Frame, 1<<20)
	if err := src.Run(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	close(out)
	var frames []source.Frame
	for f := range out {
		frames = append(frames, f)
	}
	return frames
}

func recordTape(t *testing.T, frames []source.Frame) *tape.Tape {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tape")
	rec, err := tape.New(tape.Options{Dir: dir, QueueSize: len(frames) + 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		rec.Record(f)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Run writes what is queued, then returns
	rec.Run(ctx)
	if st := rec.Stats(); st.FramesWritten != uint64(len(frames)) || st.Dropped != 0 {
		t.Fatalf("recorded %+v of %d frames", st, len(frames))
	}
	return tape.Open(dir)
}

// comparable drops the fields that count history older than the preroll.
func comparable(list []tracker.Aircraft) []tracker.Aircraft {
	slices.SortFunc(list, func(a, b tracker.Aircraft) int { return cmp.Compare(a.ICAO, b.ICAO) })
	for i := range list {
		list[i].FirstSeen, list[i].Messages, list[i].MessagesByDF = time.Time{}, 0, nil
	}
	return list
}

// TestRebuildMatchesLive is the key check of preroll and the virtual clock: the state rebuilt from the tape at T
// equals the state of a pipeline that decoded everything up to T.
func TestRebuildMatchesLive(t *testing.T) {
	frames := capture(t, 15)
	tp := recordTape(t, frames)
	start := frames[0].Time
	opt, dec := liveOptions(), modes.Options{}

	live := NewPipeline(opt, dec, start)
	i := 0
	for _, offset := range []time.Duration{3 * time.Minute, 11*time.Minute + 30*time.Second, 13 * time.Minute, 14*time.Minute + 50*time.Second} {
		at := start.Add(offset)
		for ; i < len(frames) && !frames[i].Time.After(at); i++ {
			live.Clock.Set(frames[i].Time)
			live.Feed(frames[i])
		}
		live.Clock.Set(at)
		live.ExpireAt(at)

		p, r, _, _, err := Rebuild(tp, at, opt, dec)
		if err != nil {
			t.Fatal(err)
		}
		r.Close()
		want, got := comparable(live.Tracker.Snapshot()), comparable(p.Tracker.Snapshot())
		if len(want) < 10 {
			t.Fatalf("at +%v: only %d live aircraft, the capture is too thin", offset, len(want))
		}
		// compared as JSON, what clients see: live times carry a monotonic reading that tape times don't
		if len(got) != len(want) {
			t.Fatalf("at +%v: %d live aircraft, %d rebuilt", offset, len(want), len(got))
		}
		for k := range want {
			if a, b := jsonOf(t, want[k]), jsonOf(t, got[k]); a != b {
				t.Fatalf("at +%v: aircraft differ\n live     %s\n playback %s", offset, a, b)
			}
		}
		if a, b := jsonOf(t, live.Tracker.Trails()), jsonOf(t, p.Tracker.Trails()); a != b {
			t.Fatalf("at +%v: trails differ", offset)
		}
	}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// BenchmarkPipeline measures decoding throughput from frames already in memory (60× real time is ~3,300/s).
func BenchmarkPipeline(b *testing.B) {
	frames := benchFrames(b)
	n := 0
	for b.Loop() {
		p := NewPipeline(liveOptions(), modes.Options{}, frames[0].Time)
		for _, f := range frames {
			p.Clock.Set(f.Time)
			p.Feed(f)
		}
		n += len(frames)
	}
	b.ReportMetric(float64(n)/b.Elapsed().Seconds(), "frames/s")
}

func benchFrames(b *testing.B) []source.Frame {
	b.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		b.Skip("go not on PATH")
	}
	file := filepath.Join(b.TempDir(), "busy.bin")
	if out, err := exec.Command(goBin, "run", "../../cmd/replaygen", "-n", "150", "-duration", "120", "-format", "beast", "-o", file).CombinedOutput(); err != nil {
		b.Fatalf("replaygen: %v\n%s", err, out)
	}
	out := make(chan source.Frame, 1<<20)
	if err := (&source.File{Path: file}).Run(context.Background(), out); err != nil {
		b.Fatal(err)
	}
	close(out)
	var frames []source.Frame
	for f := range out {
		frames = append(frames, f)
	}
	return frames
}
