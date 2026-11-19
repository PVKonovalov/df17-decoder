package modes

import (
	"testing"
	"time"
)

// Enhanced Mode S vectors from The 1090 MHz Riddle / pyModeS.
const (
	vecBDS40 = "A000029C85E42F313000007047D3"
	vecBDS50 = "A000139381951536E024D4CCF6B5"
	vecBDS60 = "A00004128F39F91A7E27C46ADC21"
)

func decodeCommB(t *testing.T, frame string) *CommB {
	t.Helper()
	m, err := Decode(mustHex(t, frame), time.Time{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Surveillance == nil {
		t.Fatalf("%s: no surveillance", frame)
	}
	return m.Surveillance.CommB
}

func TestCommB_Vectors(t *testing.T) {
	c := decodeCommB(t, vecBDS40)
	if c == nil || c.BDS40 == nil || c.BDS50 != nil || c.BDS60 != nil || c.Ambiguous {
		t.Fatalf("4,0: %+v", c)
	}
	if b := c.BDS40; *b.MCPAltitude != 3008 || *b.FMSAltitude != 3008 || *b.BaroSetting != 1020 {
		t.Errorf("4,0: mcp=%v fms=%v baro=%v", *b.MCPAltitude, *b.FMSAltitude, *b.BaroSetting)
	}

	c = decodeCommB(t, vecBDS50)
	if c == nil || c.BDS50 == nil || c.BDS40 != nil || c.BDS60 != nil {
		t.Fatalf("5,0: %+v", c)
	}
	if b := c.BDS50; !near(*b.Roll, 2.1, 0.01) || !near(*b.Track, 114.258, 1e-3) || *b.GroundSpeed != 438 || *b.TrackRate != 0.125 || *b.TAS != 424 {
		t.Errorf("5,0: roll=%v trk=%v gs=%v rate=%v tas=%v", *b.Roll, *b.Track, *b.GroundSpeed, *b.TrackRate, *b.TAS)
	}

	c = decodeCommB(t, vecBDS60)
	if c == nil || c.BDS60 == nil || c.BDS40 != nil || c.BDS50 != nil {
		t.Fatalf("6,0: %+v", c)
	}
	if b := c.BDS60; !near(*b.Heading, 42.715, 1e-3) || *b.IAS != 252 || !near(*b.Mach, 0.42, 1e-9) || *b.VerticalRateBaro != -1920 || *b.VerticalRateINS != -1920 {
		t.Errorf("6,0: hdg=%v ias=%v mach=%v vr=%v ins=%v", *b.Heading, *b.IAS, *b.Mach, *b.VerticalRateBaro, *b.VerticalRateINS)
	}
}

// commB builds a DF20 reply (FS 0, altitude field 0) with the given MB field for icao.
func commB(icao uint32, mbField uint64) []byte {
	return makeAP(20<<27, mbField, true, icao)
}

func TestCommB_Inference(t *testing.T) {
	tests := []struct {
		name string
		mb   uint64
		want string // "", "40", "50", "60" or "ambiguous"
	}{
		{"empty", 0, ""},
		{"BDS 1,0 data link capability", me([2]uint64{0x10, 8}, [2]uint64{0x1F, 16}), ""},
		{"status 0 with value bits", me([2]uint64{0, 1}, [2]uint64{5, 12}), ""},
		// TAS (5,0 bits 46–56) and inertial vertical rate (6,0 bits 46–56) share their bits
		{"fits 5,0 and 6,0", me([2]uint64{0, 45}, [2]uint64{1, 1}, [2]uint64{0, 1}, [2]uint64{5, 9}), "ambiguous"},
		// roll 10° right, track 90°, GS 300 kt, TAS 310 kt
		{"5,0 only", me([2]uint64{1, 1}, [2]uint64{0, 1}, [2]uint64{57, 9}, [2]uint64{1, 1}, [2]uint64{0, 1}, [2]uint64{512, 10},
			[2]uint64{1, 1}, [2]uint64{150, 10}, [2]uint64{0, 11}, [2]uint64{1, 1}, [2]uint64{155, 10}), "50"},
		// heading status + heading, IAS 250 kt, Mach 0.6
		{"6,0 only", me([2]uint64{1, 1}, [2]uint64{0, 1}, [2]uint64{256, 10}, [2]uint64{1, 1}, [2]uint64{250, 10},
			[2]uint64{1, 1}, [2]uint64{150, 10}), "60"},
		// MCP 35008 ft, baro 1013.2 hPa
		{"4,0 only", me([2]uint64{1, 1}, [2]uint64{2188, 12}, [2]uint64{0, 13}, [2]uint64{1, 1}, [2]uint64{2132, 12}), "40"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Decode(commB(0x738ABC, tt.mb), time.Time{}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			c := m.Surveillance.CommB
			got := ""
			switch {
			case c == nil:
			case c.Ambiguous:
				got = "ambiguous"
			case c.BDS40 != nil:
				got = "40"
			case c.BDS50 != nil:
				got = "50"
			case c.BDS60 != nil:
				got = "60"
			}
			if got != tt.want {
				t.Errorf("got %q (%+v), want %q", got, c, tt.want)
			}
		})
	}
}

func TestCommB_NotTriedOnBDS20(t *testing.T) {
	m, err := Decode(commB(0x738ABC, me([2]uint64{0x20, 8}, [2]uint64{0x4B1CC1, 24}, [2]uint64{0x820820, 24})), time.Time{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s := m.Surveillance; s.BDS20 == "" || s.CommB != nil {
		t.Errorf("BDS20=%q CommB=%+v", s.BDS20, s.CommB)
	}
}

func TestCommB_ImplausibleValues(t *testing.T) {
	frame := func(fields ...[2]uint64) []byte { return commB(0x738ABC, me(fields...)) }
	if b := decodeBDS50(frame([2]uint64{1, 1}, [2]uint64{0, 1}, [2]uint64{341, 9})); b != nil {
		t.Errorf("5,0 roll 60°: %+v", b)
	}
	if b := decodeBDS50(frame([2]uint64{0, 23}, [2]uint64{1, 1}, [2]uint64{50, 10}, [2]uint64{0, 11}, [2]uint64{1, 1}, [2]uint64{200, 10})); b != nil {
		t.Errorf("5,0 GS 100 kt, TAS 400 kt: %+v", b)
	}
	if b := decodeBDS60(frame([2]uint64{0, 23}, [2]uint64{1, 1}, [2]uint64{300, 10}), nil); b != nil {
		t.Errorf("6,0 Mach 1.2: %+v", b)
	}
	if b := decodeBDS60(frame([2]uint64{0, 12}, [2]uint64{1, 1}, [2]uint64{600, 10}), nil); b != nil {
		t.Errorf("6,0 IAS 600 kt: %+v", b)
	}
	if b := decodeBDS40(frame([2]uint64{1, 1}, [2]uint64{2188, 12}, [2]uint64{0, 26}, [2]uint64{1, 8})); b != nil {
		t.Errorf("4,0 reserved bits set: %+v", b)
	}
	if b := decodeBDS40(frame([2]uint64{0, 26}, [2]uint64{1, 1}, [2]uint64{3500, 12})); b != nil {
		t.Errorf("4,0 baro 1100+ hPa: %+v", b)
	}
}

func TestCASToMach(t *testing.T) {
	for _, tt := range []struct{ cas, alt, want float64 }{{250, 0, 0.378}, {250, 10000, 0.452}, {280, 35000, 0.821}, {120, 3000, 0.19}} {
		if got := CASToMach(tt.cas, tt.alt); !near(got, tt.want, 0.005) {
			t.Errorf("CASToMach(%v kt, %v ft) = %.3f, want %.3f", tt.cas, tt.alt, got, tt.want)
		}
	}
}

func TestCommB_MachMustMatchIAS(t *testing.T) {
	ias250 := func(machCode uint64) []byte {
		return commB(0x738ABC, me([2]uint64{0, 12}, [2]uint64{1, 1}, [2]uint64{250, 10}, [2]uint64{1, 1}, [2]uint64{machCode, 10}))
	}
	if b := decodeBDS60(ias250(113), new(10000)); b == nil { // Mach 0.452
		t.Error("IAS 250 kt, Mach 0.452 at 10000 ft rejected")
	}
	if b := decodeBDS60(ias250(200), new(10000)); b != nil { // Mach 0.8
		t.Errorf("IAS 250 kt, Mach 0.8 at 10000 ft accepted: %+v", b)
	}
	if b := decodeBDS60(ias250(200), nil); b == nil {
		t.Error("without altitude the Mach check must not apply")
	}
}
