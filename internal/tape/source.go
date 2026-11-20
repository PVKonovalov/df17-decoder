package tape

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/source"
)

// Source replays a tape as the service input (source.url tape://<dir>?from=<RFC3339>&speed=<x>). Frames keep
// their recorded receive times; speed paces them (0: as fast as possible) and a gap is skipped at once. Run returns
// nil at the end of the tape.
type Source struct {
	Tape  *Tape
	From  time.Time // zero: the start of the tape
	Speed float64
	stats source.Stats
}

// ParseURL splits a tape:// URL into the directory, the start time and the speed (default 1).
func ParseURL(raw string) (dir string, from time.Time, speed float64, err error) {
	rest, ok := strings.CutPrefix(raw, "tape://")
	if !ok {
		return "", from, 0, fmt.Errorf("tape: %q is not a tape:// URL", raw)
	}
	dir, query, _ := strings.Cut(rest, "?")
	if dir == "" {
		return "", from, 0, fmt.Errorf("tape: %q: missing directory", raw)
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		return "", from, 0, fmt.Errorf("tape: %q: %w", raw, err)
	}
	speed = 1
	var errs []error
	if v := q.Get("from"); v != "" {
		if from, err = time.Parse(time.RFC3339Nano, v); err != nil {
			errs = append(errs, fmt.Errorf("from must be RFC 3339, got %q", v))
		}
	}
	if v := q.Get("speed"); v != "" {
		if speed, err = strconv.ParseFloat(v, 64); err != nil || speed < 0 {
			errs = append(errs, fmt.Errorf("speed must be a number >= 0, got %q", v))
		}
	}
	if len(errs) > 0 {
		return "", from, 0, fmt.Errorf("tape: %q: %w", raw, errors.Join(errs...))
	}
	return dir, from, speed, nil
}

func (s *Source) Stats() *source.Stats { return &s.stats }

func (s *Source) Run(ctx context.Context, out chan<- source.Frame) error {
	s.stats.SetConnected(true)
	defer s.stats.SetConnected(false)
	r := s.Tape.Seek(s.From)
	defer r.Close()

	var first time.Time // recorded time paced from
	var start time.Time // wall time of first
	for {
		f, gap, err := r.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if s.Speed > 0 {
			if first.IsZero() || gap > 0 {
				first, start = f.Time, time.Now()
			}
			if wait := time.Until(start.Add(time.Duration(float64(f.Time.Sub(first)) / s.Speed))); wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				}
			}
		}
		select {
		case out <- f:
			s.stats.AddFrame()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
