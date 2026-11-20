package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/config"
	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/history"
)

// testConfig is the default config without the tape, so tests don't record into the package directory.
func testConfig() config.Config {
	cfg := config.Default()
	cfg.Tape.Enabled = false
	return cfg
}

func captureConfig() config.Config {
	cfg := testConfig()
	cfg.Source.URL = "file://../../testdata/capture.txt"
	cfg.Source.ReplaySpeed = 0
	cfg.Decoder.FixSingleBitErrors = true
	cfg.Receiver.Lat, cfg.Receiver.Lon = 52.0, 4.4 // the mode-s.org vectors were recorded near Delft
	cfg.API.Listen = "127.0.0.1:0"
	return cfg
}

func newService(t *testing.T, cfg config.Config) (*Service, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	svc, err := New(cfg, NewLogger(cfg.Log, &buf))
	if err != nil {
		t.Fatal(err)
	}
	return svc, &buf
}

func TestIngest_ReplaysCapture(t *testing.T) {
	svc, buf := newService(t, captureConfig())
	if err := svc.Ingest(t.Context()); err != nil {
		t.Fatal(err)
	}

	st := svc.Stats.Snapshot(time.Now())
	src := svc.SourceStats()
	if st.Frames != 16 || src.Malformed != 2 || st.Messages != 13 {
		t.Errorf("frames=%d malformed=%d messages=%d, want 16 2 13", st.Frames, src.Malformed, st.Messages)
	}
	if st.FixedBitErrors != 1 || st.CRCFailures != 1 || st.Invalid != 1 || st.Unsupported != 0 {
		t.Errorf("fixed=%d crc=%d invalid=%d unsupported=%d, want 1 1 1 0", st.FixedBitErrors, st.CRCFailures, st.Invalid, st.Unsupported)
	}
	if st.Positions != 2 || st.CPRRejected != 0 || st.AddressParityDrops != 1 {
		t.Errorf("positions=%d cprRejected=%d apDrops=%d, want 2 0 1", st.Positions, st.CPRRejected, st.AddressParityDrops)
	}
	if df := st.FramesPerDF; df[17] != 10 || df[4] != 3 || df[5] != 2 || df[11] != 1 {
		t.Errorf("framesPerDf = %v", df)
	}

	byICAO := map[string]bool{}
	for _, a := range svc.Tracker.Snapshot() {
		byICAO[a.ICAO] = true
	}
	for _, icao := range []string{"4840D6", "40621D", "485020", "A05F21", "738ABC"} {
		if !byICAO[icao] {
			t.Errorf("aircraft %s missing", icao)
		}
	}
	if len(byICAO) != 5 {
		t.Errorf("aircraft = %v, want 5", byICAO)
	}

	klm, _ := svc.Tracker.Get(0x4840D6)
	if klm.Callsign != "KLM1023" || klm.AltBaro == nil || *klm.AltBaro != 38000 || !klm.ADSB || klm.MessagesByDF[4] != 1 {
		t.Errorf("4840D6 = %+v", klm)
	}
	ga, _ := svc.Tracker.Get(0x738ABC)
	if ga.ADSB || ga.Squawk != "0243" || ga.AltBaro == nil || *ga.AltBaro != 2600 || ga.Country != "Israel" {
		t.Errorf("738ABC = %+v", ga)
	}

	out := buf.String()
	if n := strings.Count(out, "callsign=KLM1023"); n != 1 {
		t.Errorf("KLM1023 logged %d times, want once", n)
	}
	for _, want := range []string{
		"lat=52.26578 lon=3.93891 method=local-receiver",
		"msg=squawk icao=738ABC squawk=0243 adsb=false",
		`msg="mode-s altitude" icao=738ABC df=4 altitude=2500 onGround=false`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "mode-s altitude"); n != 1 {
		t.Errorf("mode-s altitude logged %d times at info, want 1", n)
	}
}

func writeCapture(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "c.txt")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	return "file://" + path
}

func TestIngest_EmergencyFromTC28(t *testing.T) {
	cfg := testConfig()
	cfg.Log.Level = "debug"
	cfg.Source.URL = writeCapture(t,
		"*8D738062E1092D000000000ABE6D;", // TC28 squawk 1346, no emergency: not logged as emergency
		"*8D738062E10AAA00000000998731;", // squawk 7700: emergency
		"*8D738062E10AAA00000000998731;", // unchanged
		"*8D738062E1092D000000000ABE6D;", // cleared
		"*8D738062F8000000004000A444DE;", // TC31 version 2
	)
	svc, buf := newService(t, cfg)
	if err := svc.Ingest(t.Context()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Count(out, "msg=emergency") != 2 ||
		!strings.Contains(out, "msg=emergency icao=738062 state=general squawk=7700") ||
		!strings.Contains(out, "msg=emergency icao=738062 state=none squawk=1346") {
		t.Errorf("emergency log lines wrong:\n%s", out)
	}
	if a, _ := svc.Tracker.Get(0x738062); a.ADSBVersion == nil || *a.ADSBVersion != 2 || a.Emergency != "" || a.Squawk != "1346" {
		t.Errorf("738062 = %+v", a)
	}
}

func TestIngest_SquawkEmergencyFromSurveillance(t *testing.T) {
	cfg := testConfig()
	cfg.Source.URL = writeCapture(t,
		"*5D738ABCF3536C;", // DF11 makes 738ABC known
		"*2800011C5D5770;", // DF5 squawk 0243
		"*28000AAA392E75;", // DF5 squawk 7700
		"*2800011C5D5770;", // back to 0243
	)
	svc, buf := newService(t, cfg)
	if err := svc.Ingest(t.Context()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Count(out, "msg=emergency") != 2 ||
		!strings.Contains(out, "msg=emergency icao=738ABC state=general squawk=7700") ||
		!strings.Contains(out, "msg=emergency icao=738ABC state=none squawk=0243") {
		t.Errorf("want emergency on 7700 and clear on 0243 only:\n%s", out)
	}
}

func TestIngest_MissingFile(t *testing.T) {
	cfg := testConfig()
	cfg.Source.URL = "file://does/not/exist.txt"
	svc, _ := newService(t, cfg)
	if err := svc.Ingest(t.Context()); err == nil {
		t.Fatal("expected error")
	}
}

func getJSON(t *testing.T, url string, v any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func TestRun_ServesAPIAfterReplayAndShutsDown(t *testing.T) {
	svc, _ := newService(t, captureConfig())
	addr, err := svc.Listen()
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + addr.String()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	var list []map[string]any
	deadline := time.Now().Add(3 * time.Second)
	for len(list) != 5 && time.Now().Before(deadline) {
		getJSON(t, base+"/api/aircraft", &list)
		time.Sleep(10 * time.Millisecond)
	}
	if len(list) != 5 {
		t.Fatalf("aircraft = %d, want 5", len(list))
	}

	var stats map[string]any
	if code := getJSON(t, base+"/api/stats", &stats); code != 200 || stats["aircraft"].(float64) != 5 || stats["frames"].(float64) != 16 {
		t.Errorf("stats %d %v", code, stats)
	}
	if code := getJSON(t, base+"/healthz", nil); code != http.StatusOK {
		t.Errorf("healthz = %d, want 200 right after replay", code)
	}
	if code := getJSON(t, base+"/api/aircraft/40621d", nil); code != http.StatusOK {
		t.Errorf("aircraft/40621d = %d", code)
	}

	// an open SSE client must not block shutdown
	resp, err := http.Get(base + "/api/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if line, _ := bufio.NewReader(resp.Body).ReadString('\n'); !strings.HasPrefix(line, "data: [") {
		t.Fatalf("stream first line = %q", line)
	}

	select {
	case err := <-done:
		t.Fatalf("Run returned before cancel: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRun_ListenError(t *testing.T) {
	cfg := captureConfig()
	svc1, _ := newService(t, cfg)
	addr, err := svc1.Listen()
	if err != nil {
		t.Fatal(err)
	}
	cfg.API.Listen = addr.String()
	svc2, _ := newService(t, cfg)
	if err := svc2.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "api:") {
		t.Errorf("err = %v, want listen error", err)
	}
}

func TestRun_SavesAndReloadsCoverage(t *testing.T) {
	cfg := captureConfig()
	cfg.Coverage.File = filepath.Join(t.TempDir(), "coverage.json")
	svc, _ := newService(t, cfg)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for svc.Stats.Positions.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(cfg.Coverage.File)
	if err != nil {
		t.Fatalf("coverage not saved on shutdown: %v", err)
	}
	var saved struct{ Points []map[string]any }
	if err := json.Unmarshal(data, &saved); err != nil || len(saved.Points) != 2 { // two KLM fixes over the North Sea, 311° and 313°
		t.Fatalf("saved %s (err %v)", data, err)
	}

	svc2, log := newService(t, cfg)
	if n := len(svc2.Coverage.Snapshot().Points); n != 2 || !strings.Contains(log.String(), "coverage loaded") {
		t.Errorf("reloaded %d points; log %s", n, log)
	}
	cfg.Receiver.Lat = 51
	svc3, log := newService(t, cfg)
	if n := len(svc3.Coverage.Snapshot().Points); n != 0 || !strings.Contains(log.String(), "another receiver position") {
		t.Errorf("moved receiver: %d points; log %s", n, log)
	}
}

func TestSaveCoverage_WarnsOnceAndReportsRecovery(t *testing.T) {
	cfg := captureConfig()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.Coverage.File = filepath.Join(dir, "coverage.json")
	svc, log := newService(t, cfg)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		svc.Coverage.Add(geo.Point{Lat: 52.5 + float64(i)/10, Lon: 4.4}, nil, nil, time.Now())
		svc.saveCoverage()
	}
	if n := strings.Count(log.String(), "coverage save failed"); n != 1 {
		t.Fatalf("%d save warnings, want 1; log %s", n, log)
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	svc.saveCoverage()
	if !strings.Contains(log.String(), "coverage save works again") {
		t.Errorf("no recovery message; log %s", log)
	}
	if _, err := os.Stat(cfg.Coverage.File); err != nil {
		t.Errorf("coverage not saved after recovery: %v", err)
	}
}

func TestRun_WritesHistory(t *testing.T) {
	cfg := captureConfig()
	cfg.History.Enabled = true
	cfg.History.File = filepath.Join(t.TempDir(), "history.db")
	svc, _ := newService(t, cfg)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for svc.Stats.Messages.Load() < 13 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	st := svc.Stats.Snapshot(time.Now())
	if st.Frames != 16 || st.Messages != 13 || st.Positions != 2 || st.AddressParityDrops != 1 {
		t.Errorf("decoder counts changed: frames=%d messages=%d positions=%d apDrops=%d", st.Frames, st.Messages, st.Positions, st.AddressParityDrops)
	}
	// 40621D's two fixes are 3 s apart at the same altitude: thinning keeps both only when they fall in different
	// 5 s slots, which depends on the wall time the replay is anchored to
	h := svc.History.Stats()
	if h.PointsWritten < 1 || h.PointsWritten > 2 || h.Dropped != 0 || h.WriteErrors != 0 || h.FlightsOpen != 0 {
		t.Errorf("history stats = %+v", h)
	}

	store, err := history.Open(history.Options{File: cfg.History.File})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var flights, closed, adsb, points, samples int
	if err := store.DB().QueryRow(`SELECT count(*), sum(closed), sum(adsb), sum(points),
		(SELECT count(*) FROM samples) FROM flights`).Scan(&flights, &closed, &adsb, &points, &samples); err != nil {
		t.Fatal(err)
	}
	if flights != 5 || closed != 5 || adsb != 4 || uint64(points) != h.PointsWritten || samples != 1 {
		t.Errorf("flights=%d closed=%d adsb=%d points=%d samples=%d", flights, closed, adsb, points, samples)
	}
	var alt int
	if err := store.DB().QueryRow(`SELECT s.alt FROM samples s JOIN flights f ON f.id = s.flight_id WHERE f.icao = '738ABC'`).Scan(&alt); err != nil || alt != 2600 {
		t.Errorf("738ABC sample alt %d (%v); replies at the same time replace the sample", alt, err)
	}
	var callsign, squawk string
	if err := store.DB().QueryRow(`SELECT callsign FROM flights WHERE icao = '4840D6'`).Scan(&callsign); err != nil || callsign != "KLM1023" {
		t.Errorf("4840D6 callsign %q (%v)", callsign, err)
	}
	if err := store.DB().QueryRow(`SELECT squawk FROM flights WHERE icao = '738ABC' AND adsb = 0`).Scan(&squawk); err != nil || squawk != "0243" {
		t.Errorf("738ABC squawk %q (%v)", squawk, err)
	}
}

func TestRun_RecordsTape(t *testing.T) {
	cfg := captureConfig()
	cfg.Tape.Enabled = true
	cfg.Tape.Dir = filepath.Join(t.TempDir(), "tape")
	svc, _ := newService(t, cfg)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for svc.Stats.Messages.Load() < 13 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// every frame is recorded before decoding, including the CRC failure and the rejected ones
	if st := svc.Tape.Stats(); st.FramesWritten != 16 || st.Dropped != 0 || st.WriteErrors != 0 || st.Bytes == 0 || st.Recording {
		t.Errorf("tape stats = %+v", st)
	}
	var segs []string
	_ = filepath.WalkDir(cfg.Tape.Dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			segs = append(segs, p)
		}
		return nil
	})
	if len(segs) == 0 || strings.HasSuffix(segs[0], ".part") {
		t.Errorf("segments %v", segs)
	}
}
