package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"gopkg.in/yaml.v3"
)

// WaypointIcons are the map symbols a waypoint may use; web/src/map/icons.ts has the same list.
var WaypointIcons = []string{
	"triangle", "triangleOpen", "star", "diamond", "circle", "circleOpen", "square", "hexagon", "cross", "flag",
}

const defaultWaypointIcon = "triangle"

var colorPattern = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// Waypoint is a named fix shown on the map. YAML keys: name, lat, lon, icon, color, note.
type Waypoint struct {
	Name  string
	Lat   geo.Latitude
	Lon   geo.Longitude
	Icon  string // one of WaypointIcons
	Color string // "#rgb" or "#rrggbb"; "" uses the UI default
	Note  string

	ref  string
	errs []fieldError
}

type fieldError struct{ field, msg string }

func (w *Waypoint) UnmarshalYAML(n *yaml.Node) error {
	*w = Waypoint{Icon: defaultWaypointIcon}
	coord := func(field string, parse func(string) error) func(*yaml.Node) error {
		return coordField(parse, func(value string, err error) {
			w.errs = append(w.errs, fieldError{field, fmt.Sprintf("%s %q: %v", field, value, err)})
		})
	}
	return decodeMapping(n, map[string]func(*yaml.Node) error{
		"name": func(v *yaml.Node) error { return v.Decode(&w.Name) },
		"lat": coord("lat", func(s string) (err error) {
			w.Lat, err = geo.ParseLatitude(s)
			return err
		}),
		"lon": coord("lon", func(s string) (err error) {
			w.Lon, err = geo.ParseLongitude(s)
			return err
		}),
		"icon": func(v *yaml.Node) error {
			if err := v.Decode(&w.Icon); err != nil {
				return err
			}
			if i := slices.IndexFunc(WaypointIcons, func(s string) bool { return strings.EqualFold(s, w.Icon) }); i >= 0 {
				w.Icon = WaypointIcons[i]
			}
			return nil
		},
		"color": func(v *yaml.Node) error { return v.Decode(&w.Color) },
		"note":  func(v *yaml.Node) error { return v.Decode(&w.Note) },
	})
}

func (w Waypoint) failed(field string) bool {
	return slices.ContainsFunc(w.errs, func(e fieldError) bool { return e.field == field })
}

// coordField returns a decodeMapping setter that parses a scalar with parse and hands a parse error to fail,
// so Validate can report it with its context; a null value leaves the field unset.
func coordField(parse func(string) error, fail func(value string, err error)) func(*yaml.Node) error {
	return func(v *yaml.Node) error {
		if v.ShortTag() == "!!null" {
			return nil
		}
		if v.Kind != yaml.ScalarNode {
			return errors.New("must be a number or a string")
		}
		if err := parse(v.Value); err != nil {
			fail(v.Value, err)
		}
		return nil
	}
}

// decodeMapping decodes a YAML mapping key by key with fields; unknown and repeated keys are errors, so
// KnownFields still holds for types with their own UnmarshalYAML.
func decodeMapping(n *yaml.Node, fields map[string]func(*yaml.Node) error) error {
	if n.Kind != yaml.MappingNode {
		return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: must be a mapping", n.Line)}}
	}
	var errs []string
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		set, ok := fields[k.Value]
		switch {
		case !ok:
			errs = append(errs, fmt.Sprintf("line %d: field %s not found", k.Line, k.Value))
		case seen[k.Value]:
			errs = append(errs, fmt.Sprintf("line %d: mapping key %q already defined", k.Line, k.Value))
		default:
			seen[k.Value] = true
			if err := set(v); err != nil {
				errs = append(errs, fmt.Sprintf("line %d: %s: %v", v.Line, k.Value, err))
			}
		}
	}
	if len(errs) > 0 {
		return &yaml.TypeError{Errors: errs}
	}
	return nil
}

// readWaypoints reads a YAML list of waypoints from path; ref names the entries in errors.
func readWaypoints(path, ref string) ([]Waypoint, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var wps []Waypoint
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&wps); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for i := range wps {
		wps[i].ref = fmt.Sprintf("%s[%d]", ref, i)
	}
	return wps, nil
}

// validateWaypoints checks every waypoint: name, coordinates, icon, color and unique names (case-insensitive).
func validateWaypoints(wps []Waypoint) []error {
	var errs []error
	seen := map[string]string{}
	for i, w := range wps {
		at := w.ref
		if at == "" {
			at = fmt.Sprintf("map.waypoints[%d]", i)
		}
		if w.Name != "" {
			at += " (" + w.Name + ")"
		}
		bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(at+": "+format, args...)) }

		if n := utf8.RuneCountInString(w.Name); n == 0 || n > 12 {
			bad("name must be 1–12 characters, got %d", n)
		} else if prev, dup := seen[strings.ToUpper(w.Name)]; dup {
			bad("duplicate name, also %s", prev)
		} else {
			seen[strings.ToUpper(w.Name)] = at
		}
		for _, e := range w.errs {
			bad("%s", e.msg)
		}
		if !w.Lat.IsSet() && !w.failed("lat") {
			bad("lat is required")
		}
		if !w.Lon.IsSet() && !w.failed("lon") {
			bad("lon is required")
		}
		if !slices.Contains(WaypointIcons, w.Icon) {
			bad("icon %q is not one of %s", w.Icon, strings.Join(WaypointIcons, ", "))
		}
		if w.Color != "" && !colorPattern.MatchString(w.Color) {
			bad("color %q must be #rgb or #rrggbb", w.Color)
		}
	}
	return errs
}
