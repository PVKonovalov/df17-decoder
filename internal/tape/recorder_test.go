package tape

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/source"
)

var t0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

func frame(at time.Time, i int) source.Frame {
	data := []byte{0x8D, 0x48, 0x40, 0xD6, 0x20, 0x2C, 0xC3, 0x71, 0xC3, 0x2C, 0xE0, 0x57, 0x60, byte(i)}
	if i%3 == 0 {
		data = data[:7]
	}
	sig := byte(i % 256)
	return source.Frame{Data: data, Time: at, Timestamp: uint64(i) * 12_000_123, Signal: sig, RSSI: source.SignalRSSI(sig)}
}

func newRecorder(t *testing.T, opt Options) *Recorder {
	t.Helper()
	if opt.Dir == "" {
		opt.Dir = filepath.Join(t.TempDir(), "tape")
	}
	r, err := New(opt)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// readAll reads every finished segment in dir.
func readAll(t *testing.T, dir string) []source.Frame {
	t.Helper()
	var out []source.Frame
	for _, p := range listSegments(dir) {
		sr, err := openSegment(p)
		if err != nil {
			t.Fatal(err)
		}
		for {
			f, err := sr.next()
			if err != nil {
				break
			}
			out = append(out, f)
		}
		sr.Close()
	}
	return out
}

func sameFrame(a, b source.Frame) bool {
	return bytes.Equal(a.Data, b.Data) && a.Time.Equal(b.Time) && a.Timestamp == b.Timestamp && a.Signal == b.Signal &&
		(a.RSSI == nil) == (b.RSSI == nil) && (a.RSSI == nil || *a.RSSI == *b.RSSI)
}

func relSegments(dir string) []string {
	var out []string
	for _, p := range listSegments(dir) {
		rel, _ := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

func TestRecorder_RoundTripAndRotation(t *testing.T) {
	r := newRecorder(t, Options{})
	boundary := t0.Add(time.Hour)
	var in []source.Frame
	for i, at := range []time.Time{
		t0.Add(123456789), t0.Add(30 * time.Minute), boundary.Add(-1), // last nanosecond of the first segment
		boundary, boundary.Add(time.Nanosecond), t0.Add(14 * time.Hour).Add(7 * time.Second), // next day in UTC
	} {
		in = append(in, frame(at, i))
	}
	for _, f := range in {
		r.write(f)
	}
	r.closeSegment()

	if got, want := relSegments(r.opt.Dir), []string{
		"2026/11/20/20261120T100000Z.tape.gz", "2026/11/20/20261120T110000Z.tape.gz", "2026/11/21/20261121T000000Z.tape.gz",
	}; !slicesEqual(got, want) {
		t.Errorf("segments %v, want %v", got, want)
	}
	out := readAll(t, r.opt.Dir)
	if len(out) != len(in) {
		t.Fatalf("read %d frames, want %d", len(out), len(in))
	}
	for i := range in {
		if !sameFrame(in[i], out[i]) {
			t.Errorf("frame %d: got %+v, want %+v", i, out[i], in[i])
		}
	}
	if st := r.Stats(); st.FramesWritten != 6 || st.Bytes <= 0 || !st.Oldest.Equal(in[0].Time) || !st.Newest.Equal(in[5].Time) {
		t.Errorf("stats = %+v", st)
	}
}

func slicesEqual(a, b []string) bool { return strings.Join(a, "|") == strings.Join(b, "|") }

func TestRecorder_RestartWithinSegmentKeepsBoth(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tape")
	for i, at := range []time.Time{t0.Add(time.Minute), t0.Add(20*time.Minute + 500*time.Millisecond)} {
		r := newRecorder(t, Options{Dir: dir})
		r.write(frame(at, i))
		r.closeSegment()
	}
	if got := relSegments(dir); !slicesEqual(got, []string{"2026/11/20/20261120T100000Z.tape.gz", "2026/11/20/20261120T102000Z.tape.gz"}) {
		t.Errorf("segments %v", got)
	}
	if out := readAll(t, dir); len(out) != 2 || !out[1].Time.Equal(t0.Add(20*time.Minute+500*time.Millisecond)) {
		t.Errorf("frames %+v", out)
	}
}

func TestRecorder_ClockSteppingBackKeepsOrder(t *testing.T) {
	r := newRecorder(t, Options{})
	r.write(frame(t0.Add(10*time.Second), 1))
	r.write(frame(t0.Add(5*time.Second), 2))
	r.closeSegment()
	out := readAll(t, r.opt.Dir)
	if len(out) != 2 || !out[1].Time.Equal(t0.Add(10*time.Second)) {
		t.Errorf("frames %+v", out)
	}
}

func TestRecorder_PartFinalizedAndTruncatedTailReadable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tape")
	r := newRecorder(t, Options{Dir: dir})
	for i := range 100 {
		r.write(frame(t0.Add(time.Duration(i)*time.Second), i))
	}
	r.flush()
	for i := 100; i < 200; i++ {
		r.write(frame(t0.Add(time.Duration(i)*time.Second), i))
	}
	r.flush()
	part := r.cur.path + ".part"
	// crash: the segment is never closed, and the last write is cut short
	fi, err := os.Stat(part)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(part, fi.Size()-20); err != nil {
		t.Fatal(err)
	}

	r2 := newRecorder(t, Options{Dir: dir})
	r2.finalizeParts()
	r2.scan()
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Error(".part still there")
	}
	out := readAll(t, dir)
	if len(out) < 100 || len(out) >= 200 {
		t.Fatalf("read %d frames from the truncated segment, want the first flush (100) at least", len(out))
	}
	for i, f := range out {
		if !sameFrame(f, frame(t0.Add(time.Duration(i)*time.Second), i)) {
			t.Fatalf("frame %d differs", i)
		}
	}
	if st := r2.Stats(); !st.Newest.Equal(out[len(out)-1].Time) || !st.Oldest.Equal(t0) {
		t.Errorf("stats after recovery = %+v", st)
	}
}

func TestRecorder_Retention(t *testing.T) {
	now := t0.Add(15 * 24 * time.Hour)
	r := newRecorder(t, Options{Retention: 14 * 24 * time.Hour, Now: func() time.Time { return now }})
	for i, at := range []time.Time{t0, t0.Add(23 * time.Hour), now.Add(-14*24*time.Hour - time.Hour), now.Add(-14*24*time.Hour + time.Minute), now.Add(-time.Hour)} {
		r.write(frame(at, i))
		r.closeSegment()
	}
	r.prune()
	// the two 09:00 segments (the second named after its first free second) ended at the cutoff
	if got := relSegments(r.opt.Dir); !slicesEqual(got, []string{"2026/11/21/20261121T100000Z.tape.gz", "2026/12/05/20261205T090000Z.tape.gz"}) {
		t.Errorf("segments after retention %v", got)
	}
	if _, err := os.Stat(filepath.Join(r.opt.Dir, "2026/11/20")); !os.IsNotExist(err) {
		t.Error("empty day directory kept")
	}
	if st := r.Stats(); !st.Oldest.Equal(now.Add(-14*24*time.Hour + time.Minute)) {
		t.Errorf("oldest = %v", st.Oldest)
	}
}

func dirSize(dir string) uint64 {
	var n uint64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += uint64(fi.Size())
			}
		}
		return nil
	})
	return n
}

func TestRecorder_DiskGuard(t *testing.T) {
	var buf bytes.Buffer
	dir := filepath.Join(t.TempDir(), "tape")
	var capacity uint64 = 1 << 30
	r := newRecorder(t, Options{
		Dir: dir, MinFreeBytes: 1000, Logger: slog.New(slog.NewTextHandler(&buf, nil)),
		FreeSpace: func(string) (uint64, error) { return capacity - min(capacity, dirSize(dir)), nil },
	})
	for h := range 4 {
		r.write(frame(t0.Add(time.Duration(h)*time.Hour), h))
	}
	r.closeSegment()
	size := dirSize(dir)
	perSegment := size / 4

	// room for two segments above the minimum: the two oldest go
	capacity = 1000 + 2*perSegment + perSegment/2
	r.guard()
	if got := len(listSegments(dir)); got != 2 || r.Stats().Paused {
		t.Fatalf("%d segments left, paused %v", got, r.Stats().Paused)
	}

	// even an empty tape leaves too little: everything goes, then recording pauses
	capacity = 500
	r.guard()
	r.running.Store(true)
	if st := r.Stats(); len(listSegments(dir)) != 0 || !st.Paused || st.Recording || st.GuardDeleted != 4 {
		t.Fatalf("stats = %+v", st)
	}
	r.write(frame(t0.Add(5*time.Hour), 5))
	if r.Stats().Dropped != 1 || r.cur != nil {
		t.Error("a paused recorder must drop frames")
	}
	if !strings.Contains(buf.String(), "tape recording paused") {
		t.Errorf("log: %s", &buf)
	}

	capacity = 1 << 30
	r.guard()
	r.write(frame(t0.Add(6*time.Hour), 6))
	if st := r.Stats(); st.Paused || !st.Recording || st.FramesWritten != 5 || !strings.Contains(buf.String(), "tape recording resumed") {
		t.Errorf("after resume: %+v; log %s", st, &buf)
	}
}

func TestRecorder_FullQueueDropsWithoutBlocking(t *testing.T) {
	r := newRecorder(t, Options{QueueSize: 2})
	done := make(chan struct{})
	go func() {
		for i := range 5 {
			r.Record(frame(t0, i))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Record blocked on a full queue")
	}
	if d := r.Stats().Dropped; d != 3 {
		t.Errorf("dropped %d, want 3", d)
	}
}

func TestRecorder_RunFlushesAndClosesOnCancel(t *testing.T) {
	r := newRecorder(t, Options{FlushInterval: 10 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { r.Run(ctx) })
	for i := range 50 {
		r.Record(frame(t0.Add(time.Duration(i)*time.Millisecond), i))
	}
	deadline := time.Now().Add(2 * time.Second)
	for r.Stats().LastFlush.IsZero() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if st := r.Stats(); !st.Recording || st.LastFlush.IsZero() {
		t.Errorf("while running: %+v", st)
	}
	cancel()
	wg.Wait()
	if st := r.Stats(); st.Recording || st.FramesWritten != 50 {
		t.Errorf("after stop: %+v", st)
	}
	if n := len(readAll(t, r.opt.Dir)); n != 50 {
		t.Errorf("read %d frames, want 50", n)
	}
}

func TestRecorder_WriteFailuresLoggedOncePerStreak(t *testing.T) {
	var buf bytes.Buffer
	r := newRecorder(t, Options{Logger: slog.New(slog.NewTextHandler(&buf, nil))})
	blocker := filepath.Join(r.opt.Dir, "2026")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil { // a file where the year directory must go
		t.Fatal(err)
	}
	for i := range 3 {
		r.write(frame(t0.Add(time.Duration(i)*time.Second), i))
	}
	if n := strings.Count(buf.String(), "tape write failed"); n != 1 || r.Stats().WriteErrors != 3 {
		t.Fatalf("%d warnings, %d errors; log %s", n, r.Stats().WriteErrors, &buf)
	}
	os.Remove(blocker)
	r.write(frame(t0.Add(4*time.Second), 4))
	r.flush()
	if !strings.Contains(buf.String(), "tape writes work again") {
		t.Errorf("no recovery message; log %s", &buf)
	}
}

func TestRecorder_FlushWhileIdle(t *testing.T) {
	now := t0
	r := newRecorder(t, Options{Now: func() time.Time { return now }})
	r.flush() // nothing received yet: the tape is still up to date
	if st := r.Stats(); !st.LastFlush.Equal(t0) {
		t.Errorf("idle flush: %v", st.LastFlush)
	}
	r.write(frame(t0, 1))
	now = t0.Add(5 * time.Second)
	r.flush()
	now = t0.Add(10 * time.Second)
	r.flush() // a quiet sky
	if st := r.Stats(); !st.LastFlush.Equal(t0.Add(10 * time.Second)) {
		t.Errorf("quiet flush: %v", st.LastFlush)
	}
	r.failing = true // writes fail: the last good flush stays
	now = t0.Add(15 * time.Second)
	r.flush()
	if st := r.Stats(); !st.LastFlush.Equal(t0.Add(10 * time.Second)) {
		t.Errorf("failing flush moved: %v", st.LastFlush)
	}
	r.closeSegment()
}
