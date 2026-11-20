package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_EmptyFileGivesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, ""), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg != Default() {
		t.Errorf("got %+v, want defaults %+v", cfg, Default())
	}
}

func TestLoad_ExampleFileMatchesDefaults(t *testing.T) {
	cfg, err := Load("../../config/config.example.yaml", Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg != Default() {
		t.Errorf("config.example.yaml differs from defaults:\n got %+v\nwant %+v", cfg, Default())
	}
}

func TestLoad_YAMLOverridesDefaultsAndFlagOverridesYAML(t *testing.T) {
	path := writeConfig(t, `
source:
  url: file://testdata/capture.txt
  replaySpeed: 0
receiver:
  lat: 51.5
tracker:
  ttl: 2m
log:
  level: debug
`)
	cfg, err := Load(path, Overrides{LogLevel: "warn"})
	if err != nil {
		t.Fatal(err)
	}

	want := Default()
	want.Source.URL = "file://testdata/capture.txt"
	want.Source.ReplaySpeed = 0
	want.Receiver.Lat = 51.5
	want.Tracker.TTL = Duration(2 * time.Minute)
	want.Log.Level = "warn"
	if cfg != want {
		t.Errorf("got  %+v\nwant %+v", cfg, want)
	}
}

func TestLoad_Errors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr []string
	}{
		{"unknown top-level key", "sorce:\n  url: tcp://a:1\n", []string{"sorce"}},
		{"unknown nested key", "tracker:\n  tll: 10s\n", []string{"tll"}},
		{"bad duration", "tracker:\n  ttl: 10\n", []string{"missing unit"}},
		{"duration not a string", "tracker:\n  ttl: [1]\n", []string{"duration must be a string"}},
		{"lat out of range", "receiver:\n  lat: 91\n", []string{"receiver.lat"}},
		{"lon out of range", "receiver:\n  lon: -181\n", []string{"receiver.lon"}},
		{"negative ttl", "tracker:\n  ttl: -1s\n", []string{"tracker.ttl"}},
		{"zero stream interval", "api:\n  streamInterval: 0s\n", []string{"api.streamInterval"}},
		{"reconnect max below min", "source:\n  reconnectMin: 10s\n  reconnectMax: 1s\n", []string{"source.reconnectMax"}},
		{"bad scheme", "source:\n  url: http://a:1\n", []string{"scheme must be"}},
		{"tcp without port", "source:\n  url: tcp://localhost\n", []string{"host:port"}},
		{"negative playback sessions", "playback:\n  maxSessions: -1\n", []string{"playback.maxSessions"}},
		{"tape with a bad start", "source:\n  url: tape://tape?from=noon\n", []string{"source.url", "RFC 3339"}},
		{"bad log level", "log:\n  level: verbose\n", []string{"log.level"}},
		{"all errors reported", "receiver:\n  lat: 100\n  lon: 200\n", []string{"receiver.lat", "receiver.lon"}},
		{"tile url not http", "map:\n  tileUrl: ftp://t/{z}/{x}/{y}.png\n", []string{"map.tileUrl must be an http(s) URL"}},
		{"tile url without placeholders", "map:\n  tileUrl: https://t.lan/tiles.png\n", []string{"{z}", "{x}", "{y}"}},
		{"max zoom out of range", "map:\n  maxZoom: 30\n", []string{"map.maxZoom"}},
		{"coverage dir missing", "coverage:\n  file: /no/such/dir/coverage.json\n", []string{"coverage.file", "/no/such/dir"}},
		{"history dir missing", "history:\n  enabled: true\n  file: /no/such/dir/history.db\n", []string{"history.file", "/no/such/dir"}},
		{"history without file", "history:\n  enabled: true\n  file: \"\"\n", []string{"history.file must be set"}},
		{"history durations", "history:\n  flushInterval: 0s\n  flightGap: 0s\n  retention: -1h\n", []string{"history.flushInterval", "history.flightGap", "history.retention"}},
		{"tape segment", "tape:\n  segment: 7m\n", []string{"tape.segment must divide a day"}},
		{"tape flush interval", "tape:\n  segment: 10m\n  flushInterval: 10m\n", []string{"tape.flushInterval"}},
		{"tape dir missing", "tape:\n  dir: /no/such/dir/tape\n", []string{"tape.dir", "/no/such/dir"}},
		{"tape values", "tape:\n  retention: -1h\n  minFreeMB: -1\n", []string{"tape.retention", "tape.minFreeMB"}},
		{"trails durations", "trails:\n  window: 0s\n  minInterval: 0s\n", []string{"trails.window", "trails.minInterval"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.yaml), Overrides{})
			if err == nil {
				t.Fatal("expected error")
			}
			for _, w := range tt.wantErr {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
		})
	}
}

func TestLoad_CoverageDirNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to read-only directories")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	_, err := Load(writeConfig(t, "coverage:\n  file: "+filepath.Join(dir, "coverage.json")+"\n"), Overrides{})
	if err == nil || !strings.Contains(err.Error(), "coverage.file") || !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("err = %v, want a not-writable coverage.file error", err)
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(writeConfig(t, "coverage:\n  file: "+filepath.Join(dir, "coverage.json")+"\n"), Overrides{}); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("write check left %d files behind", len(entries))
	}
}

func TestLoad_BadFlagOverride(t *testing.T) {
	if _, err := Load(writeConfig(t, ""), Overrides{LogLevel: "loud"}); err == nil {
		t.Fatal("expected error for invalid -log-level")
	}
}

func TestLoad_EmptyPathGivesDefaults(t *testing.T) {
	cfg, err := Load("", Overrides{LogLevel: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.Log.Level = "debug"
	if cfg != want {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml"), Overrides{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestEndpoint(t *testing.T) {
	tests := []struct {
		url, scheme, addr string
	}{
		{"tcp://localhost:30002", "tcp", "localhost:30002"},
		{"beast://10.0.0.5:30005", "beast", "10.0.0.5:30005"},
		{"file://testdata/capture.txt", "file", "testdata/capture.txt"},
		{"file:///abs/capture.txt", "file", "/abs/capture.txt"},
		{"tape://tape", "tape", "tape"},
		{"tape:///var/lib/df17-decoder/tape?from=2026-11-20T10:00:00Z&speed=8", "tape", "/var/lib/df17-decoder/tape"},
	}
	for _, tt := range tests {
		scheme, addr, err := Source{URL: tt.url}.Endpoint()
		if err != nil || scheme != tt.scheme || addr != tt.addr {
			t.Errorf("%s: got %q %q %v, want %q %q", tt.url, scheme, addr, err, tt.scheme, tt.addr)
		}
	}
}
