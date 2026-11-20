// Package tape records every received frame to rotating gzip segments and reads them back for playback.
package tape

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/source"
)

// Segment file layout (inside gzip): header = magic, version, segment start (unix ns, int64 big endian);
// then per frame: uvarint time delta (ns) since the previous record (the first since the segment start),
// 6-byte 12 MHz timestamp, signal byte, length byte, frame bytes.
const (
	magic         = "DF17TAPE"
	formatVersion = 1
	headerLen     = len(magic) + 1 + 8
	timestampLen  = 6
	timestampMask = 1<<(8*timestampLen) - 1
)

var errBadHeader = errors.New("tape: not a tape segment")

func writeHeader(w io.Writer, start time.Time) error {
	var b [headerLen]byte
	copy(b[:], magic)
	b[len(magic)] = formatVersion
	binary.BigEndian.PutUint64(b[len(magic)+1:], uint64(start.UnixNano()))
	_, err := w.Write(b[:])
	return err
}

// appendRecord appends the record of f, delta ns after the previous one, to buf.
func appendRecord(buf []byte, delta uint64, f source.Frame) []byte {
	buf = binary.AppendUvarint(buf, delta)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], f.Timestamp&timestampMask)
	buf = append(buf, ts[8-timestampLen:]...)
	buf = append(buf, f.Signal, byte(len(f.Data)))
	return append(buf, f.Data...)
}

// segmentReader reads one segment's frames in order. A truncated gzip tail (a crash after the last flush) ends
// the segment quietly after its last complete record.
type segmentReader struct {
	file  *os.File
	gz    *gzip.Reader
	br    *bufio.Reader
	start time.Time
	t     int64 // unix ns of the last record
}

func openSegment(path string) (*segmentReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("tape: %s: %w", path, err)
	}
	r := &segmentReader{file: f, gz: gz, br: bufio.NewReaderSize(gz, 64<<10)}
	var h [headerLen]byte
	if _, err := io.ReadFull(r.br, h[:]); err != nil || string(h[:len(magic)]) != magic {
		r.Close()
		return nil, fmt.Errorf("%w: %s", errBadHeader, path)
	}
	if v := h[len(magic)]; v != formatVersion {
		r.Close()
		return nil, fmt.Errorf("tape: %s: unsupported format version %d", path, v)
	}
	r.t = int64(binary.BigEndian.Uint64(h[len(magic)+1:]))
	r.start = time.Unix(0, r.t)
	return r, nil
}

// next returns the next frame, or io.EOF at the end of the segment (including a truncated tail).
func (r *segmentReader) next() (source.Frame, error) { return r.nextFrom(math.MinInt64) }

// nextFrom returns the next frame at or after unix ns minTime, skipping earlier records without allocating.
func (r *segmentReader) nextFrom(minTime int64) (source.Frame, error) {
	var fixed [timestampLen + 2]byte
	for {
		delta, err := binary.ReadUvarint(r.br)
		if err != nil {
			return source.Frame{}, eof(err)
		}
		if _, err := io.ReadFull(r.br, fixed[:]); err != nil {
			return source.Frame{}, eof(err)
		}
		if r.t+int64(delta) >= minTime {
			r.t += int64(delta)
			break
		}
		if _, err := r.br.Discard(int(fixed[timestampLen+1])); err != nil {
			return source.Frame{}, eof(err)
		}
		r.t += int64(delta)
	}
	var ts [8]byte
	copy(ts[8-timestampLen:], fixed[:timestampLen])
	data := make([]byte, fixed[timestampLen+1])
	if _, err := io.ReadFull(r.br, data); err != nil {
		return source.Frame{}, eof(err)
	}
	sig := fixed[timestampLen]
	return source.Frame{
		Data:      data,
		Time:      time.Unix(0, r.t),
		Timestamp: binary.BigEndian.Uint64(ts[:]),
		Signal:    sig,
		RSSI:      source.SignalRSSI(sig),
	}, nil
}

// eof maps the end of the data, complete or cut short, to io.EOF.
func eof(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return io.EOF
	}
	return err
}

func (r *segmentReader) Close() error {
	r.gz.Close()
	return r.file.Close()
}
