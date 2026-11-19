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
		{"bad log level", "log:\n  level: verbose\n", []string{"log.level"}},
		{"all errors reported", "receiver:\n  lat: 100\n  lon: 200\n", []string{"receiver.lat", "receiver.lon"}},
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
	}
	for _, tt := range tests {
		scheme, addr, err := Source{URL: tt.url}.Endpoint()
		if err != nil || scheme != tt.scheme || addr != tt.addr {
			t.Errorf("%s: got %q %q %v, want %q %q", tt.url, scheme, addr, err, tt.scheme, tt.addr)
		}
	}
}
