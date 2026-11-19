package modes

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

const (
	vecEven = "8D40621D58C382D690C8AC2863A7"
	vecOdd  = "8D40621D58C386435CC412692AD6"
)

func decodePos(t *testing.T, frame string) AirbornePosition {
	t.Helper()
	m, err := Decode(mustHex(t, frame), time.Time{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if m.AirbornePosition == nil {
		t.Fatalf("%s: no airborne position", frame)
	}
	return *m.AirbornePosition
}

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestDecode_AirbornePositionVector(t *testing.T) {
	even, odd := decodePos(t, vecEven), decodePos(t, vecOdd)

	for name, p := range map[string]AirbornePosition{"even": even, "odd": odd} {
		if p.Altitude == nil || *p.Altitude != 38000 || p.GNSS {
			t.Errorf("%s: altitude %v gnss=%v, want 38000 baro", name, p.Altitude, p.GNSS)
		}
	}
	if even.CPR != (CPRFrame{Odd: false, Lat: 93000, Lon: 51372}) {
		t.Errorf("even CPR = %+v", even.CPR)
	}
	if odd.CPR != (CPRFrame{Odd: true, Lat: 74158, Lon: 50194}) {
		t.Errorf("odd CPR = %+v", odd.CPR)
	}
}

func TestCPRGlobal_Vector(t *testing.T) {
	even, odd := decodePos(t, vecEven).CPR, decodePos(t, vecOdd).CPR

	lat, lon, err := CPRGlobal(even, odd, false)
	if err != nil {
		t.Fatal(err)
	}
	if !near(lat, 52.2572, 1e-4) || !near(lon, 3.9194, 1e-4) {
		t.Errorf("even newest: %.5f, %.5f, want 52.2572, 3.9194", lat, lon)
	}

	lat, lon, err = CPRGlobal(even, odd, true)
	if err != nil {
		t.Fatal(err)
	}
	wantLat, wantLon := CPRLocal(odd, 52.2572, 3.9194)
	if !near(lat, wantLat, 1e-9) || !near(lon, wantLon, 1e-9) || !near(lat, 52.2572, 0.02) {
		t.Errorf("odd newest: %.5f, %.5f, want local decode %.5f, %.5f", lat, lon, wantLat, wantLon)
	}
}

func TestCPRLocal_Vector(t *testing.T) {
	even := decodePos(t, vecEven).CPR
	lat, lon := CPRLocal(even, 52.258, 3.918)
	if !near(lat, 52.2572, 1e-4) || !near(lon, 3.9194, 1e-4) {
		t.Errorf("got %.5f, %.5f, want 52.2572, 3.9194", lat, lon)
	}
	// a reference 150 NM away still resolves to the same cell
	lat, lon = CPRLocal(even, 50.0, 5.5)
	if !near(lat, 52.2572, 1e-4) || !near(lon, 3.9194, 1e-4) {
		t.Errorf("far ref: got %.5f, %.5f", lat, lon)
	}
}

func TestCPR_EncodeDecodeRoundTrip(t *testing.T) {
	points := [][2]float64{{25.07, 22.87}, {24.88, 23.21}, {-33.9, 151.2}, {64.1, -21.9}, {0.5, -0.5}, {52.2572, 3.9194}}
	for _, p := range points {
		t.Run(fmt.Sprint(p), func(t *testing.T) {
			even, odd := EncodeCPR(p[0], p[1], false), EncodeCPR(p[0], p[1], true)
			for _, oddNewest := range []bool{false, true} {
				lat, lon, err := CPRGlobal(even, odd, oddNewest)
				if err != nil || !near(lat, p[0], 1e-3) || !near(lon, p[1], 1e-3) {
					t.Errorf("global oddNewest=%v: %.5f, %.5f, %v", oddNewest, lat, lon, err)
				}
			}
			lat, lon := CPRLocal(odd, p[0]+1, p[1]-1)
			if !near(lat, p[0], 1e-3) || !near(lon, p[1], 1e-3) {
				t.Errorf("local: %.5f, %.5f", lat, lon)
			}
		})
	}
}

func TestCPRGlobal_ZoneMismatch(t *testing.T) {
	// even and odd frames from either side of the NL 59/58 boundary (10.4704713°)
	even := EncodeCPR(10.465, 20.0, false)
	odd := EncodeCPR(10.476, 20.0, true)
	if _, _, err := CPRGlobal(even, odd, false); !errors.Is(err, ErrCPRZoneMismatch) {
		t.Errorf("err = %v, want ErrCPRZoneMismatch", err)
	}
}

func TestNL(t *testing.T) {
	tests := []struct {
		lat  float64
		want int
	}{
		{0, 59}, {10.4704712, 59}, {10.4704714, 58}, {14.8281743, 58}, {14.8281745, 57},
		{52.2572, 36}, {-52.2572, 36}, {86.5353699, 3}, {86.5353701, 2},
		{86.9999, 2}, {87, 2}, {87.0001, 1}, {90, 1}, {-90, 1},
	}
	for _, tt := range tests {
		if got := NL(tt.lat); got != tt.want {
			t.Errorf("NL(%v) = %d, want %d", tt.lat, got, tt.want)
		}
	}
}

func TestNL_AllZoneBoundaries(t *testing.T) {
	// transition latitude where NL drops from nl to nl-1, from the inverse of the NL formula
	for nl := 59; nl >= 3; nl-- {
		b := 180 / math.Pi * math.Acos(math.Sqrt((1-math.Cos(math.Pi/(2*nz)))/(1-math.Cos(2*math.Pi/float64(nl)))))
		if NL(b-1e-6) != nl || NL(b+1e-6) != nl-1 {
			t.Errorf("boundary %.8f: NL below=%d above=%d, want %d/%d", b, NL(b-1e-6), NL(b+1e-6), nl, nl-1)
		}
	}
}

// gillhamField builds a 13-bit AC field from bits in the order D2 D4 A1 A2 A4 B1 B2 B4 C1 C2 C4.
func gillhamField(t *testing.T, s string) uint32 {
	t.Helper()
	s = strings.ReplaceAll(s, " ", "")
	if len(s) != 11 {
		t.Fatalf("bad pattern %q", s)
	}
	pos := []int{2, 0, 11, 9, 7, 5, 3, 1, 12, 10, 8} // AC13 bit index of each letter
	var f uint32
	for i, c := range s {
		if c == '1' {
			f |= 1 << pos[i]
		}
	}
	return f
}

func TestGillham(t *testing.T) {
	tests := []struct {
		pattern string // D2 D4 A1 A2 A4 B1 B2 B4 C1 C2 C4
		want    int
	}{
		{"00000000 010", -1000},
		{"00000001 010", -500},
		{"00000011 011", -100},
		{"00000011 010", 0},
		{"00000011 110", 100},
		{"00000010 011", 600},
		{"00000110 010", 1000},
		{"00001001 001", 5800},
		{"00011100 100", 10300},
		{"01100011 010", 32000},
		{"01110000 100", 46300},
		{"01010101 100", 50200},
		{"11011110 100", 73200},
		{"10000000 011", 126600},
		{"10000000 001", 126700},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got, ok := Gillham(gillhamField(t, tt.pattern))
			if !ok || got != tt.want {
				t.Errorf("got %d ok=%v, want %d", got, ok, tt.want)
			}
		})
	}
}

func TestGillham_Invalid(t *testing.T) {
	for _, p := range []string{"00000011 000", "00000011 111", "00000011 101"} {
		if ft, ok := Gillham(gillhamField(t, p)); ok {
			t.Errorf("%s: got %d, want invalid", p, ft)
		}
	}
	if _, ok := Gillham(gillhamField(t, "00000011 010") | 0x10); ok {
		t.Error("D1 set: want invalid")
	}
}

func TestAltitude12(t *testing.T) {
	tests := []struct {
		name  string
		field uint32
		want  int
		ok    bool
	}{
		{"vector 38000", 0xC38, 38000, true},
		{"Q=1 minimum", 0x010, -1000, true},
		{"Q=1 zero ft", 0x058, 0, true},
		{"unavailable", 0x000, 0, false},
		{"Q=0 invalid gillham", 0x004, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Altitude12(tt.field)
			if ok != tt.ok || got != tt.want {
				t.Errorf("Altitude12(%#x) = %d %v, want %d %v", tt.field, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestAltitude12_Gillham(t *testing.T) {
	for _, tt := range []struct {
		pattern string
		want    int
	}{{"00000011 010", 0}, {"01100011 010", 32000}, {"10000000 001", 126700}} {
		ac13 := gillhamField(t, tt.pattern)
		field12 := ac13>>7<<6 | ac13&0x3F // drop the M bit
		if got, ok := Altitude12(field12); !ok || got != tt.want {
			t.Errorf("%s: got %d %v, want %d", tt.pattern, got, ok, tt.want)
		}
	}
}

func TestAltitude13(t *testing.T) {
	tests := []struct {
		name  string
		field uint32
		want  int
		ok    bool
	}{
		// Q=1: N = 11 bits without M and Q; 38000 ft => N=1560=0b11000011000
		{"Q=1 38000", 0b110000_0_1_1_1000, 38000, true},
		{"M=1 metric unsupported", 0x40 | 0x10, 0, false},
		{"unavailable", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Altitude13(tt.field)
			if ok != tt.ok || got != tt.want {
				t.Errorf("Altitude13(%#b) = %d %v, want %d %v", tt.field, got, ok, tt.want, tt.ok)
			}
		})
	}
	if got, ok := Altitude13(gillhamField(t, "11011110 100")); !ok || got != 73200 {
		t.Errorf("gillham via Altitude13 = %d %v, want 73200", got, ok)
	}
}
