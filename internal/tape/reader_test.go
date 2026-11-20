package tape

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/source"
)

// recordAt writes one frame per entry of times (seconds after t0) and closes the segment, except when keepOpen.
func recordAt(t *testing.T, dir string, keepOpen bool, secs ...float64) *Recorder {
	t.Helper()
	r := newRecorder(t, Options{Dir: dir})
	for i, s := range secs {
		r.write(frame(t0.Add(time.Duration(s*float64(time.Second))), i))
	}
	if keepOpen {
		r.flush()
	} else {
		r.closeSegment()
	}
	return r
}

type read struct {
	sec float64
	gap time.Duration
}

func readFrom(t *testing.T, tp *Tape, sec float64) []read {
	t.Helper()
	r := tp.Seek(t0.Add(time.Duration(sec * float64(time.Second))))
	defer r.Close()
	var out []read
	for {
		f, gap, err := r.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, read{f.Time.Sub(t0).Seconds(), gap})
	}
}

func secsOf(rs []read) []float64 {
	out := make([]float64, len(rs))
	for i, r := range rs {
		out[i] = r.sec
	}
	return out
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestReader_Seek(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tape")
	// two segments (10:00, 11:00) with a 30 min hole between 10:20 and 10:50, then the next hour
	recordAt(t, dir, false, 0, 1, 2, 1200, 1201, 3000, 3599.5, 3600, 3601, 7199)
	tp := Open(dir)
	for _, c := range []struct {
		name string
		from float64
		want []float64
	}{
		{"before the tape", -100, []float64{0, 1, 2, 1200, 1201, 3000, 3599.5, 3600, 3601, 7199}},
		{"start of a segment", 0, []float64{0, 1, 2, 1200, 1201, 3000, 3599.5, 3600, 3601, 7199}},
		{"middle, between frames", 1.5, []float64{2, 1200, 1201, 3000, 3599.5, 3600, 3601, 7199}},
		{"end of a segment", 3599.5, []float64{3599.5, 3600, 3601, 7199}},
		{"exactly at the boundary", 3600, []float64{3600, 3601, 7199}},
		{"inside a gap", 2000, []float64{3000, 3599.5, 3600, 3601, 7199}},
		{"after the tape", 8000, nil},
	} {
		if got := secsOf(readFrom(t, tp, c.from)); !equalFloats(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReader_ReportsGaps(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tape")
	recordAt(t, dir, false, 0, 30, 89, 200, 3600, 3700)
	got := readFrom(t, Open(dir), -120)
	want := []read{{0, 120 * time.Second}, {30, 0}, {89, 0}, {200, 111 * time.Second}, {3600, 3400 * time.Second}, {3700, 100 * time.Second}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("frame %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestReader_ReadsActiveSegment(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tape")
	recordAt(t, dir, false, 0, 1)
	r := newRecorder(t, Options{Dir: dir})
	r.write(frame(t0.Add(3600*time.Second), 0))
	r.flush()
	r.write(frame(t0.Add(3601*time.Second), 1)) // not flushed yet: not visible
	if got := secsOf(readFrom(t, Open(dir), 0)); !equalFloats(got, []float64{0, 1, 3600}) {
		t.Errorf("got %v", got)
	}
	r.closeSegment()
}

func TestExtent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tape")
	if e := Open(dir).Extent(); e.Oldest != nil || len(e.Spans) != 0 {
		t.Errorf("empty tape: %+v", e)
	}
	// a 30 s silence is merged, a 3 min one splits; the hour boundary at 3600 doesn't
	recordAt(t, dir, false, 0, 30, 60, 240, 300, 3590, 3600, 3610)
	r := newRecorder(t, Options{Dir: dir})
	r.write(frame(t0.Add(3640*time.Second), 0))
	r.flush()
	tp := Open(dir)
	e := tp.Extent()
	sec := func(x time.Time) float64 { return x.Sub(t0).Seconds() }
	var got [][2]float64
	for _, s := range e.Spans {
		got = append(got, [2]float64{sec(s.From), sec(s.To)})
	}
	want := [][2]float64{{0, 60}, {240, 300}, {3590, 3640}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("spans %v, want %v", got, want)
	}
	if sec(*e.Oldest) != 0 || sec(*e.Newest) != 3640 {
		t.Errorf("ends %v %v", e.Oldest, e.Newest)
	}

	r.write(frame(t0.Add(3650*time.Second), 1)) // the active segment is rescanned
	r.flush()
	if e := tp.Extent(); sec(*e.Newest) != 3650 {
		t.Errorf("newest after more recording: %v", e.Newest)
	}
	r.closeSegment()
}

func TestParseURL(t *testing.T) {
	dir, from, speed, err := ParseURL("tape:///var/lib/df17-decoder/tape?from=2026-11-20T10:00:00Z&speed=8")
	if err != nil || dir != "/var/lib/df17-decoder/tape" || !from.Equal(t0) || speed != 8 {
		t.Errorf("%q %v %v %v", dir, from, speed, err)
	}
	if dir, from, speed, err = ParseURL("tape://tape"); err != nil || dir != "tape" || !from.IsZero() || speed != 1 {
		t.Errorf("defaults: %q %v %v %v", dir, from, speed, err)
	}
	for _, bad := range []string{"file://x", "tape://", "tape://x?from=yesterday", "tape://x?speed=-1", "tape://x?speed=fast"} {
		if _, _, _, err := ParseURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSource_ReplaysFromAndPaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tape")
	recordAt(t, dir, false, 0, 1, 2, 2.2, 2.4, 2.6, 900) // a gap before 900 must not be slept through
	src := &Source{Tape: Open(dir), From: t0.Add(2 * time.Second), Speed: 4}
	out := make(chan source.Frame, 10)
	start := time.Now()
	if err := src.Run(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	close(out)
	elapsed := time.Since(start)
	var got []float64
	for f := range out {
		got = append(got, f.Time.Sub(t0).Seconds())
	}
	if !equalFloats(got, []float64{2, 2.2, 2.4, 2.6, 900}) {
		t.Errorf("frames %v", got)
	}
	// 0.6 s of tape at 4× is 150 ms; the 15 min gap is skipped
	if elapsed < 140*time.Millisecond || elapsed > 2*time.Second {
		t.Errorf("replay took %v", elapsed)
	}
	if st := src.Stats().Snapshot(); st.Frames != 5 || st.Connected {
		t.Errorf("stats %+v", st)
	}
}

// BenchmarkSeekEndOfHour seeks to the last second of a full 1-hour segment at about 55 frames/s.
func BenchmarkSeekEndOfHour(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "tape")
	r, err := New(Options{Dir: dir})
	if err != nil {
		b.Fatal(err)
	}
	const n = 55 * 3600
	for i := range n {
		r.write(frame(t0.Add(time.Duration(i)*time.Hour/n), i))
	}
	r.closeSegment()
	tp := Open(dir)
	target := t0.Add(time.Hour - time.Second)
	for b.Loop() {
		rd := tp.Seek(target)
		if _, _, err := rd.Next(); err != nil {
			b.Fatal(err)
		}
		rd.Close()
	}
}
