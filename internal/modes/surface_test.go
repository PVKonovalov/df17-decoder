package modes

import (
	"math"
	"testing"
	"time"
)

// Surface position vectors from The 1090 MHz Riddle, "Surface position" (aircraft at Schiphol).
const (
	vecSurfaceEven = "8C4841753AAB238733C8CD4020B1"
	vecSurfaceOdd  = "8C4841753A9A153237AEF0F275BE"
)

func decodeSurface(t *testing.T, frame string) Message {
	t.Helper()
	m, err := Decode(mustHex(t, frame), time.Time{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if m.SurfacePosition == nil {
		t.Fatalf("%s: no surface position", frame)
	}
	return m
}

func TestDecode_SurfacePositionVector(t *testing.T) {
	tests := []struct {
		frame    string
		odd      bool
		gs, trk  float64
		lat, lon float64
	}{
		{vecSurfaceEven, false, 18, 140.625, 52.32304, 4.73047},
		{vecSurfaceOdd, true, 17, 92.8125, 52.32056, 4.73574},
	}
	for _, tt := range tests {
		m := decodeSurface(t, tt.frame)
		p := m.SurfacePosition
		if m.TC != 7 || m.OnGround == nil || !*m.OnGround || m.ICAO != 0x484175 {
			t.Errorf("%s: tc=%d onGround=%v icao=%06X", tt.frame, m.TC, m.OnGround, m.ICAO)
		}
		if p.CPR.Odd != tt.odd || p.GroundSpeed == nil || *p.GroundSpeed != tt.gs || p.Track == nil || *p.Track != tt.trk {
			t.Errorf("%s: %+v gs=%v trk=%v", tt.frame, p.CPR, p.GroundSpeed, p.Track)
		}
		lat, lon := CPRSurfaceLocal(p.CPR, 51.990, 4.375)
		if !near(lat, tt.lat, 1e-5) || !near(lon, tt.lon, 1e-5) {
			t.Errorf("%s: local %.5f, %.5f, want %.5f, %.5f", tt.frame, lat, lon, tt.lat, tt.lon)
		}
	}
}

// pyModeS reference case: frame 8FC8200A3AB8F5F893096B (parity zeroed, so only the ME is used), ref -43.5, 172.5.
func TestCPRSurfaceLocal_SouthernHemisphere(t *testing.T) {
	lat, lon := CPRSurfaceLocal(CPRFrame{Odd: true, Lat: 64585, Lon: 67947}, -43.5, 172.5)
	if !near(lat, -43.48564, 1e-5) || !near(lon, 172.53942, 1e-5) {
		t.Errorf("got %.5f, %.5f, want -43.48564, 172.53942", lat, lon)
	}
}

func TestCPRSurface_RoundTrip(t *testing.T) {
	for _, p := range [][2]float64{{25.07, 22.87}, {52.3105, 4.7683}, {-33.94, 151.17}, {64.13, -21.94}, {0.01, -0.01}, {1.3644, 103.9915}} {
		for _, odd := range []bool{false, true} {
			// reference up to ~40 NM away in each direction
			for _, d := range [][2]float64{{0, 0}, {0.6, 0}, {-0.6, 0}, {0, 0.6}, {0, -0.6}} {
				lat, lon := CPRSurfaceLocal(EncodeSurfaceCPR(p[0], p[1], odd), p[0]+d[0], p[1]+d[1])
				if lon > 180 {
					lon -= 360
				}
				if !near(lat, p[0], 1e-4) || !near(lon, p[1], 1e-4) {
					t.Errorf("%v odd=%v ref%+v: got %.5f, %.5f", p, odd, d, lat, lon)
				}
			}
		}
	}
}

func TestMovement(t *testing.T) {
	tests := []struct {
		code int
		want float64 // NaN = nil
	}{
		{0, math.NaN()}, {1, 0}, {2, 0.125}, {8, 0.875}, {9, 1}, {12, 1.75}, {13, 2}, {38, 14.5}, {39, 15},
		{43, 19}, {93, 69}, {94, 70}, {108, 98}, {109, 100}, {123, 170}, {124, 175}, {125, math.NaN()}, {127, math.NaN()},
	}
	for _, tt := range tests {
		got := Movement(tt.code)
		if math.IsNaN(tt.want) {
			if got != nil {
				t.Errorf("Movement(%d) = %v, want nil", tt.code, *got)
			}
			continue
		}
		if got == nil || !near(*got, tt.want, 1e-9) {
			t.Errorf("Movement(%d) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestDecode_SurfaceTrackInvalid(t *testing.T) {
	f := EncodeSurfaceCPR(25.07, 22.87, false)
	m, err := Decode(makeES(0x738062, me([2]uint64{6, 5}, [2]uint64{43, 7}, [2]uint64{0, 1}, [2]uint64{100, 7},
		[2]uint64{0, 1}, [2]uint64{0, 1}, [2]uint64{uint64(f.Lat), 17}, [2]uint64{uint64(f.Lon), 17})), time.Time{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	p := m.SurfacePosition
	if p == nil || p.Track != nil || p.GroundSpeed == nil || *p.GroundSpeed != 19 || p.CPR != f {
		t.Errorf("got %+v", p)
	}
}
