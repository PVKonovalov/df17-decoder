package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestParseAVR(t *testing.T) {
	tests := []struct {
		line    string
		data    string
		ts      uint64
		wantErr bool
	}{
		{line: "*8D4840D6202CC371C32CE0576098;", data: "8d4840d6202cc371c32ce0576098"},
		{line: "*5D4840D6202CC3;", data: "5d4840d6202cc3"},
		{line: "  *5d4840d6202cc3;\r", data: "5d4840d6202cc3"},
		{line: "@00000012ABCD8D4840D6202CC371C32CE0576098;", data: "8d4840d6202cc371c32ce0576098", ts: 0x12ABCD},
		{line: "*8D4840D6202CC371C32CE0576098", wantErr: true},
		{line: "8D4840D6202CC371C32CE0576098;", wantErr: true},
		{line: "*8D4840D6202C;", wantErr: true},
		{line: "*ZZ4840D6202CC3;", wantErr: true},
		{line: "@0012ABCD8D4840D6202CC3;", wantErr: true},
		{line: "*;", wantErr: true},
		{line: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			data, ts, err := ParseAVR([]byte(tt.line))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %x", data)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", data); got != tt.data || ts != tt.ts {
				t.Errorf("got %s ts=%d, want %s ts=%d", got, ts, tt.data, tt.ts)
			}
		})
	}
}

func collect(ctx context.Context, t *testing.T, src Source) ([]Frame, error) {
	t.Helper()
	out := make(chan Frame)
	errc := make(chan error, 1)
	go func() { errc <- src.Run(ctx, out); close(out) }()
	var frames []Frame
	for f := range out {
		frames = append(frames, f)
	}
	return frames, <-errc
}

const sample = "*8D4840D6202CC371C32CE0576098;\n\ngarbage\n*5D4840D6202CC3;\n"

func TestFile_ReplaysAndCountsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.txt")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	src, _ := New("file", path, Options{Logger: quietLogger()})

	frames, err := collect(t.Context(), t, src)
	if err != nil {
		t.Fatal(err)
	}
	snap := src.Stats().Snapshot()
	if len(frames) != 2 || snap.Frames != 2 || snap.Malformed != 1 {
		t.Errorf("frames=%d stats=%+v, want 2 frames, 1 malformed", len(frames), snap)
	}
}

func TestFile_PacesByTimestamp(t *testing.T) {
	// two frames 0.6 s apart at 12 MHz, replayed at 3x speed => ~200 ms
	content := fmt.Sprintf("@%012X5D4840D6202CC3;\n@%012X5D4840D6202CC3;\n", 1000, 1000+7_200_000)
	path := filepath.Join(t.TempDir(), "c.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	src, _ := New("file", path, Options{ReplaySpeed: 3, Logger: quietLogger()})

	start := time.Now()
	frames, err := collect(t.Context(), t, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 {
		t.Fatalf("got %d frames", len(frames))
	}
	if wall := time.Since(start); wall < 150*time.Millisecond || wall > time.Second {
		t.Errorf("replay took %s, want ~200ms", wall)
	}
	if gap := frames[1].Time.Sub(frames[0].Time); gap != 600*time.Millisecond {
		t.Errorf("frame time gap = %s, want 600ms capture time", gap)
	}
}

func TestFile_FastReplayKeepsCaptureTiming(t *testing.T) {
	content := fmt.Sprintf("@%012X5D4840D6202CC3;\n*5D4840D6202CC3;\n@%012X5D4840D6202CC3;\n", 1000, 1000+clockHz*30)
	path := filepath.Join(t.TempDir(), "c.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	src, _ := New("file", path, Options{ReplaySpeed: 0, Logger: quietLogger()})

	start := time.Now()
	frames, err := collect(t.Context(), t, src)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Error("speed 0 replay should not sleep")
	}
	if !frames[1].Time.Equal(frames[0].Time) {
		t.Errorf("untimestamped line: time %v, want previous frame time %v", frames[1].Time, frames[0].Time)
	}
	if gap := frames[2].Time.Sub(frames[0].Time); gap != 30*time.Second {
		t.Errorf("gap = %s, want 30s capture time", gap)
	}
}

func TestFile_CancelStopsPacing(t *testing.T) {
	content := fmt.Sprintf("@%012X5D4840D6202CC3;\n@%012X5D4840D6202CC3;\n", 1, 1+clockHz*3600)
	path := filepath.Join(t.TempDir(), "c.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	src, _ := New("file", path, Options{ReplaySpeed: 1, Logger: quietLogger()})

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := collect(ctx, t, src); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want deadline exceeded", err)
	}
}

func TestTCP_ReconnectsAndStopsOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte(sample))
			conn.Close()
		}
	}()

	src, _ := New("tcp", ln.Addr().String(), Options{
		ReconnectMin: 10 * time.Millisecond,
		ReconnectMax: 20 * time.Millisecond,
		Logger:       quietLogger(),
	})

	ctx, cancel := context.WithCancel(t.Context())
	out := make(chan Frame)
	errc := make(chan error, 1)
	go func() { errc <- src.Run(ctx, out) }()

	for range 6 { // 2 frames per connection => at least 3 connections
		select {
		case <-out:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for frames")
		}
	}
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}

	if snap := src.Stats().Snapshot(); snap.Reconnects < 2 || snap.Malformed < 3 || snap.Connected {
		t.Errorf("stats = %+v", snap)
	}
}

func TestTCP_CancelWhileConnectedIdle(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- c
		}
	}()

	src, _ := New("tcp", ln.Addr().String(), Options{ReconnectMin: time.Second, ReconnectMax: time.Second, Logger: quietLogger()})
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- src.Run(ctx, make(chan Frame)) }()

	c := <-accepted
	defer c.Close()
	cancel()
	select {
	case <-errc:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop while blocked on read")
	}
}

func TestNew_UnknownScheme(t *testing.T) {
	if _, err := New("udp", "x", Options{}); err == nil {
		t.Error("expected error")
	}
	if src, err := New("beast", "x:1", Options{}); err != nil || src.(*TCP).Format != Beast {
		t.Errorf("beast: src = %v, err = %v", src, err)
	}
}
