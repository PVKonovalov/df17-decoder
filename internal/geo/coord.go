package geo

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Latitude is a latitude in decimal degrees that remembers the AIP DMS text (DDMMSS[.s]N|S) it was parsed from.
type Latitude struct{ coord }

// Longitude is a longitude in decimal degrees that remembers the AIP DMS text (DDDMMSS[.s]E|W) it was parsed from.
type Longitude struct{ coord }

type coord struct {
	deg float64
	dms string
	set bool
}

type axis struct {
	name      string
	degDigits int
	max       float64
	pos, neg  byte
	other     string
	layout    string
}

var (
	latAxis = axis{"latitude", 2, 90, 'N', 'S', "EW", "DDMMSS[.s]N|S"}
	lonAxis = axis{"longitude", 3, 180, 'E', 'W', "NS", "DDDMMSS[.s]E|W"}

	dmsBody = regexp.MustCompile(`^([0-9]+)(\.[0-9]+)?$`)
	decimal = regexp.MustCompile(`^[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)$`)
)

// Lat returns the latitude deg (decimal degrees).
func Lat(deg float64) Latitude { return Latitude{coord{deg: deg, set: true}} }

// Lon returns the longitude deg (decimal degrees).
func Lon(deg float64) Longitude { return Longitude{coord{deg: deg, set: true}} }

// ParseLatitude parses s as AIP DMS (250412.00N) or decimal degrees (25.07, "-0.5").
func ParseLatitude(s string) (Latitude, error) {
	c, err := parse(s, latAxis)
	return Latitude{c}, err
}

// ParseLongitude parses s as AIP DMS (0225218.00E) or decimal degrees (22.87, "-0.5").
func ParseLongitude(s string) (Longitude, error) {
	c, err := parse(s, lonAxis)
	return Longitude{c}, err
}

func parse(s string, a axis) (coord, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return coord{}, errors.New("empty value")
	}
	last := s[len(s)-1] &^ 0x20
	if last < 'A' || last > 'Z' {
		if !decimal.MatchString(s) {
			return coord{}, fmt.Errorf("not a DMS (%s) or decimal-degree value", a.layout)
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return coord{}, err
		}
		if math.Abs(v) > a.max {
			return coord{}, fmt.Errorf("%s must be within ±%g°, got %g", a.name, a.max, v)
		}
		return coord{deg: v, set: true}, nil
	}

	switch {
	case strings.IndexByte(a.other, last) >= 0:
		return coord{}, fmt.Errorf("hemisphere %c is not valid for a %s", last, a.name)
	case last != a.pos && last != a.neg:
		return coord{}, fmt.Errorf("unknown hemisphere %c, want %c or %c", last, a.pos, a.neg)
	}
	m := dmsBody.FindStringSubmatch(s[:len(s)-1])
	if m == nil {
		return coord{}, fmt.Errorf("not a DMS value (%s)", a.layout)
	}
	if len(m[1]) != a.degDigits+4 {
		return coord{}, fmt.Errorf("%s DMS needs %d degree digits (%s)", a.name, a.degDigits, a.layout)
	}
	d, _ := strconv.Atoi(m[1][:a.degDigits])
	mins, _ := strconv.Atoi(m[1][a.degDigits : a.degDigits+2])
	sec, _ := strconv.ParseFloat(m[1][a.degDigits+2:]+m[2], 64)
	switch {
	case mins >= 60:
		return coord{}, errors.New("minutes must be < 60")
	case sec >= 60:
		return coord{}, errors.New("seconds must be < 60")
	}
	v := float64(d) + float64(mins)/60 + sec/3600
	if v > a.max {
		return coord{}, fmt.Errorf("%s must be within ±%g°", a.name, a.max)
	}
	if last == a.neg {
		v = -v
	}
	return coord{deg: v, dms: s[:len(s)-1] + string(last), set: true}, nil
}

// Degrees returns the value in decimal degrees.
func (c coord) Degrees() float64 { return c.deg }

// IsSet reports whether the value was parsed or constructed, as opposed to the zero value.
func (c coord) IsSet() bool { return c.set }

func (c coord) format(a axis) string {
	if c.dms != "" {
		return c.dms
	}
	h := a.pos
	if c.deg < 0 {
		h = a.neg
	}
	cs := int64(math.Round(math.Abs(c.deg) * 360000))
	return fmt.Sprintf("%0*d%02d%02d.%02d%c", a.degDigits, cs/360000, cs/6000%60, cs/100%60, cs%100, h)
}

func (c coord) MarshalJSON() ([]byte, error) {
	return strconv.AppendFloat(nil, c.deg, 'f', -1, 64), nil
}

// DMS returns the AIP text: as configured when it was parsed from DMS, else built from the degrees (250412.00N).
func (l Latitude) DMS() string { return l.format(latAxis) }

// DMS returns the AIP text: as configured when it was parsed from DMS, else built from the degrees (0225218.00E).
func (l Longitude) DMS() string { return l.format(lonAxis) }

func (l *Latitude) UnmarshalText(b []byte) (err error) {
	*l, err = ParseLatitude(string(b))
	return err
}

func (l *Longitude) UnmarshalText(b []byte) (err error) {
	*l, err = ParseLongitude(string(b))
	return err
}

func (l *Latitude) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: latitude must be a number or a string", n.Line)
	}
	return l.UnmarshalText([]byte(n.Value))
}

func (l *Longitude) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: longitude must be a number or a string", n.Line)
	}
	return l.UnmarshalText([]byte(n.Value))
}
