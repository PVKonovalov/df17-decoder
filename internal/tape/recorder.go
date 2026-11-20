package tape

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/source"
)

const (
	defaultQueueSize     = 1 << 14
	defaultSegment       = time.Hour
	defaultFlushInterval = 5 * time.Second
	retentionInterval    = time.Hour

	segmentExt = ".tape.gz"
	partExt    = segmentExt + ".part"
	nameLayout = "20060102T150405Z"
	dayLayout  = "2006/01/02"
)

type Options struct {
	Dir           string
	Segment       time.Duration // segment length, dividing a day; 1h when zero
	FlushInterval time.Duration // gzip flush interval, the most a crash loses; 5s when zero
	Retention     time.Duration // segments older than this are deleted hourly; 0 keeps everything
	MinFreeBytes  uint64        // disk guard: oldest segments go, then recording pauses, below this; 0 disables
	QueueSize     int           // frames buffered for the writer before new ones are dropped; 16384 when zero
	Logger        *slog.Logger
	Now           func() time.Time                 // wall clock for retention; time.Now when nil
	FreeSpace     func(dir string) (uint64, error) // free bytes for unprivileged users; statfs when nil
}

// Stats are the recorder's counters. Oldest and Newest are receive times of the tape's ends.
type Stats struct {
	Recording     bool // running and not paused by the disk guard
	Paused        bool // paused because free space is below MinFreeBytes
	Oldest        time.Time
	Newest        time.Time
	Bytes         int64 // on disk, including the active segment up to its last flush
	FramesWritten uint64
	Dropped       uint64 // full queue or paused
	WriteErrors   uint64
	LastFlush     time.Time
}

type segment struct {
	path  string // final name; the file is path + ".part" until closed
	file  *os.File
	cw    *countingWriter
	gz    *gzip.Writer
	end   int64 // unix ns of the next segment boundary
	last  int64 // unix ns of the last record (the boundary before the first)
	dirty bool  // records written since the last flush
}

type countingWriter struct {
	w io.Writer
	n atomic.Int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

// Recorder writes frames to the tape. Record never blocks; Run owns all file access.
type Recorder struct {
	opt   Options
	queue chan source.Frame

	cur     *segment // Run only
	buf     []byte   // Run only
	paused  bool     // Run only
	failing bool     // Run only

	running     atomic.Bool
	pausedFlag  atomic.Bool
	written     atomic.Uint64
	dropped     atomic.Uint64
	writeErrors atomic.Uint64
	lastFlush   atomic.Int64 // unix ns
	oldest      atomic.Int64
	newest      atomic.Int64
	closedBytes atomic.Int64
	cw          atomic.Pointer[countingWriter]
}

// New creates the tape directory and returns a recorder; Run starts it.
func New(opt Options) (*Recorder, error) {
	if opt.Segment <= 0 {
		opt.Segment = defaultSegment
	}
	if opt.FlushInterval <= 0 {
		opt.FlushInterval = defaultFlushInterval
	}
	if opt.QueueSize <= 0 {
		opt.QueueSize = defaultQueueSize
	}
	if opt.Logger == nil {
		opt.Logger = slog.New(slog.DiscardHandler)
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.FreeSpace == nil {
		opt.FreeSpace = freeBytes
	}
	if err := os.MkdirAll(opt.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("tape: %w", err)
	}
	return &Recorder{opt: opt, queue: make(chan source.Frame, opt.QueueSize)}, nil
}

// Record queues f for writing, or drops and counts it when the queue is full.
func (r *Recorder) Record(f source.Frame) {
	select {
	case r.queue <- f:
	default:
		r.dropped.Add(1)
	}
}

func (r *Recorder) Stats() Stats {
	st := Stats{
		Recording:     r.running.Load() && !r.pausedFlag.Load(),
		Paused:        r.pausedFlag.Load(),
		Bytes:         r.closedBytes.Load(),
		FramesWritten: r.written.Load(),
		Dropped:       r.dropped.Load(),
		WriteErrors:   r.writeErrors.Load(),
	}
	if cw := r.cw.Load(); cw != nil {
		st.Bytes += cw.n.Load()
	}
	for _, v := range []struct {
		src *atomic.Int64
		dst *time.Time
	}{{&r.oldest, &st.Oldest}, {&r.newest, &st.Newest}, {&r.lastFlush, &st.LastFlush}} {
		if ns := v.src.Load(); ns != 0 {
			*v.dst = time.Unix(0, ns)
		}
	}
	return st
}

// Run finalizes segments left by a crash, then records queued frames until ctx is cancelled, flushing every
// FlushInterval and applying retention hourly; on cancel it writes what is queued and closes the segment.
func (r *Recorder) Run(ctx context.Context) {
	r.finalizeParts()
	r.scan()
	r.prune()
	r.running.Store(true)
	defer r.running.Store(false)
	r.guard()

	flush := time.NewTicker(r.opt.FlushInterval)
	defer flush.Stop()
	prune := time.NewTicker(retentionInterval)
	defer prune.Stop()
	for {
		select {
		case f := <-r.queue:
			r.write(f)
		case <-flush.C:
			r.flush()
		case <-prune.C:
			r.prune()
		case <-ctx.Done():
			for {
				select {
				case f := <-r.queue:
					r.write(f)
				default:
					r.closeSegment()
					return
				}
			}
		}
	}
}

func (r *Recorder) write(f source.Frame) {
	if r.paused {
		r.dropped.Add(1)
		return
	}
	t := f.Time.UnixNano()
	if r.cur != nil && t >= r.cur.end {
		r.closeSegment()
	}
	if r.cur == nil {
		if err := r.openSegment(f.Time); err != nil {
			r.dropped.Add(1)
			r.fail(err)
			return
		}
	}
	s := r.cur
	t = max(t, s.last) // a wall clock stepping back must not break the record order
	r.buf = appendRecord(r.buf[:0], uint64(t-s.last), f)
	if _, err := s.gz.Write(r.buf); err != nil {
		r.fail(err)
		r.closeSegment()
		return
	}
	s.last, s.dirty = t, true
	r.written.Add(1)
	r.newest.Store(t)
	if r.oldest.Load() == 0 {
		r.oldest.Store(t)
	}
}

// openSegment starts the segment holding receive time at. Its header carries the UTC boundary; the name is the
// boundary too, unless a file of that name exists (a restart within the segment), then the first free second.
func (r *Recorder) openSegment(at time.Time) error {
	start := at.UTC().Truncate(r.opt.Segment)
	dir := filepath.Join(r.opt.Dir, start.Format(dayLayout))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	name := start
	var path string
	for {
		path = filepath.Join(dir, name.Format(nameLayout)+segmentExt)
		if !exists(path) && !exists(path+".part") {
			break
		}
		name = name.Add(time.Second)
		if sec := at.UTC().Truncate(time.Second); sec.After(name) {
			name = sec
		}
	}
	file, err := os.OpenFile(path+".part", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	cw := &countingWriter{w: file}
	gz := gzip.NewWriter(cw)
	if err := writeHeader(gz, start); err != nil {
		file.Close()
		return err
	}
	r.cur = &segment{path: path, file: file, cw: cw, gz: gz, end: start.Add(r.opt.Segment).UnixNano(), last: start.UnixNano()}
	r.cw.Store(cw)
	return nil
}

// closeSegment finishes the active segment and gives it its final name.
func (r *Recorder) closeSegment() {
	s := r.cur
	if s == nil {
		return
	}
	r.cur = nil
	err := errors.Join(s.gz.Close(), s.file.Sync(), s.file.Close())
	if renameErr := os.Rename(s.path+".part", s.path); renameErr != nil {
		err = errors.Join(err, renameErr)
	}
	r.closedBytes.Add(s.cw.n.Load())
	r.cw.Store(nil)
	if err != nil {
		r.fail(err)
	}
}

// flush pushes the active segment to the file, applies the disk guard and logs recovery from a failure streak.
func (r *Recorder) flush() {
	if s := r.cur; s != nil && s.dirty {
		if err := s.gz.Flush(); err != nil {
			r.fail(err)
			r.closeSegment()
		} else {
			s.dirty = false
			r.lastFlush.Store(r.opt.Now().UnixNano())
			if r.failing {
				r.opt.Logger.Info("tape writes work again", "dir", r.opt.Dir)
				r.failing = false
			}
		}
	}
	r.guard()
}

func (r *Recorder) fail(err error) {
	r.writeErrors.Add(1)
	if !r.failing {
		r.opt.Logger.Warn("tape write failed; further failures are not logged until it works again", "dir", r.opt.Dir, "err", err)
	}
	r.failing = true
}

// guard deletes the oldest segments while free space is below MinFreeBytes, then pauses recording if that was
// not enough, and resumes once there is room again.
func (r *Recorder) guard() {
	if r.opt.MinFreeBytes == 0 {
		return
	}
	free, err := r.opt.FreeSpace(r.opt.Dir)
	if err != nil {
		return
	}
	deleted := false
	for free < r.opt.MinFreeBytes {
		segs := listSegments(r.opt.Dir)
		if len(segs) == 0 {
			break
		}
		r.remove(segs[0])
		deleted = true
		r.opt.Logger.Warn("tape: low disk space, deleted the oldest segment", "file", segs[0], "freeMB", free>>20)
		if free, err = r.opt.FreeSpace(r.opt.Dir); err != nil {
			return
		}
	}
	if deleted {
		r.removeEmptyDirs()
		r.updateOldest()
	}
	switch {
	case free < r.opt.MinFreeBytes && !r.paused:
		r.paused = true
		r.closeSegment()
		r.opt.Logger.Warn("tape recording paused: low disk space", "dir", r.opt.Dir, "freeMB", free>>20, "minFreeMB", r.opt.MinFreeBytes>>20)
	case free >= r.opt.MinFreeBytes && r.paused:
		r.paused = false
		r.opt.Logger.Info("tape recording resumed", "dir", r.opt.Dir, "freeMB", free>>20)
	}
	r.pausedFlag.Store(r.paused)
}

// prune deletes closed segments that ended before now - Retention, and empty day directories.
func (r *Recorder) prune() {
	if r.opt.Retention > 0 {
		cutoff := r.opt.Now().Add(-r.opt.Retention)
		n := 0
		for _, p := range listSegments(r.opt.Dir) {
			if start, ok := segmentStart(p); ok && !start.Truncate(r.opt.Segment).Add(r.opt.Segment).After(cutoff) {
				r.remove(p)
				n++
			}
		}
		if n > 0 {
			r.opt.Logger.Info("tape retention", "deletedSegments", n, "before", cutoff.UTC())
		}
	}
	r.removeEmptyDirs()
	r.updateOldest()
}

func (r *Recorder) remove(path string) {
	if fi, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			r.fail(err)
			return
		}
		r.closedBytes.Add(-fi.Size())
	}
}

// finalizeParts renames segments left as .part by a crash; they are readable up to their last flush.
func (r *Recorder) finalizeParts() {
	_ = filepath.WalkDir(r.opt.Dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, partExt) {
			final := strings.TrimSuffix(p, ".part")
			if exists(final) {
				r.opt.Logger.Warn("tape: leftover segment conflicts with a finished one, keeping both", "file", p)
				return nil
			}
			if err := os.Rename(p, final); err != nil {
				r.fail(err)
			} else {
				r.opt.Logger.Info("tape: finalized a segment left open by an unclean shutdown", "file", final)
			}
		}
		return nil
	})
}

// scan sets the size and the ends of the tape from the files on disk.
func (r *Recorder) scan() {
	segs := listSegments(r.opt.Dir)
	var total int64
	for _, p := range segs {
		if fi, err := os.Stat(p); err == nil {
			total += fi.Size()
		}
	}
	r.closedBytes.Store(total)
	r.updateOldest()
	if len(segs) > 0 {
		if t, ok := lastRecordTime(segs[len(segs)-1]); ok {
			r.newest.Store(t.UnixNano())
		}
	}
}

func (r *Recorder) updateOldest() {
	segs := listSegments(r.opt.Dir)
	switch {
	case len(segs) > 0:
		if t, ok := firstRecordTime(segs[0]); ok {
			r.oldest.Store(t.UnixNano())
		}
	case r.cur == nil:
		r.oldest.Store(0)
		r.newest.Store(0)
	}
}

// removeEmptyDirs removes empty directories below Dir, deepest first.
func (r *Recorder) removeEmptyDirs() {
	var dirs []string
	_ = filepath.WalkDir(r.opt.Dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != r.opt.Dir {
			dirs = append(dirs, p)
		}
		return nil
	})
	slices.SortFunc(dirs, func(a, b string) int { return len(b) - len(a) })
	for _, d := range dirs {
		_ = os.Remove(d) // fails while not empty
	}
}

// listSegments returns the finished segments below dir in time order.
func listSegments(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, segmentExt) {
			out = append(out, p)
		}
		return nil
	})
	slices.Sort(out)
	return out
}

// segmentStart parses the time in a segment's file name.
func segmentStart(path string) (time.Time, bool) {
	t, err := time.Parse(nameLayout, strings.TrimSuffix(filepath.Base(path), segmentExt))
	return t, err == nil
}

func firstRecordTime(path string) (time.Time, bool) {
	sr, err := openSegment(path)
	if err != nil {
		return time.Time{}, false
	}
	defer sr.Close()
	f, err := sr.next()
	return f.Time, err == nil
}

func lastRecordTime(path string) (time.Time, bool) {
	sr, err := openSegment(path)
	if err != nil {
		return time.Time{}, false
	}
	defer sr.Close()
	var last time.Time
	for {
		f, err := sr.next()
		if err != nil {
			return last, !last.IsZero()
		}
		last = f.Time
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
