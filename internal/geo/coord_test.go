package geo

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseLatitude(t *testing.T) {
	tests := []struct {
		in      string
		want    float64
		dms     string
		wantErr string
	}{
		{"250412.00N", 25.07, "250412.00N", ""},
		{"250412N", 25.07, "250412N", ""},
		{"250412.5n", 25 + 4.0/60 + 12.5/3600, "250412.5N", ""},
		{"  250412.00S ", -25.07, "250412.00S", ""},
		{"900000N", 90, "900000N", ""},
		{"25.07", 25.07, "250412.00N", ""},
		{"-0.5", -0.5, "003000.00S", ""},
		{" 12 ", 12, "120000.00N", ""},
		{"+.5", 0.5, "003000.00N", ""},
		{"", 0, "", "empty value"},
		{"   ", 0, "", "empty value"},
		{"0250412.00N", 0, "", "latitude DMS needs 2 degree digits"},
		{"256012N", 0, "", "minutes must be < 60"},
		{"250460N", 0, "", "seconds must be < 60"},
		{"250459.999N", 0, "", ""},
		{"910000N", 0, "", "within ±90°"},
		{"900001N", 0, "", "within ±90°"},
		{"91", 0, "", "within ±90°"},
		{"-90.5", 0, "", "within ±90°"},
		{"250412.00E", 0, "", "hemisphere E is not valid for a latitude"},
		{"250412w", 0, "", "hemisphere W is not valid for a latitude"},
		{"250412X", 0, "", "unknown hemisphere X"},
		{"-250412N", 0, "", "not a DMS value"},
		{"25 04 12N", 0, "", "not a DMS value"},
		{"25.07.1", 0, "", "not a DMS"},
		{"1e3", 0, "", "not a DMS"},
		{"inf", 0, "", "unknown hemisphere F"},
		{"NaN", 0, "", "not a DMS"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseLatitude(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.dms == "" {
				return
			}
			if math.Abs(got.Degrees()-tt.want) > 1e-9 || !got.IsSet() {
				t.Errorf("degrees = %.12f, want %.12f", got.Degrees(), tt.want)
			}
			if got.DMS() != tt.dms {
				t.Errorf("DMS = %q, want %q", got.DMS(), tt.dms)
			}
		})
	}
}

func TestParseLongitude(t *testing.T) {
	tests := []struct {
		in      string
		want    float64
		dms     string
		wantErr string
	}{
		{"0225218.00E", 22 + 52.0/60 + 18.0/3600, "0225218.00E", ""},
		{"0225218E", 22 + 52.0/60 + 18.0/3600, "0225218E", ""},
		{"0225218.00w", -(22 + 52.0/60 + 18.0/3600), "0225218.00W", ""},
		{"1800000W", -180, "1800000W", ""},
		{"22.87", 22.87, "0225212.00E", ""},
		{"-0.5", -0.5, "0003000.00W", ""},
		{"-179.999999", -179.999999, "1800000.00W", ""},
		{"225218.00E", 0, "", "longitude DMS needs 3 degree digits"},
		{"0226018E", 0, "", "minutes must be < 60"},
		{"0225260E", 0, "", "seconds must be < 60"},
		{"1810000E", 0, "", "within ±180°"},
		{"180.5", 0, "", "within ±180°"},
		{"0225218.00N", 0, "", "hemisphere N is not valid for a longitude"},
		{"0225218s", 0, "", "hemisphere S is not valid for a longitude"},
		{"", 0, "", "empty value"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseLongitude(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(got.Degrees()-tt.want) > 1e-9 {
				t.Errorf("degrees = %.12f, want %.12f", got.Degrees(), tt.want)
			}
			if got.DMS() != tt.dms {
				t.Errorf("DMS = %q, want %q", got.DMS(), tt.dms)
			}
		})
	}
}

func TestDMSRoundTrip(t *testing.T) {
	for _, deg := range []float64{0, 25.07, -33.8568, 89.99999, -0.0001} {
		lat, err := ParseLatitude(Lat(deg).DMS())
		if err != nil || math.Abs(lat.Degrees()-deg) > 0.01/3600 {
			t.Errorf("lat %g: DMS %q parsed to %v, %v", deg, Lat(deg).DMS(), lat.Degrees(), err)
		}
	}
	for _, deg := range []float64{0, 22.87, -151.2093, 179.99999} {
		lon, err := ParseLongitude(Lon(deg).DMS())
		if err != nil || math.Abs(lon.Degrees()-deg) > 0.01/3600 {
			t.Errorf("lon %g: DMS %q parsed to %v, %v", deg, Lon(deg).DMS(), lon.Degrees(), err)
		}
	}
}

func TestCoordYAMLAndJSON(t *testing.T) {
	var v struct {
		A, B, C Latitude
		D, E    Longitude
	}
	src := "a: 250412.00N\nb: 25.07\nc: \"-0.5\"\nd: 0225218.00E\ne: 22\n"
	if err := yaml.Unmarshal([]byte(src), &v); err != nil {
		t.Fatal(err)
	}
	if math.Abs(v.A.Degrees()-25.07) > 1e-9 || v.B.Degrees() != 25.07 || v.C.Degrees() != -0.5 || v.E.Degrees() != 22 {
		t.Errorf("got %+v", v)
	}
	if v.A.DMS() != "250412.00N" || v.D.DMS() != "0225218.00E" {
		t.Errorf("DMS = %q, %q", v.A.DMS(), v.D.DMS())
	}

	b, err := json.Marshal(map[string]any{"lat": v.B, "lon": v.D, "neg": v.C})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"lat":25.07,"lon":22.871666666666666,"neg":-0.5}`; string(b) != want {
		t.Errorf("JSON = %s, want %s", b, want)
	}

	for _, bad := range []string{"a: 250412.00E\n", "a: [1]\n", "d: 250412N\n"} {
		if err := yaml.Unmarshal([]byte(bad), &v); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}

	var l Longitude
	if err := l.UnmarshalText([]byte("0225218W")); err != nil || l.Degrees() >= 0 {
		t.Errorf("UnmarshalText = %v, %v", l.Degrees(), err)
	}
}
