package app

import (
	"bytes"
	"os"
	"path/filepath"
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

	if c.Frames != 16 || c.Source.Malformed != 2 {
		t.Errorf("frames=%d malformed=%d, want 16 and 2", c.Frames, c.Source.Malformed)
	}
	if c.Fixed != 1 || c.CRCFailures != 1 || c.Invalid != 1 {
		t.Errorf("fixed=%d crc=%d invalid=%d, want 1 each", c.Fixed, c.CRCFailures, c.Invalid)
	}
	if c.Decoded != 13 || c.Unsupported != 0 || c.Positions != 2 || c.CPRRejected != 0 {
		t.Errorf("decoded=%d unsupported=%d positions=%d cprRejected=%d, want 13, 0, 2, 0", c.Decoded, c.Unsupported, c.Positions, c.CPRRejected)
	}
	if c.AddressParityDrops != 1 || c.PerDF[4] != 3 || c.PerDF[5] != 2 || c.PerDF[11] != 1 {
		t.Errorf("apDrops=%d DF4=%d DF5=%d DF11=%d, want 1 3 2 1", c.AddressParityDrops, c.PerDF[4], c.PerDF[5], c.PerDF[11])
	}
	for _, want := range []string{
		"msg=squawk icao=738ABC squawk=0243 adsb=false",
		"msg=\"mode-s altitude\" icao=738ABC df=4 altitude=2500 onGround=false",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log missing %q:\n%s", want, buf.String())
		}
	}
	if n := strings.Count(buf.String(), "mode-s altitude"); n != 1 {
		t.Errorf("mode-s altitude logged %d times at info, want 1 (only first, non-ADS-B)", n)
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

func TestRun_SquawkEmergencyFromSurveillance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.txt")
	frames := strings.Join([]string{
		"*5D738ABCF3536C;", // DF11 makes 738ABC known
		"*2800011C5D5770;", // DF5 squawk 0243
		"*28000AAA392E75;", // DF5 squawk 7700
		"*2800011C5D5770;", // back to 0243
	}, "\n")
	if err := os.WriteFile(path, []byte(frames), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Source.URL = "file://" + path
	var buf bytes.Buffer
	if _, err := Run(t.Context(), cfg, NewLogger(cfg.Log, &buf)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Count(out, "msg=emergency") != 2 ||
		!strings.Contains(out, "msg=emergency icao=738ABC state=general squawk=7700") ||
		!strings.Contains(out, "msg=emergency icao=738ABC state=none squawk=0243") {
		t.Errorf("want emergency on 7700 and clear on 0243 only:\n%s", out)
	}
}

func TestRun_EmergencyLoggedOnChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.txt")
	frames := strings.Join([]string{
		"*8D738062E1092D000000000ABE6D;", // TC28 squawk 1346, no emergency: not logged
		"*8D738062E10AAA00000000998731;", // squawk 7700: emergency
		"*8D738062E10AAA00000000998731;", // unchanged: not logged again
		"*8D738062E1092D000000000ABE6D;", // cleared
		"*8D738062F8000000004000A444DE;", // TC31 version 2
	}, "\n")
	if err := os.WriteFile(path, []byte(frames), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Source.URL = "file://" + path
	cfg.Log.Level = "debug"

	var buf bytes.Buffer
	c, err := Run(t.Context(), cfg, NewLogger(cfg.Log, &buf))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if c.Decoded != 5 {
		t.Errorf("decoded = %d, want 5", c.Decoded)
	}
	if strings.Count(out, "msg=emergency") != 2 ||
		!strings.Contains(out, "msg=emergency icao=738062 state=general squawk=7700") ||
		!strings.Contains(out, "msg=emergency icao=738062 state=none squawk=1346") {
		t.Errorf("emergency log lines wrong:\n%s", out)
	}
	if !strings.Contains(out, "adsbVersion=2") {
		t.Errorf("version not logged:\n%s", out)
	}
}
