package modes

import (
	"errors"
	"testing"
	"time"
)

// makeAP builds a short (DF<16) or long frame with the given first 32 bits and an address/parity field
// overlaid with icao, so that Remainder(frame) == icao.
func makeAP(head uint32, mb uint64, long bool, icao uint32) []byte {
	n := ShortLen
	if long {
		n = LongLen
	}
	msg := make([]byte, n)
	msg[0], msg[1], msg[2], msg[3] = byte(head>>24), byte(head>>16), byte(head>>8), byte(head)
	if long {
		for i := range 7 {
			msg[4+i] = byte(mb >> (48 - 8*i))
		}
	}
	ap := Remainder(msg) ^ icao
	msg[n-3], msg[n-2], msg[n-1] = byte(ap>>16), byte(ap>>8), byte(ap)
	return msg
}

func TestDecode_AllCallVector(t *testing.T) {
	m, err := Decode(mustHex(t, "5D484FDEA248F5"), time.Time{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if m.DF != 11 || m.ICAO != 0x484FDE || m.AllCall == nil || m.AllCall.Capability != 5 || m.AllCall.IID != 22 {
		t.Errorf("got DF=%d ICAO=%06X %+v, want DF11 484FDE CA5 IID 22", m.DF, m.ICAO, m.AllCall)
	}
	if m.AddressParity || m.OnGround == nil || *m.OnGround {
		t.Errorf("AddressParity=%v OnGround=%v, want false, airborne", m.AddressParity, m.OnGround)
	}
}

func TestDecode_AllCallRemainderLimit(t *testing.T) {
	head := uint32(11<<27 | 5<<24 | 0x738062)
	if _, err := Decode(makeAP(head, 0, false, 0x7F), time.Time{}, Options{}); err != nil {
		t.Errorf("IID 0x7F: %v", err)
	}
	if _, err := Decode(makeAP(head, 0, false, 0x80), time.Time{}, Options{}); !errors.Is(err, ErrCRC) {
		t.Errorf("remainder 0x80: err = %v, want ErrCRC", err)
	}
}

func TestDecode_SurveillanceVectors(t *testing.T) {
	tests := []struct {
		name     string
		frame    string
		df       int
		altitude int
		squawk   string
		bds20    string
		icao     uint32 // 0 = not asserted
	}{
		{name: "DF4 altitude", frame: "2000171806A983", df: 4, altitude: 36000},
		{name: "DF5 identity", frame: "2A00516D492B80", df: 5, squawk: "0356"},
		{name: "DF20 altitude", frame: "A02014B400000000000000F9D514", df: 20, altitude: 32300},
		{name: "DF21 identity", frame: "A800292DFFBBA9383FFCEB903D01", df: 21, squawk: "1346"},
		{name: "DF20 BDS 2,0", frame: "A000083E202CC371C31DE0AA1CCF", df: 20, bds20: "KLM1017"},
		{name: "DF20 ICAO from parity", frame: "A0001839CA3800315800007448D9", df: 20, icao: 0x400940},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Decode(mustHex(t, tt.frame), time.Time{}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			s := m.Surveillance
			if m.DF != tt.df || !m.AddressParity || s == nil {
				t.Fatalf("DF=%d AP=%v surveillance=%+v", m.DF, m.AddressParity, s)
			}
			if tt.altitude != 0 && !ptrEq(s.Altitude, tt.altitude) {
				t.Errorf("altitude = %v, want %d", s.Altitude, tt.altitude)
			}
			if s.Squawk != tt.squawk {
				t.Errorf("squawk = %q, want %q", s.Squawk, tt.squawk)
			}
			if s.BDS20 != tt.bds20 {
				t.Errorf("BDS20 = %q, want %q", s.BDS20, tt.bds20)
			}
			if tt.icao != 0 && m.ICAO != tt.icao {
				t.Errorf("ICAO = %06X, want %06X", m.ICAO, tt.icao)
			}
		})
	}
}

func TestDecode_SurveillanceSynthetic(t *testing.T) {
	const icao = 0x738062
	ac := uint32(0b110000_0_1_1_1000) // Q=1, 38000 ft
	id := uint32(0b0101010101010)     // 7700
	tests := []struct {
		name     string
		head     uint32
		long     bool
		mb       uint64
		altitude *int
		squawk   string
		ground   *bool
		alert    bool
		spi      bool
		bds20    string
	}{
		{name: "DF0 airborne", head: 0<<27 | ac, altitude: new(38000), ground: new(false)},
		{name: "DF0 on ground (VS)", head: 0<<27 | 1<<26 | ac, altitude: new(38000), ground: new(true)},
		{name: "DF16 altitude", head: 16<<27 | ac, long: true, altitude: new(38000), ground: new(false)},
		{name: "DF4 FS=1 on ground", head: 4<<27 | 1<<24 | ac, altitude: new(38000), ground: new(true)},
		{name: "DF4 FS=2 alert airborne", head: 4<<27 | 2<<24 | ac, altitude: new(38000), ground: new(false), alert: true},
		{name: "DF4 FS=4 alert SPI", head: 4<<27 | 4<<24 | ac, altitude: new(38000), alert: true, spi: true},
		{name: "DF5 FS=5 SPI 7700", head: 5<<27 | 5<<24 | id, squawk: "7700", spi: true},
		{name: "DF5 no squawk", head: 5 << 27, ground: new(false)},
		{name: "DF4 no altitude", head: 4 << 27, ground: new(false)},
		{name: "DF21 BDS 2,0", head: 21<<27 | id, long: true, mb: 0x202CC371C31DE0, squawk: "7700", ground: new(false), bds20: "KLM1017"},
		{name: "DF20 MB 0x20 with bad chars", head: 20<<27 | ac, long: true, mb: 0x20000000000000, altitude: new(38000), ground: new(false)},
		{name: "DF20 other BDS", head: 20<<27 | ac, long: true, mb: 0x10000000000000, altitude: new(38000), ground: new(false)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Decode(makeAP(tt.head, tt.mb, tt.long, icao), time.Time{}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			s := m.Surveillance
			if m.ICAO != icao {
				t.Errorf("ICAO = %06X", m.ICAO)
			}
			if (tt.altitude == nil) != (s.Altitude == nil) || tt.altitude != nil && *tt.altitude != *s.Altitude {
				t.Errorf("altitude = %v, want %v", s.Altitude, tt.altitude)
			}
			if s.Squawk != tt.squawk || s.BDS20 != tt.bds20 || s.Alert != tt.alert || s.SPI != tt.spi {
				t.Errorf("got %+v", *s)
			}
			if (tt.ground == nil) != (m.OnGround == nil) || tt.ground != nil && *tt.ground != *m.OnGround {
				t.Errorf("onGround = %v, want %v", m.OnGround, tt.ground)
			}
		})
	}
}

func TestDecode_SurveillanceInvalidFS(t *testing.T) {
	if _, err := Decode(makeAP(4<<27|6<<24, 0, false, 1), time.Time{}, Options{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("FS=6: err = %v, want ErrInvalid", err)
	}
}

func TestDecode_ExtendedSquitterOnGroundFromCA(t *testing.T) {
	m, err := Decode(mustHex(t, "8D4840D6202CC371C32CE0576098"), time.Time{}, Options{}) // CA=5
	if err != nil {
		t.Fatal(err)
	}
	if m.OnGround == nil || *m.OnGround {
		t.Errorf("OnGround = %v, want airborne", m.OnGround)
	}
}
