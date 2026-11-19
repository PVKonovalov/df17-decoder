package modes

import (
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var validFrames = []string{
	"8D4840D6202CC371C32CE0576098",
	"8D40621D58C382D690C8AC2863A7",
	"8D40621D58C386435CC412692AD6",
	"8D485020994409940838175B284F",
	"8DA05F219B06B6AF189400CBC33F",
}

func TestBits(t *testing.T) {
	msg := mustHex(t, "8D4840D6202CC371C32CE0576098")
	tests := []struct {
		name          string
		start, length int
		want          uint64
	}{
		{"DF", 1, 5, 17},
		{"CA", 6, 3, 5},
		{"ICAO", 9, 24, 0x4840D6},
		{"TC", 33, 5, 4},
		{"first byte", 1, 8, 0x8D},
		{"last bit", 112, 1, 0},
		{"parity", 89, 24, 0x576098},
		{"cross byte", 7, 4, 0b0101},
		{"full 64", 1, 64, 0x8D4840D6202CC371},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bits(msg, tt.start, tt.length); got != tt.want {
				t.Errorf("bits(%d,%d) = %#x, want %#x", tt.start, tt.length, got, tt.want)
			}
		})
	}
}

func TestRemainder_ValidFramesAreZero(t *testing.T) {
	for _, s := range validFrames {
		if r := Remainder(mustHex(t, s)); r != 0 {
			t.Errorf("%s: remainder = %06X, want 0", s, r)
		}
	}
}

func TestRemainder_AnySingleBitFlipIsDetected(t *testing.T) {
	for _, s := range validFrames {
		msg := mustHex(t, s)
		for i := range len(msg) * 8 {
			flipped := append([]byte(nil), msg...)
			flipped[i/8] ^= 1 << (7 - i%8)
			if Remainder(flipped) == 0 {
				t.Errorf("%s: flipping bit %d not detected", s, i+1)
			}
		}
	}
}

func TestDecode_Identification(t *testing.T) {
	tests := []struct {
		frame    string
		icao     uint32
		tc       int
		callsign string
		category string
	}{
		{"8D4840D6202CC371C32CE0576098", 0x4840D6, 4, "KLM1023", "A0"},
	}
	for _, tt := range tests {
		t.Run(tt.frame, func(t *testing.T) {
			m, err := Decode(mustHex(t, tt.frame), time.Time{}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if m.DF != 17 || m.ICAO != tt.icao || m.TC != tt.tc {
				t.Errorf("DF=%d ICAO=%06X TC=%d, want 17 %06X %d", m.DF, m.ICAO, m.TC, tt.icao, tt.tc)
			}
			if m.Identification == nil {
				t.Fatal("Identification is nil")
			}
			if m.Identification.Callsign != tt.callsign || m.Identification.Category != tt.category {
				t.Errorf("got %+v, want callsign %q category %q", *m.Identification, tt.callsign, tt.category)
			}
		})
	}
}

func TestDecode_SingleBitFix(t *testing.T) {
	msg := mustHex(t, "8D4840D6202CC371C32CE0576098")
	msg[6] ^= 0x10 // bit 52

	if _, err := Decode(msg, time.Time{}, Options{}); !errors.Is(err, ErrCRC) {
		t.Fatalf("without fix: err = %v, want ErrCRC", err)
	}

	m, err := Decode(msg, time.Time{}, Options{FixSingleBitErrors: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.FixedBit != 52 || m.Identification == nil || m.Identification.Callsign != "KLM1023" {
		t.Errorf("FixedBit=%d ident=%+v, want 52 KLM1023", m.FixedBit, m.Identification)
	}
}

func TestDecode_SingleBitFixNotForDF18(t *testing.T) {
	msg := mustHex(t, "8D4840D6202CC371C32CE0576098")
	msg[0] = 0x90 // DF18 CF0; invalidates CRC
	if _, err := Decode(msg, time.Time{}, Options{FixSingleBitErrors: true}); !errors.Is(err, ErrCRC) {
		t.Errorf("err = %v, want ErrCRC", err)
	}
}

func TestDecode_Errors(t *testing.T) {
	tests := []struct {
		name  string
		frame string
		want  error
	}{
		{"too short", "8D4840", ErrLength},
		{"long DF with short frame", "8D4840D6202CC3", ErrLength},
		{"short DF with long frame", "5D4840D6202CC371C32CE0576098", ErrLength},
		{"DF11 bad parity", "5D4840D6202CC3", ErrCRC},
		{"DF24 unsupported", "C04840D6202CC371C32CE0576098", ErrUnsupported},
		{"bad crc", "8D4840D6202CC371C32CE0576099", ErrCRC},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode(mustHex(t, tt.frame), time.Time{}, Options{}); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDF(t *testing.T) {
	tests := []struct {
		first byte
		want  int
	}{
		{0x00, 0}, {0x20, 4}, {0x28, 5}, {0x58, 11}, {0x8D, 17}, {0x90, 18}, {0xA0, 20}, {0xA8, 21}, {0xC0, 24}, {0xFF, 24},
	}
	for _, tt := range tests {
		if got := DF([]byte{tt.first}); got != tt.want {
			t.Errorf("DF(%#x) = %d, want %d", tt.first, got, tt.want)
		}
	}
}
