package config

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
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
	if !reflect.DeepEqual(cfg, Default()) {
		t.Errorf("got %+v, want defaults %+v", cfg, Default())
	}
}

func TestLoad_ExampleFileMatchesDefaults(t *testing.T) {
	cfg, err := Load("../../config/config.example.yaml", Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
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
	want.Receiver.Lat = geo.Lat(51.5)
	want.Tracker.TTL = Duration(2 * time.Minute)
	want.Log.Level = "warn"
	if !reflect.DeepEqual(cfg, want) {
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
		{"negative stats retention", "stats:\n  retention: -1h\n", []string{"stats.retention"}},
		{"unknown stats timezone", "stats:\n  timezone: Mars/Olympus\n", []string{"stats.timezone"}},
		{"Local stats timezone", "stats:\n  timezone: Local\n", []string{"stats.timezone"}},
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
		{"receiver lat DMS on the wrong axis", "receiver:\n  lat: 0225218E\n  lon: 250412N\n", []string{
			`receiver.lat "0225218E": hemisphere E is not valid for a latitude`,
			`receiver.lon "250412N": hemisphere N is not valid for a longitude`}},
		{"receiver lat not a scalar", "receiver:\n  lat: [1]\n", []string{"lat: must be a number or a string"}},
		{"receiver unknown key", "receiver:\n  alt: 10\n", []string{"field alt not found"}},
		{"label zoom out of range", "map:\n  waypointLabelMinZoom: 0\n", []string{"map.waypointLabelMinZoom"}},
		{"waypoint unknown key", "map:\n  waypoints:\n    - {name: A, lat: 1, lon: 2, colour: red}\n", []string{"field colour not found"}},
		{"waypoints file missing", "map:\n  waypointsFile: nope.yaml\n", []string{"map.waypointsFile", "nope.yaml"}},
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
	if !reflect.DeepEqual(cfg, want) {
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

func TestLoad_ReceiverFormats(t *testing.T) {
	tests := []struct {
		yaml     string
		lat, lon float64
	}{
		{"receiver:\n  lat: 25.07\n  lon: 22.87\n", 25.07, 22.87},
		{"receiver:\n  lat: \"-33.5\"\n  lon: -70\n", -33.5, -70},
		{"receiver:\n  lat: 250412.00N\n  lon: 0225218.00E\n", 25.07, 22 + 52.0/60 + 18.0/3600},
		{"receiver:\n  lat: 250412s\n  lon: \"0225218W\"\n", -25.07, -(22 + 52.0/60 + 18.0/3600)},
	}
	for _, tt := range tests {
		cfg, err := Load(writeConfig(t, tt.yaml), Overrides{})
		if err != nil {
			t.Fatal(err)
		}
		if p := cfg.Receiver.Point(); math.Abs(p.Lat-tt.lat) > 1e-9 || math.Abs(p.Lon-tt.lon) > 1e-9 {
			t.Errorf("%q: got %v, want %v,%v", tt.yaml, p, tt.lat, tt.lon)
		}
	}

	cfg, err := Load(writeConfig(t, "receiver:\n  lat: 25.07\n  lon: 22.87\n  maxRangeNm: 300\n"), Overrides{})
	if err != nil || !reflect.DeepEqual(cfg, Default()) {
		t.Errorf("decimal receiver config differs from the defaults: %+v, %v", cfg.Receiver, err)
	}
}

func TestLoad_Waypoints(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "waypoints.yaml"), []byte(`
- name: DELTA
  lat: 245030N
  lon: 0230000E
  icon: hexagon
- name: ECHO
  lat: 25.3
  lon: 22.5
`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
map:
  waypointsFile: waypoints.yaml
  waypointLabelMinZoom: 7
  waypoints:
    - name: ALPHA
      lat: 250412.00N
      lon: 0225218.00E
      icon: Star
      color: "#f80"
      note: fictional
    - name: Bravo
      lat: 25.0883
      lon: "22.95"
      icon: TriangleOpen
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	wps := cfg.Map.Waypoints
	if len(wps) != 4 || cfg.Map.WaypointLabelMinZoom != 7 {
		t.Fatalf("got %d waypoints, label zoom %d", len(wps), cfg.Map.WaypointLabelMinZoom)
	}
	a := wps[0]
	if a.Name != "ALPHA" || a.Icon != "star" || a.Color != "#f80" || a.Note != "fictional" ||
		math.Abs(a.Lat.Degrees()-25.07) > 1e-9 || a.Lat.DMS() != "250412.00N" || a.Lon.DMS() != "0225218.00E" {
		t.Errorf("ALPHA = %+v", a)
	}
	if wps[1].Icon != "triangleOpen" || wps[1].Lon.Degrees() != 22.95 {
		t.Errorf("Bravo = %+v", wps[1])
	}
	if wps[2].Name != "DELTA" || wps[2].Icon != "hexagon" || wps[3].Icon != "triangle" || wps[3].Lat.Degrees() != 25.3 {
		t.Errorf("file waypoints = %+v", wps[2:])
	}
}

func TestLoad_WaypointErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "waypoints.yaml"), []byte(`
- name: alpha
  lat: 25
  lon: 22
- name: FOXTROT
  lat: 25
  lon: 0225218N
`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
map:
  waypointsFile: waypoints.yaml
  waypoints:
    - name: ALPHA
      lat: 250412.00N
      lon: 0225218.00E
    - name: ALPHA
      lat: 25
      lon: 22
    - name: CHARLIE
      lat: 25
      lon: 22
      icon: pentagon
    - name: BRAVO
      lat: 326015N
      lon: 0225218.00E
      color: red
    - name: TOOLONGNAME13
      lat: 25
    - lat: 25
      lon: 22
`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, Overrides{})
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		"map.waypoints[1] (ALPHA): duplicate name, also map.waypoints[0] (ALPHA)",
		`map.waypoints[2] (CHARLIE): icon "pentagon" is not one of triangle, triangleOpen, star, diamond, circle, circleOpen, square, hexagon, cross, flag`,
		`map.waypoints[3] (BRAVO): lat "326015N": minutes must be < 60`,
		`map.waypoints[3] (BRAVO): color "red" must be #rgb or #rrggbb`,
		"map.waypoints[4] (TOOLONGNAME13): name must be 1–12 characters, got 13",
		"map.waypoints[4] (TOOLONGNAME13): lon is required",
		"map.waypoints[5]: name must be 1–12 characters, got 0",
		"waypoints.yaml[0] (alpha): duplicate name, also map.waypoints[0] (ALPHA)",
		`waypoints.yaml[1] (FOXTROT): lon "0225218N": hemisphere N is not valid for a longitude`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not contain %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "(BRAVO): lat is required") {
		t.Errorf("a bad lat is also reported as missing:\n%v", err)
	}
}

// TestWaypointIconsMatchUI keeps WaypointIcons and WAYPOINT_ICONS in web/src/map/icons.ts in sync through the
// shared fixture, which the TS icon test compares too.
func TestWaypointIconsMatchUI(t *testing.T) {
	data, err := os.ReadFile("../../web/src/map/waypointIcons.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var ui []string
	if err := json.Unmarshal(data, &ui); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ui, WaypointIcons) {
		t.Errorf("WaypointIcons %v differ from waypointIcons.fixture.json %v", WaypointIcons, ui)
	}
}

func TestLoad_DemoConfig(t *testing.T) {
	cfg, err := Load("../../config/demo.yaml", Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	icons := map[string]bool{}
	for _, w := range cfg.Map.Waypoints {
		icons[w.Icon] = true
	}
	if len(icons) != len(WaypointIcons) {
		t.Errorf("demo waypoints use %d of the %d icons", len(icons), len(WaypointIcons))
	}
}

func TestStatsLocation(t *testing.T) {
	t.Setenv("TZ", "Asia/Tokyo")
	if loc := (Stats{}).Location(); loc.String() != "Asia/Tokyo" {
		t.Errorf("empty timezone gives %v, want the local zone by name", loc)
	}
	t.Setenv("TZ", "Not/AZone")
	if (Stats{}).Location() != time.Local {
		t.Error("an unknown TZ is not time.Local")
	}
	if loc := (Stats{Timezone: "Europe/Berlin"}).Location(); loc.String() != "Europe/Berlin" {
		t.Errorf("location %v", loc)
	}
}
