package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"testing"
	"time"
)

// beastFrame encodes one Beast frame, doubling every 0x1A after the leading escape.
func beastFrame(typ byte, ts uint64, sig byte, data []byte) []byte {
	body := []byte{byte(ts >> 40), byte(ts >> 32), byte(ts >> 24), byte(ts >> 16), byte(ts >> 8), byte(ts), sig}
	body = append(body, data...)
	out := []byte{beastEsc, typ}
	for _, b := range body {
		out = append(out, b)
		if b == beastEsc {
			out = append(out, beastEsc)
		}
	}
	return out
}

var (
	beastLong  = []byte{0x8D, 0x48, 0x40, 0xD6, 0x20, 0x2C, 0xC3, 0x71, 0xC3, 0x2C, 0xE0, 0x57, 0x60, 0x98}
	beastShort = []byte{0x5D, 0x48, 0x40, 0xD6, 0x20, 0x2C, 0xC3}
	beastEscd  = []byte{0x1A, 0x48, 0x1A, 0x1A, 0x20, 0x2C, 0x1A} // payload full of escape bytes
)

func runBeast(t *testing.T, stream []byte) ([]Frame, *Stats) {
	t.Helper()
	var stats Stats
	out := make(chan Frame, 64)
	if err := readBeast(t.Context(), bytes.NewReader(stream), out, &stats, nil); err != nil {
		t.Fatal(err)
	}
	close(out)
	var frames []Frame
	for f := range out {
		frames = append(frames, f)
	}
	return frames, &stats
}

func TestReadBeast(t *testing.T) {
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	long := beastFrame('3', 0x0102030405_06, 0x80, beastLong)
	short := beastFrame('2', 0x1A1A00001A1A, 0x1A, beastShort)
	escd := beastFrame('2', 7, 255, beastEscd)
	modeAC := beastFrame('1', 9, 100, []byte{0x12, 0x34})
	status := cat([]byte{beastEsc, '4'}, []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9})

	tests := []struct {
		name      string
		stream    []byte
		want      []string
		malformed uint64
	}{
		{"long and short", cat(long, short), []string{"8d4840d6202cc371c32ce0576098", "5d4840d6202cc3"}, 0},
		{"escaped payload", escd, []string{"1a481a1a202c1a"}, 0},
		{"mode A/C and status skipped", cat(modeAC, status, long), []string{"8d4840d6202cc371c32ce0576098"}, 0},
		{"leading garbage", cat([]byte{1, 2, 3, beastEsc, beastEsc, 4}, short), []string{"5d4840d6202cc3"}, 1},
		{"garbage between frames", cat(long, []byte{0xFF, 0xEE}, short), []string{"8d4840d6202cc371c32ce0576098", "5d4840d6202cc3"}, 1},
		{"unknown type", cat([]byte{beastEsc, 'x', 1, 2}, short), []string{"5d4840d6202cc3"}, 1},
		{"frame cut by next frame", cat(long[:10], short), []string{"5d4840d6202cc3"}, 1},
		{"truncated at EOF", cat(short, long[:12]), []string{"5d4840d6202cc3"}, 1},
		{"empty", nil, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frames, stats := runBeast(t, tt.stream)
			var got []string
			for _, f := range frames {
				got = append(got, fmt.Sprintf("%x", f.Data))
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("frames = %v, want %v", got, tt.want)
			}
			snap := stats.Snapshot()
			if snap.Malformed != tt.malformed || snap.Frames != uint64(len(tt.want)) {
				t.Errorf("stats = %+v, want malformed %d", snap, tt.malformed)
			}
		})
	}
}

func TestReadBeast_TimestampAndRSSI(t *testing.T) {
	frames, _ := runBeast(t, bytes.Join([][]byte{
		beastFrame('3', 0x0102030405_06, 255, beastLong),
		beastFrame('2', 0x1A1A00001A1A, 0x1A, beastShort),
		beastFrame('2', 1, 0, beastShort),
	}, nil))
	if len(frames) != 3 {
		t.Fatalf("got %d frames", len(frames))
	}
	if frames[0].Timestamp != 0x010203040506 || frames[0].RSSI == nil || *frames[0].RSSI != 0 {
		t.Errorf("frame 0: ts=%x rssi=%v", frames[0].Timestamp, frames[0].RSSI)
	}
	want := 20 * math.Log10(26.0/255)
	if frames[1].Timestamp != 0x1A1A00001A1A || frames[1].RSSI == nil || math.Abs(*frames[1].RSSI-want) > 1e-9 {
		t.Errorf("frame 1: ts=%x rssi=%v, want %.2f", frames[1].Timestamp, frames[1].RSSI, want)
	}
	if frames[2].RSSI != nil {
		t.Errorf("frame 2: rssi=%v, want nil", *frames[2].RSSI)
	}
}

func TestTCP_Beast(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Write(beastFrame('3', 1, 200, beastLong))
		conn.Write(beastFrame('2', 2, 100, beastEscd))
		time.Sleep(time.Second)
	}()

	src, err := New("beast", ln.Addr().String(), Options{ReconnectMin: time.Second, ReconnectMax: time.Second, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	out := make(chan Frame)
	errc := make(chan error, 1)
	go func() { errc <- src.Run(ctx, out) }()

	var got []string
	for range 2 {
		select {
		case f := <-out:
			if f.RSSI == nil {
				t.Error("missing RSSI")
			}
			got = append(got, fmt.Sprintf("%x", f.Data))
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for frames")
		}
	}
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if want := "[8d4840d6202cc371c32ce0576098 1a481a1a202c1a]"; fmt.Sprint(got) != want {
		t.Errorf("frames = %v, want %v", got, want)
	}
}

func TestFile_Beast(t *testing.T) {
	var stream []byte
	stream = append(stream, beastFrame('3', 1000, 128, beastLong)...)
	stream = append(stream, 0x55, 0x66)                                // junk: one malformed run
	stream = append(stream, beastFrame('4', 1000, 0, []byte{0, 0})...) // status frame, skipped
	stream = append(stream, beastFrame('2', 1000+7_200_000, 0x1A, beastEscd)...)
	path := t.TempDir() + "/c.bin"
	if err := os.WriteFile(path, stream, 0o600); err != nil {
		t.Fatal(err)
	}
	src, _ := New("file", path, Options{ReplaySpeed: 3, Logger: quietLogger()})

	start := time.Now()
	frames, err := collect(t.Context(), t, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || !bytes.Equal(frames[0].Data, beastLong) || !bytes.Equal(frames[1].Data, beastEscd) {
		t.Fatalf("frames: %+v", frames)
	}
	if gap := frames[1].Time.Sub(frames[0].Time); gap != 600*time.Millisecond {
		t.Errorf("capture time gap = %s, want 600ms", gap)
	}
	if wall := time.Since(start); wall < 150*time.Millisecond || wall > time.Second {
		t.Errorf("replay took %s, want ~200ms at 3x", wall)
	}
	if frames[0].RSSI == nil || math.Abs(*frames[0].RSSI-20*math.Log10(128.0/255)) > 1e-9 || frames[1].RSSI == nil {
		t.Errorf("rssi: %v %v", frames[0].RSSI, frames[1].RSSI)
	}
	if m := src.Stats().Snapshot().Malformed; m != 1 {
		t.Errorf("malformed = %d, want 1", m)
	}
}
