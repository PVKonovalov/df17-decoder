package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/PVKonovalov/df17-decoder/internal/config"
)

func TestRun_ReplaysCapture(t *testing.T) {
	cfg := config.Default()
	cfg.Source.URL = "file://../../testdata/capture.txt"
	cfg.Source.ReplaySpeed = 0
	cfg.Decoder.FixSingleBitErrors = true
	cfg.Receiver.Lat, cfg.Receiver.Lon = 52.0, 4.4 // the mode-s.org vectors were recorded near Delft

	var buf bytes.Buffer
	c, err := Run(t.Context(), cfg, NewLogger(cfg.Log, &buf))
	if err != nil {
		t.Fatal(err)
	}

	if c.Frames != 10 || c.Source.Malformed != 2 {
		t.Errorf("frames=%d malformed=%d, want 10 and 2", c.Frames, c.Source.Malformed)
	}
	if c.Fixed != 1 || c.CRCFailures != 1 || c.Invalid != 1 {
		t.Errorf("fixed=%d crc=%d invalid=%d, want 1 each", c.Fixed, c.CRCFailures, c.Invalid)
	}
	if c.Decoded != 5 || c.Positions != 2 || c.CPRRejected != 0 {
		t.Errorf("decoded=%d positions=%d cprRejected=%d, want 5, 2, 0", c.Decoded, c.Positions, c.CPRRejected)
	}
	if !strings.Contains(buf.String(), "lat=52.26578 lon=3.93891 method=local-receiver") ||
		!strings.Contains(buf.String(), "altitude=38000") {
		t.Errorf("first fix not logged as expected:\n%s", buf.String())
	}
	if c.PerDF[17] != 10 {
		t.Errorf("DF17 count = %d, want 10", c.PerDF[17])
	}
	if n := strings.Count(buf.String(), "callsign=KLM1023"); n != 1 {
		t.Errorf("KLM1023 logged %d times, want once:\n%s", n, buf.String())
	}
}

func TestRun_MissingFile(t *testing.T) {
	cfg := config.Default()
	cfg.Source.URL = "file://does/not/exist.txt"
	var buf bytes.Buffer
	if _, err := Run(t.Context(), cfg, NewLogger(cfg.Log, &buf)); err == nil {
		t.Fatal("expected error")
	}
}
