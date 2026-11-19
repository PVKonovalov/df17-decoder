package source

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"time"
)

var ErrMalformed = errors.New("malformed AVR line")

// ParseAVR parses one AVR line: "*<hex>;" or "@<12 hex timestamp><hex>;" with 14 or 28 hex frame digits.
// It returns the frame bytes and the 12 MHz timestamp (0 for the "*" form).
func ParseAVR(line []byte) (data []byte, timestamp uint64, err error) {
	line = bytes.TrimSpace(line)
	if len(line) < 2 || line[len(line)-1] != ';' {
		return nil, 0, ErrMalformed
	}
	body := line[1 : len(line)-1]

	switch line[0] {
	case '*':
	case '@':
		if len(body) < 12 {
			return nil, 0, ErrMalformed
		}
		if timestamp, err = strconv.ParseUint(string(body[:12]), 16, 64); err != nil {
			return nil, 0, ErrMalformed
		}
		body = body[12:]
	default:
		return nil, 0, ErrMalformed
	}

	if len(body) != 14 && len(body) != 28 {
		return nil, 0, ErrMalformed
	}
	data = make([]byte, len(body)/2)
	if _, err = hex.Decode(data, body); err != nil {
		return nil, 0, ErrMalformed
	}
	return data, timestamp, nil
}

// readAVR reads AVR lines from r and sends valid frames to out until r ends or ctx is cancelled.
// Empty lines are skipped silently; malformed lines are counted. clock, if non-nil, gives the frame time
// and may block to pace a replay; nil means the wall clock.
func readAVR(ctx context.Context, r io.Reader, out chan<- Frame, stats *Stats, clock func(ts uint64) (time.Time, error)) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		data, ts, err := ParseAVR(line)
		if err != nil {
			stats.malformed.Add(1)
			continue
		}
		frameTime := time.Now()
		if clock != nil {
			if frameTime, err = clock(ts); err != nil {
				return err
			}
		}
		select {
		case out <- Frame{Data: data, Time: frameTime, Timestamp: ts}:
			stats.frames.Add(1)
			stats.lastFrame.Store(time.Now().UnixNano())
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return sc.Err()
}
