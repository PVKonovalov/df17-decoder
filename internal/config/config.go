package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/tape"
	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration written in YAML as a Go duration string ("60s", "1m30s").
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return fmt.Errorf("line %d: duration must be a string like \"10s\"", n.Line)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

func (d Duration) D() time.Duration { return time.Duration(d) }

type Config struct {
	Source   Source   `yaml:"source"`
	Receiver Receiver `yaml:"receiver"`
	Decoder  Decoder  `yaml:"decoder"`
	Tracker  Tracker  `yaml:"tracker"`
	Trails   Trails   `yaml:"trails"`
	History  History  `yaml:"history"`
	Tape     Tape     `yaml:"tape"`
	Playback Playback `yaml:"playback"`
	Stats    Stats    `yaml:"stats"`
	API      API      `yaml:"api"`
	Map      Map      `yaml:"map"`
	Coverage Coverage `yaml:"coverage"`
	Log      Log      `yaml:"log"`
}

type Source struct {
	URL          string   `yaml:"url"`
	ReconnectMin Duration `yaml:"reconnectMin"`
	ReconnectMax Duration `yaml:"reconnectMax"`
	ReplaySpeed  float64  `yaml:"replaySpeed"`
}

// Receiver is the receiver position (AIP DMS or decimal degrees) and range. YAML keys: lat, lon, maxRangeNm.
type Receiver struct {
	Lat        geo.Latitude
	Lon        geo.Longitude
	MaxRangeNm float64

	errs []string
}

// Point returns the receiver position in decimal degrees.
func (r Receiver) Point() geo.Point { return geo.Point{Lat: r.Lat.Degrees(), Lon: r.Lon.Degrees()} }

func (r *Receiver) UnmarshalYAML(n *yaml.Node) error {
	coord := func(field string, parse func(string) error) func(*yaml.Node) error {
		return coordField(parse, func(value string, err error) {
			r.errs = append(r.errs, fmt.Sprintf("receiver.%s %q: %v", field, value, err))
		})
	}
	return decodeMapping(n, map[string]func(*yaml.Node) error{
		"lat": coord("lat", func(s string) error {
			v, err := geo.ParseLatitude(s)
			if err == nil {
				r.Lat = v
			}
			return err
		}),
		"lon": coord("lon", func(s string) error {
			v, err := geo.ParseLongitude(s)
			if err == nil {
				r.Lon = v
			}
			return err
		}),
		"maxRangeNm": func(v *yaml.Node) error { return v.Decode(&r.MaxRangeNm) },
	})
}

type Decoder struct {
	FixSingleBitErrors bool     `yaml:"fixSingleBitErrors"`
	CPRPairMaxAge      Duration `yaml:"cprPairMaxAge"`
}

type Tracker struct {
	TTL             Duration `yaml:"ttl"`
	KnownAddressTTL Duration `yaml:"knownAddressTTL"`
}

// Trails configures the per-aircraft track buffer.
type Trails struct {
	Window      Duration `yaml:"window"`
	MinInterval Duration `yaml:"minInterval"`
}

// History configures the SQLite flight history.
type History struct {
	Enabled       bool     `yaml:"enabled"`
	File          string   `yaml:"file"`
	FlushInterval Duration `yaml:"flushInterval"`
	FlightGap     Duration `yaml:"flightGap"`
	Retention     Duration `yaml:"retention"`
}

// Tape configures the raw frame recorder used for playback.
type Tape struct {
	Enabled       bool     `yaml:"enabled"`
	Dir           string   `yaml:"dir"`
	Segment       Duration `yaml:"segment"`
	FlushInterval Duration `yaml:"flushInterval"`
	Retention     Duration `yaml:"retention"`
	MinFreeMB     int      `yaml:"minFreeMB"`
}

// Stats configures the hourly rollups (they need history.enabled).
type Stats struct {
	Retention Duration `yaml:"retention"` // 0 keeps them forever
}

// Playback limits the per-connection tape playback sessions.
type Playback struct {
	MaxSessions int      `yaml:"maxSessions"`
	IdleTimeout Duration `yaml:"idleTimeout"`
}

type API struct {
	Listen         string   `yaml:"listen"`
	StreamInterval Duration `yaml:"streamInterval"`
}

// Map configures the web UI's tile layer (any XYZ tile server) and the waypoints drawn on it.
type Map struct {
	TileURL              string     `yaml:"tileUrl"`
	Attribution          string     `yaml:"attribution"`
	MaxZoom              int        `yaml:"maxZoom"`
	DarkFilter           bool       `yaml:"darkFilter"`
	Waypoints            []Waypoint `yaml:"waypoints"`
	WaypointsFile        string     `yaml:"waypointsFile"` // relative to the config file's directory
	WaypointLabelMinZoom int        `yaml:"waypointLabelMinZoom"`
}

// Coverage configures the range outline (furthest position per bearing).
type Coverage struct {
	File string `yaml:"file"` // JSON file the outline is kept in across restarts; "" keeps it in memory only
}

type Log struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Overrides are values from CLI flags; empty fields leave the YAML value untouched.
type Overrides struct {
	LogLevel string
}

// Default returns the built-in defaults.
func Default() Config {
	return Config{
		Source: Source{
			URL:          "beast://localhost:30005",
			ReconnectMin: Duration(time.Second),
			ReconnectMax: Duration(30 * time.Second),
			ReplaySpeed:  1.0,
		},
		Receiver: Receiver{Lat: geo.Lat(25.07), Lon: geo.Lon(22.87), MaxRangeNm: 300},
		Decoder:  Decoder{CPRPairMaxAge: Duration(10 * time.Second)},
		Tracker:  Tracker{TTL: Duration(60 * time.Second), KnownAddressTTL: Duration(60 * time.Second)},
		Trails:   Trails{Window: Duration(10 * time.Minute), MinInterval: Duration(5 * time.Second)},
		History: History{
			File:          "history.db",
			FlushInterval: Duration(15 * time.Second),
			FlightGap:     Duration(10 * time.Minute),
			Retention:     Duration(720 * time.Hour),
		},
		Tape: Tape{
			Enabled:       true,
			Dir:           "tape",
			Segment:       Duration(time.Hour),
			FlushInterval: Duration(5 * time.Second),
			Retention:     Duration(336 * time.Hour),
			MinFreeMB:     1024,
		},
		Playback: Playback{MaxSessions: 4, IdleTimeout: Duration(30 * time.Minute)},
		Stats:    Stats{Retention: Duration(8760 * time.Hour)},
		API:      API{Listen: ":8080", StreamInterval: Duration(time.Second)},
		Map: Map{
			TileURL:              "https://tile.openstreetmap.org/{z}/{x}/{y}.png",
			Attribution:          `&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors`,
			MaxZoom:              19,
			DarkFilter:           true,
			Waypoints:            []Waypoint{},
			WaypointLabelMinZoom: 9,
		},
		Log: Log{Level: "info", Format: "text"},
	}
}

// Load builds the configuration: defaults < YAML file at path (skipped when path is "") < overrides,
// then validates it.
func Load(path string, ov Overrides) (Config, error) {
	cfg := Default()
	if path != "" {
		if err := decodeFile(path, &cfg); err != nil {
			return cfg, err
		}
		for i := range cfg.Map.Waypoints {
			cfg.Map.Waypoints[i].ref = fmt.Sprintf("map.waypoints[%d]", i)
		}
		if f := cfg.Map.WaypointsFile; f != "" {
			if !filepath.IsAbs(f) {
				f = filepath.Join(filepath.Dir(path), f)
			}
			wps, err := readWaypoints(f, filepath.Base(f))
			if err != nil {
				return cfg, fmt.Errorf("config %s: map.waypointsFile: %w", path, err)
			}
			cfg.Map.Waypoints = append(cfg.Map.Waypoints, wps...)
		}
	}

	if ov.LogLevel != "" {
		cfg.Log.Level = ov.LogLevel
	}

	if err := cfg.Validate(); err != nil {
		if path == "" {
			return cfg, fmt.Errorf("config: %w", err)
		}
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

func decodeFile(path string, cfg *Config) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err = dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("config %s: %w", path, err)
	}
	return nil
}

// Validate reports every invalid value at once.
func (c Config) Validate() error {
	var errs []error
	check := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}

	if _, _, err := c.Source.Endpoint(); err != nil {
		errs = append(errs, err)
	}
	check(c.Source.ReconnectMin > 0, "source.reconnectMin must be positive, got %s", c.Source.ReconnectMin.D())
	check(c.Source.ReconnectMax >= c.Source.ReconnectMin, "source.reconnectMax (%s) must be >= source.reconnectMin (%s)", c.Source.ReconnectMax.D(), c.Source.ReconnectMin.D())
	check(c.Source.ReplaySpeed >= 0, "source.replaySpeed must be >= 0, got %g", c.Source.ReplaySpeed)

	for _, e := range c.Receiver.errs {
		errs = append(errs, errors.New(e))
	}
	lat, lon := c.Receiver.Lat.Degrees(), c.Receiver.Lon.Degrees()
	check(lat >= -90 && lat <= 90, "receiver.lat must be in [-90, 90], got %g", lat)
	check(lon >= -180 && lon <= 180, "receiver.lon must be in [-180, 180], got %g", lon)
	check(c.Receiver.MaxRangeNm > 0, "receiver.maxRangeNm must be positive, got %g", c.Receiver.MaxRangeNm)

	check(c.Decoder.CPRPairMaxAge > 0, "decoder.cprPairMaxAge must be positive, got %s", c.Decoder.CPRPairMaxAge.D())
	check(c.Tracker.TTL > 0, "tracker.ttl must be positive, got %s", c.Tracker.TTL.D())
	check(c.Tracker.KnownAddressTTL > 0, "tracker.knownAddressTTL must be positive, got %s", c.Tracker.KnownAddressTTL.D())

	check(c.Trails.Window > 0, "trails.window must be positive, got %s", c.Trails.Window.D())
	check(c.Trails.MinInterval > 0, "trails.minInterval must be positive, got %s", c.Trails.MinInterval.D())

	check(c.History.FlushInterval > 0, "history.flushInterval must be positive, got %s", c.History.FlushInterval.D())
	check(c.History.FlightGap > 0, "history.flightGap must be positive, got %s", c.History.FlightGap.D())
	check(c.History.Retention >= 0, "history.retention must be >= 0, got %s", c.History.Retention.D())
	if c.History.Enabled {
		if c.History.File == "" {
			errs = append(errs, errors.New("history.file must be set when history.enabled is true"))
		} else if err := checkWritableDir(filepath.Dir(c.History.File)); err != nil {
			errs = append(errs, fmt.Errorf("history.file %q: %w", c.History.File, err))
		}
	}

	seg := c.Tape.Segment.D()
	check(seg > 0 && (24*time.Hour)%seg == 0, "tape.segment must divide a day evenly (e.g. 15m, 1h), got %s", seg)
	check(c.Tape.FlushInterval > 0 && c.Tape.FlushInterval < c.Tape.Segment, "tape.flushInterval must be positive and shorter than tape.segment, got %s", c.Tape.FlushInterval.D())
	check(c.Tape.Retention >= 0, "tape.retention must be >= 0, got %s", c.Tape.Retention.D())
	check(c.Tape.MinFreeMB >= 0, "tape.minFreeMB must be >= 0, got %d", c.Tape.MinFreeMB)
	check(c.Playback.MaxSessions >= 0, "playback.maxSessions must be >= 0, got %d", c.Playback.MaxSessions)
	check(c.Stats.Retention >= 0, "stats.retention must be >= 0, got %s", c.Stats.Retention.D())
	check(c.Playback.IdleTimeout >= 0, "playback.idleTimeout must be >= 0, got %s", c.Playback.IdleTimeout.D())
	if c.Tape.Enabled {
		if err := checkTapeDir(c.Tape.Dir); err != nil {
			errs = append(errs, fmt.Errorf("tape.dir %q: %w", c.Tape.Dir, err))
		}
	}

	_, _, err := net.SplitHostPort(c.API.Listen)
	check(err == nil, "api.listen must be host:port, got %q", c.API.Listen)
	check(c.API.StreamInterval > 0, "api.streamInterval must be positive, got %s", c.API.StreamInterval.D())

	if u, err := url.Parse(c.Map.TileURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("map.tileUrl must be an http(s) URL, got %q", c.Map.TileURL))
	}
	for _, p := range []string{"{z}", "{x}", "{y}"} {
		check(strings.Contains(c.Map.TileURL, p), "map.tileUrl must contain %s, got %q", p, c.Map.TileURL)
	}
	check(c.Map.MaxZoom >= 1 && c.Map.MaxZoom <= 22, "map.maxZoom must be in [1, 22], got %d", c.Map.MaxZoom)
	check(c.Map.WaypointLabelMinZoom >= 1 && c.Map.WaypointLabelMinZoom <= 22, "map.waypointLabelMinZoom must be in [1, 22], got %d", c.Map.WaypointLabelMinZoom)
	errs = append(errs, validateWaypoints(c.Map.Waypoints)...)
	if c.Coverage.File != "" {
		if err := checkWritableDir(filepath.Dir(c.Coverage.File)); err != nil {
			errs = append(errs, fmt.Errorf("coverage.file %q: %w", c.Coverage.File, err))
		}
	}

	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("log.level must be debug|info|warn|error, got %q", c.Log.Level))
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		errs = append(errs, fmt.Errorf("log.format must be text|json, got %q", c.Log.Format))
	}

	return errors.Join(errs...)
}

// checkTapeDir checks that dir, or its parent when dir doesn't exist yet, is a writable directory.
func checkTapeDir(dir string) error {
	if dir == "" {
		return errors.New("must be set when tape.enabled is true")
	}
	if _, err := os.Stat(dir); err != nil {
		return checkWritableDir(filepath.Dir(filepath.Clean(dir)))
	}
	return checkWritableDir(dir)
}

// checkWritableDir returns an error when dir is missing, not a directory, or a temporary file can't be created
// in it (read-only file system, permissions).
func checkWritableDir(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("directory %q does not exist", abs)
	}
	f, err := os.CreateTemp(abs, ".df17-write-check-*")
	if err != nil {
		return fmt.Errorf("directory %q is not writable: %w", abs, err)
	}
	f.Close()
	return os.Remove(f.Name())
}

// Endpoint splits source.url into its scheme (tcp, beast, file) and address (host:port or file path).
func (s Source) Endpoint() (scheme, address string, err error) {
	u, err := url.Parse(s.URL)
	if err != nil {
		return "", "", fmt.Errorf("source.url: %w", err)
	}
	switch u.Scheme {
	case "tcp", "beast":
		if _, port, err := net.SplitHostPort(u.Host); err != nil || port == "" || u.Path != "" {
			return "", "", fmt.Errorf("source.url %q: want %s://host:port", s.URL, u.Scheme)
		}
		return u.Scheme, u.Host, nil
	case "file":
		path := strings.TrimPrefix(s.URL, "file://")
		if path == "" {
			return "", "", fmt.Errorf("source.url %q: missing file path", s.URL)
		}
		return u.Scheme, path, nil
	case "tape":
		dir, _, _, err := tape.ParseURL(s.URL)
		if err != nil {
			return "", "", fmt.Errorf("source.url: %w", err)
		}
		return u.Scheme, dir, nil
	default:
		return "", "", fmt.Errorf("source.url %q: scheme must be tcp, beast, file or tape", s.URL)
	}
}
