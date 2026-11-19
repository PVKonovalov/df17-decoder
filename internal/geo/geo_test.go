package geo

import (
	"math"
	"testing"
)

func TestDistanceAndBearing(t *testing.T) {
	tests := []struct {
		name         string
		a, b         Point
		distNm, brng float64
	}{
		{"one degree of latitude", Point{0, 0}, Point{1, 0}, 60.04, 0},
		{"one degree east at equator", Point{0, 0}, Point{0, 1}, 60.04, 90},
		{"south", Point{32, 35}, Point{31, 35}, 60.04, 180},
		{"receiver to LLBG", Point{32.09, 34.96}, Point{32.0114, 34.8867}, 6.01, 218.3},
		{"same point", Point{32, 35}, Point{32, 35}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if d := DistanceNm(tt.a, tt.b); math.Abs(d-tt.distNm) > 0.05 {
				t.Errorf("distance = %.3f, want %.2f", d, tt.distNm)
			}
			if tt.distNm == 0 {
				return
			}
			if br := Bearing(tt.a, tt.b); math.Abs(br-tt.brng) > 0.5 {
				t.Errorf("bearing = %.2f, want %.1f", br, tt.brng)
			}
		})
	}
}
