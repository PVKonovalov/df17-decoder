package source

import (
	"bufio"
	"context"
	"errors"
	"io"
	"math"
	"time"
)

const (
	beastEsc       = 0x1A
	beastHeaderLen = 6 + 1 // 12 MHz timestamp + signal level
)

// beastPayloadLen returns the frame length for a Beast type byte, 0 for types that carry no Mode S frame.
func beastPayloadLen(typ byte) int {
	switch typ {
	case '1':
		return 2 // Mode A/C
	case '2':
		return 7
	case '3':
		return 14
	}
	return 0
}

// SignalRSSI converts a Beast signal byte to dBFS; 0 gives nil.
func SignalRSSI(sig byte) *float64 {
	if sig == 0 {
		return nil
	}
	return new(20 * math.Log10(float64(sig)/255))
}

// readBeast reads Beast binary frames from r and sends Mode S frames to out until r ends or ctx is cancelled.
// Mode A/C and status frames are skipped. Bytes outside a frame, unknown types and frames cut short by a new
// frame or EOF count as malformed; the reader resyncs on the next 0x1A. clock (nil for live input) gives each
// frame its time from the 12 MHz timestamp and paces file replay, as in readAVR.
func readBeast(ctx context.Context, r io.Reader, out chan<- Frame, stats *Stats, clock func(ts uint64) (time.Time, error)) error {
	br := bufio.NewReader(r)
	end := func(err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}

	buf := make([]byte, 0, beastHeaderLen+14)
	var typ byte
	started := false // typ holds the type byte of a frame whose 0x1A was consumed mid-frame
	junk, quiet := false, false
	for {
		if !started {
			b, err := br.ReadByte()
			if err != nil {
				return end(err)
			}
			if b != beastEsc {
				junk = junk || !quiet
				continue
			}
			if typ, err = br.ReadByte(); err != nil {
				return end(err)
			}
			if typ == beastEsc { // escaped data byte, we are inside a frame we did not see start
				junk = junk || !quiet
				continue
			}
		}
		started, quiet = false, false
		if junk {
			stats.malformed.Add(1)
			junk = false
		}

		n := beastPayloadLen(typ)
		if n == 0 {
			if typ == '4' { // receiver status frame
				quiet = true
			} else {
				junk = true
			}
			continue
		}

		buf = buf[:0]
		for len(buf) < beastHeaderLen+n {
			c, err := br.ReadByte()
			if err != nil {
				if errors.Is(err, io.EOF) {
					stats.malformed.Add(1)
				}
				return end(err)
			}
			if c == beastEsc {
				next, err := br.ReadByte()
				if err != nil {
					if errors.Is(err, io.EOF) {
						stats.malformed.Add(1)
					}
					return end(err)
				}
				if next != beastEsc {
					stats.malformed.Add(1)
					typ, started = next, true
					break
				}
			}
			buf = append(buf, c)
		}
		if started || typ == '1' {
			continue
		}

		var ts uint64
		for _, b := range buf[:6] {
			ts = ts<<8 | uint64(b)
		}
		frameTime := time.Now()
		if clock != nil {
			var err error
			if frameTime, err = clock(ts); err != nil {
				return err
			}
		}
		f := Frame{
			Data:      append([]byte(nil), buf[beastHeaderLen:]...),
			Time:      frameTime,
			Timestamp: ts,
			Signal:    buf[6],
			RSSI:      SignalRSSI(buf[6]),
		}
		select {
		case out <- f:
			stats.frames.Add(1)
			stats.lastFrame.Store(time.Now().UnixNano())
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
