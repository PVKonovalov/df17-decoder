package modes

import (
	"errors"
	"testing"
	"time"
)

// makeES builds a DF17 frame (CA=5) for icao with the 56-bit ME field me and a valid parity.
func makeES(icao uint32, me uint64) []byte {
	msg := make([]byte, LongLen)
	msg[0] = 17<<3 | 5
	msg[1], msg[2], msg[3] = byte(icao>>16), byte(icao>>8), byte(icao)
	for i := range 7 {
		msg[4+i] = byte(me >> (48 - 8*i))
	}
	crc := Remainder(msg) // parity bytes are zero, so this is the CRC of the data
	msg[11], msg[12], msg[13] = byte(crc>>16), byte(crc>>8), byte(crc)
	return msg
}

// me assembles an ME field from (value, width) pairs, MSB first, padding to 56 bits.
func me(fields ...[2]uint64) uint64 {
	var v uint64
	n := 0
	for _, f := range fields {
		v = v<<f[1] | f[0]
		n += int(f[1])
	}
	return v << (56 - n)
}

func TestMakeES_ValidCRC(t *testing.T) {
	if r := Remainder(makeES(0x738062, me([2]uint64{31, 5}))); r != 0 {
		t.Fatalf("remainder = %06X", r)
	}
}

func ptrEq[T comparable](p *T, want T) bool { return p != nil && *p == want }

func TestDecode_VelocityVectors(t *testing.T) {
	m, err := Decode(mustHex(t, "8D485020994409940838175B284F"), time.Time{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	v := m.Velocity
	if v == nil || v.Subtype != 1 || v.GroundSpeed == nil || v.Track == nil {
		t.Fatalf("velocity = %+v", v)
	}
	if !near(*v.GroundSpeed, 159.20, 0.01) || !near(*v.Track, 182.88, 0.01) {
		t.Errorf("gs=%.2f trk=%.2f, want 159.20 182.88", *v.GroundSpeed, *v.Track)
	}
	if !ptrEq(v.VerticalRate, -832) || v.Heading != nil || v.IAS != nil || v.TAS != nil {
		t.Errorf("vr=%v hdg=%v ias=%v tas=%v", v.VerticalRate, v.Heading, v.IAS, v.TAS)
	}
	if !ptrEq(v.GNSSBaroDiff, 550) {
		t.Errorf("GNSSBaroDiff = %v, want 550", v.GNSSBaroDiff)
	}

	m, err = Decode(mustHex(t, "8DA05F219B06B6AF189400CBC33F"), time.Time{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	v = m.Velocity
	if v == nil || v.Subtype != 3 || v.Heading == nil {
		t.Fatalf("velocity = %+v", v)
	}
	if !near(*v.Heading, 243.98, 0.01) || !ptrEq(v.TAS, 375) || v.IAS != nil || !ptrEq(v.VerticalRate, -2304) {
		t.Errorf("hdg=%.2f tas=%v ias=%v vr=%v", *v.Heading, v.TAS, v.IAS, v.VerticalRate)
	}
	if v.GroundSpeed != nil || v.GNSSBaroDiff != nil {
		t.Errorf("unexpected gs=%v diff=%v", v.GroundSpeed, v.GNSSBaroDiff)
	}
}

func TestDecode_VelocitySynthetic(t *testing.T) {
	// fields: TC ST IC IFR NUC | S-EW V-EW S-NS V-NS | VrSrc S-Vr Vr | res S-Dif dAlt
	vel := func(st, sEW, vEW, sNS, vNS, src, sVr, vr, sDif, dAlt uint64) uint64 {
		return me([2]uint64{19, 5}, [2]uint64{st, 3}, [2]uint64{0, 5},
			[2]uint64{sEW, 1}, [2]uint64{vEW, 10}, [2]uint64{sNS, 1}, [2]uint64{vNS, 10},
			[2]uint64{src, 1}, [2]uint64{sVr, 1}, [2]uint64{vr, 9}, [2]uint64{0, 2}, [2]uint64{sDif, 1}, [2]uint64{dAlt, 7})
	}
	tests := []struct {
		name     string
		me       uint64
		gs, trk  float64
		ias, tas *int
		hdg      float64
		vr       *int
		baro     bool
	}{
		{name: "due east 300 kt, climbing baro", me: vel(1, 0, 301, 0, 1, 1, 0, 11, 0, 0), gs: 300, trk: 90, vr: new(640), baro: true},
		{name: "due west", me: vel(1, 1, 101, 0, 1, 0, 0, 0, 0, 0), gs: 100, trk: 270},
		{name: "supersonic north", me: vel(2, 0, 1, 0, 401, 0, 0, 0, 0, 0), gs: 1600, trk: 0},
		{name: "IAS 250 kt heading 90", me: vel(3, 1, 256, 0, 251, 0, 1, 2, 0, 0), ias: new(250), hdg: 90, vr: new(-64)},
		{name: "supersonic TAS", me: vel(4, 0, 0, 1, 301, 0, 0, 0, 0, 0), tas: new(1200), hdg: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Decode(makeES(0x738062, tt.me), time.Time{}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			v := m.Velocity
			if tt.gs != 0 && (v.GroundSpeed == nil || !near(*v.GroundSpeed, tt.gs, 1e-9) || !near(*v.Track, tt.trk, 1e-9)) {
				t.Errorf("gs/trk = %v/%v, want %v/%v", v.GroundSpeed, v.Track, tt.gs, tt.trk)
			}
			if (tt.ias == nil) != (v.IAS == nil) || tt.ias != nil && *tt.ias != *v.IAS {
				t.Errorf("ias = %v, want %v", v.IAS, tt.ias)
			}
			if (tt.tas == nil) != (v.TAS == nil) || tt.tas != nil && *tt.tas != *v.TAS {
				t.Errorf("tas = %v, want %v", v.TAS, tt.tas)
			}
			switch {
			case tt.hdg > 0 && (v.Heading == nil || !near(*v.Heading, tt.hdg, 0.36)):
				t.Errorf("heading = %v, want %v", v.Heading, tt.hdg)
			case tt.hdg < 0 && v.Heading != nil:
				t.Errorf("heading = %v, want unavailable (SH=0)", *v.Heading)
			}
			if (tt.vr == nil) != (v.VerticalRate == nil) || tt.vr != nil && *tt.vr != *v.VerticalRate {
				t.Errorf("vr = %v, want %v", v.VerticalRate, tt.vr)
			}
			if v.VerticalRateBaro != tt.baro {
				t.Errorf("baro = %v, want %v", v.VerticalRateBaro, tt.baro)
			}
		})
	}
}

func TestDecode_VelocityUnavailableComponent(t *testing.T) {
	m, err := Decode(makeES(1, me([2]uint64{19, 5}, [2]uint64{1, 3}, [2]uint64{0, 5}, [2]uint64{0, 1}, [2]uint64{0, 10}, [2]uint64{0, 1}, [2]uint64{100, 10})), time.Time{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Velocity.GroundSpeed != nil || m.Velocity.Track != nil {
		t.Errorf("gs/trk must be nil when V-EW is unavailable: %v %v", *m.Velocity.GroundSpeed, *m.Velocity.Track)
	}
}

func TestSquawk(t *testing.T) {
	tests := []struct {
		id13 uint32
		want string
	}{
		{0b0100100101101, "1346"}, // ID field of DF21 A800292DFFBBA9383FFCEB903D01
		{0b0101010101010, "7700"}, // A1 A2 A4 B1 B2 B4
		{0b0101010001010, "7600"}, // A1 A2 A4 B2 B4
		{0b0101010100010, "7500"}, // A1 A2 A4 B1 B4
		{0b1000000000000, "0010"}, // C1
		{0b0000000010000, "0001"}, // D1
		{0b0000000000001, "0004"}, // D4
		{0b1111111111111 &^ 0b0000001000000, "7777"},
	}
	for _, tt := range tests {
		if got := Squawk(tt.id13); got != tt.want {
			t.Errorf("Squawk(%013b) = %s, want %s", tt.id13, got, tt.want)
		}
	}
}

func TestDecode_AircraftStatus(t *testing.T) {
	tests := []struct {
		name      string
		emergency Emergency
		id13      uint64
		squawk    string
	}{
		{"no emergency", EmergencyNone, 0b0100100101101, "1346"},
		{"hijack", EmergencyUnlawfulInterference, 0b0101010100010, "7500"},
		{"no squawk", EmergencyMedical, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Decode(makeES(0x738062, me([2]uint64{28, 5}, [2]uint64{1, 3}, [2]uint64{uint64(tt.emergency), 3}, [2]uint64{tt.id13, 13})), time.Time{}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			s := m.AircraftStatus
			if s == nil || s.Emergency != tt.emergency || s.Squawk != tt.squawk {
				t.Errorf("got %+v, want %v %q", s, tt.emergency, tt.squawk)
			}
		})
	}

	if _, err := Decode(makeES(1, me([2]uint64{28, 5}, [2]uint64{2, 3})), time.Time{}, Options{}); !errors.Is(err, ErrUnsupportedTC) {
		t.Errorf("TC28 ST2: err = %v, want ErrUnsupportedTC", err)
	}
}

func TestSquawkEmergency(t *testing.T) {
	for sq, want := range map[string]Emergency{"7500": EmergencyUnlawfulInterference, "7600": EmergencyNoComms, "7700": EmergencyGeneral} {
		if e, ok := SquawkEmergency(sq); !ok || e != want {
			t.Errorf("%s: %v %v", sq, e, ok)
		}
	}
	if _, ok := SquawkEmergency("1200"); ok {
		t.Error("1200 is not an emergency")
	}
}

func TestDecode_OperationalStatus(t *testing.T) {
	for _, tt := range []struct {
		st, ver uint64
		surface bool
	}{{0, 0, false}, {0, 1, false}, {0, 2, false}, {1, 2, true}} {
		// TC ST | 32 bits capability/mode | ver(3)
		m, err := Decode(makeES(0x738062, me([2]uint64{31, 5}, [2]uint64{tt.st, 3}, [2]uint64{0, 32}, [2]uint64{tt.ver, 3})), time.Time{}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if s := m.OperationalStatus; s == nil || s.Version != int(tt.ver) || s.Surface != tt.surface {
			t.Errorf("st=%d ver=%d: got %+v", tt.st, tt.ver, s)
		}
	}
}

func TestDecode_UnsupportedTypeCodes(t *testing.T) {
	for _, tc := range []uint64{0, 23, 24, 27, 29, 30} {
		if _, err := Decode(makeES(1, me([2]uint64{tc, 5})), time.Time{}, Options{}); !errors.Is(err, ErrUnsupportedTC) {
			t.Errorf("TC %d: err = %v, want ErrUnsupportedTC", tc, err)
		}
	}
	if _, err := Decode(makeES(1, me([2]uint64{19, 5}, [2]uint64{5, 3})), time.Time{}, Options{}); !errors.Is(err, ErrUnsupportedTC) {
		t.Errorf("TC19 ST5: err = %v", err)
	}
}
