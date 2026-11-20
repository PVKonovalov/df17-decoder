package tape

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/source"
)

// GapThreshold is the silence after which the reader reports a gap and the extent splits a span.
const GapThreshold = 60 * time.Second

// Tape reads the segments in a recording directory, including the active one up to its last flush.
type Tape struct {
	dir   string
	mu    sync.Mutex
	spans map[string]cachedSpans // finished segments, by path
}

type cachedSpans struct {
	size  int64
	mod   time.Time
	spans []Span
}

// Span is a stretch of recording without a gap longer than GapThreshold.
type Span struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Extent is what the tape holds: its ends and the recorded spans in time order, in UTC.
type Extent struct {
	Oldest *time.Time `json:"oldest,omitempty"`
	Newest *time.Time `json:"newest,omitempty"`
	Spans  []Span     `json:"spans"`
}

func Open(dir string) *Tape { return &Tape{dir: dir, spans: map[string]cachedSpans{}} }

type segmentFile struct {
	path   string
	start  time.Time // from the name
	active bool      // still being written (.part)
}

// files lists finished and active segments in time order.
func (tp *Tape) files() []segmentFile {
	var out []segmentFile
	_ = filepath.WalkDir(tp.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		active := strings.HasSuffix(p, partExt)
		if !active && !strings.HasSuffix(p, segmentExt) {
			return nil
		}
		if start, ok := segmentStart(strings.TrimSuffix(p, ".part")); ok {
			out = append(out, segmentFile{path: p, start: start, active: active})
		}
		return nil
	})
	slices.SortFunc(out, func(a, b segmentFile) int { return a.start.Compare(b.start) })
	return out
}

// Reader returns the frames of a tape in time order from a seek point.
type Reader struct {
	files []segmentFile
	cur   *segmentReader
	from  time.Time
	last  time.Time
}

// Seek returns a reader starting at the first frame at or after t. Segments are decompressed from their start,
// so seeking inside one costs a scan of the frames before t.
func (tp *Tape) Seek(t time.Time) *Reader {
	files := tp.files()
	i := 0
	for j, f := range files {
		if !f.start.After(t) {
			i = j
		}
	}
	return &Reader{files: files[i:], from: t, last: t}
}

// Next returns the next frame and the gap before it: the time skipped since the previous frame (or the seek
// point) when longer than GapThreshold, else 0. It returns io.EOF at the end of the tape. Segments that are
// gone (retention) or unreadable are skipped.
func (r *Reader) Next() (source.Frame, time.Duration, error) {
	for {
		if r.cur == nil {
			if len(r.files) == 0 {
				return source.Frame{}, 0, io.EOF
			}
			sr, err := openSegment(r.files[0].path)
			r.files = r.files[1:]
			if err != nil {
				continue
			}
			r.cur = sr
		}
		f, err := r.cur.nextFrom(r.from.UnixNano())
		if err != nil {
			r.cur.Close()
			r.cur = nil
			continue
		}
		gap := f.Time.Sub(r.last)
		if gap <= GapThreshold {
			gap = 0
		}
		r.last = f.Time
		return f, gap, nil
	}
}

func (r *Reader) Close() {
	if r.cur != nil {
		r.cur.Close()
		r.cur = nil
	}
	r.files = nil
}

// Extent scans the tape for its recorded spans. Finished segments are scanned once and cached.
func (tp *Tape) Extent() Extent {
	files := tp.files()
	tp.mu.Lock()
	defer tp.mu.Unlock()
	seen := map[string]bool{}
	var spans []Span
	for _, f := range files {
		var ss []Span
		if f.active {
			ss = segmentSpans(f.path)
		} else {
			seen[f.path] = true
			fi, err := os.Stat(f.path)
			if err != nil {
				continue
			}
			c, ok := tp.spans[f.path]
			if !ok || c.size != fi.Size() || !c.mod.Equal(fi.ModTime()) {
				c = cachedSpans{size: fi.Size(), mod: fi.ModTime(), spans: segmentSpans(f.path)}
				tp.spans[f.path] = c
			}
			ss = c.spans
		}
		for _, s := range ss {
			s.From, s.To = s.From.UTC(), s.To.UTC()
			if n := len(spans); n > 0 && s.From.Sub(spans[n-1].To) <= GapThreshold {
				spans[n-1].To = s.To
			} else {
				spans = append(spans, s)
			}
		}
	}
	for p := range tp.spans {
		if !seen[p] {
			delete(tp.spans, p)
		}
	}
	e := Extent{Spans: spans}
	if n := len(spans); n > 0 {
		e.Oldest, e.Newest = &spans[0].From, &spans[n-1].To
	}
	if e.Spans == nil {
		e.Spans = []Span{}
	}
	return e
}

// segmentSpans returns one segment's recorded spans.
func segmentSpans(path string) []Span {
	sr, err := openSegment(path)
	if err != nil {
		return nil
	}
	defer sr.Close()
	var spans []Span
	for {
		f, err := sr.next()
		if err != nil {
			return spans
		}
		if n := len(spans); n > 0 && f.Time.Sub(spans[n-1].To) <= GapThreshold {
			spans[n-1].To = f.Time
		} else {
			spans = append(spans, Span{From: f.Time, To: f.Time})
		}
	}
}
