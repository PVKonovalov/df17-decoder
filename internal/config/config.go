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

type Receiver struct {
	Lat        float64 `yaml:"lat"`
	Lon        float64 `yaml:"lon"`
	MaxRangeNm float64 `yaml:"maxRangeNm"`
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

type API struct {
	Listen         string   `yaml:"listen"`
	StreamInterval Duration `yaml:"streamInterval"`
}

// Map configures the web UI's tile layer (any XYZ tile server).
type Map struct {
	TileURL     string `yaml:"tileUrl"`
	Attribution string `yaml:"attribution"`
	MaxZoom     int    `yaml:"maxZoom"`
	DarkFilter  bool   `yaml:"darkFilter"`
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
		Receiver: Receiver{Lat: 25.07, Lon: 22.87, MaxRangeNm: 300},
		Decoder:  Decoder{CPRPairMaxAge: Duration(10 * time.Second)},
		Tracker:  Tracker{TTL: Duration(60 * time.Second), KnownAddressTTL: Duration(60 * time.Second)},
		Trails:   Trails{Window: Duration(10 * time.Minute), MinInterval: Duration(5 * time.Second)},
		History: History{
			File:          "history.db",
			FlushInterval: Duration(15 * time.Second),
			FlightGap:     Duration(10 * time.Minute),
			Retention:     Duration(720 * time.Hour),
		},
		API: API{Listen: ":8080", StreamInterval: Duration(time.Second)},
		Map: Map{
			TileURL:     "https://tile.openstreetmap.org/{z}/{x}/{y}.png",
			Attribution: `&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors`,
			MaxZoom:     19,
			DarkFilter:  true,
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

	check(c.Receiver.Lat >= -90 && c.Receiver.Lat <= 90, "receiver.lat must be in [-90, 90], got %g", c.Receiver.Lat)
	check(c.Receiver.Lon >= -180 && c.Receiver.Lon <= 180, "receiver.lon must be in [-180, 180], got %g", c.Receiver.Lon)
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
	default:
		return "", "", fmt.Errorf("source.url %q: scheme must be tcp, beast or file", s.URL)
	}
}
