package modes

import "fmt"

// Emergency is the emergency/priority state of TC 28 subtype 1.
type Emergency int

const (
	EmergencyNone Emergency = iota
	EmergencyGeneral
	EmergencyMedical
	EmergencyMinimumFuel
	EmergencyNoComms
	EmergencyUnlawfulInterference
	EmergencyDownedAircraft
	EmergencyReserved
)

func (e Emergency) String() string {
	switch e {
	case EmergencyNone:
		return "none"
	case EmergencyGeneral:
		return "general"
	case EmergencyMedical:
		return "lifeguard"
	case EmergencyMinimumFuel:
		return "minfuel"
	case EmergencyNoComms:
		return "nordo"
	case EmergencyUnlawfulInterference:
		return "unlawful"
	case EmergencyDownedAircraft:
		return "downed"
	}
	return "reserved"
}

// SquawkEmergency maps the emergency squawks 7500/7600/7700 to an Emergency; ok=false for other codes.
func SquawkEmergency(squawk string) (Emergency, bool) {
	switch squawk {
	case "7500":
		return EmergencyUnlawfulInterference, true
	case "7600":
		return EmergencyNoComms, true
	case "7700":
		return EmergencyGeneral, true
	}
	return EmergencyNone, false
}

// AircraftStatus is the content of TC 28 subtype 1 (emergency/priority status).
type AircraftStatus struct {
	Emergency Emergency
	Squawk    string // 4 octal digits; "" when the Mode A code field is 0
}

// OperationalStatus is the content of TC 31.
type OperationalStatus struct {
	Version int  // ADS-B version: 0 (DO-260), 1 (DO-260A), 2 (DO-260B)
	Surface bool // subtype 1 (surface) rather than 0 (airborne)
}

// decodeAircraftStatus decodes TC 28. ME: ST 6–8, emergency state 9–11, Mode A code 12–24 (13-bit ID field).
// Subtype 2 (ACAS RA broadcast) is not decoded.
// Not covered by The 1090 MHz Riddle; layout per DO-260B (aircraft status, emergency/priority subtype),
// the same fields dump1090 decodes.
func decodeAircraftStatus(msg []byte) (AircraftStatus, error) {
	if st := bits(msg, 38, 3); st != 1 {
		return AircraftStatus{}, fmt.Errorf("%w: TC28 subtype %d", ErrUnsupportedTC, st)
	}
	s := AircraftStatus{Emergency: Emergency(bits(msg, 41, 3))}
	if id := uint32(bits(msg, 44, 13)); id != 0 {
		s.Squawk = Squawk(id)
	}
	return s, nil
}

// decodeOperationalStatus decodes TC 31. ME: ST 6–8 (0 airborne, 1 surface), version 41–43.
// The 1090 MHz Riddle, "Operation Status" (https://mode-s.org/1090mhz/content/ads-b/6-operation-status.html).
func decodeOperationalStatus(msg []byte) (OperationalStatus, error) {
	st := bits(msg, 38, 3)
	if st > 1 {
		return OperationalStatus{}, fmt.Errorf("%w: TC31 subtype %d", ErrUnsupportedTC, st)
	}
	return OperationalStatus{Version: int(bits(msg, 73, 3)), Surface: st == 1}, nil
}

// Squawk decodes a 13-bit identity field C1 A1 C2 A2 C4 A4 X B1 D1 B2 D2 B4 D4 into 4 octal digits ABCD,
// each digit being (x4 x2 x1).
// The 1090 MHz Riddle, "Surveillance reply" → identity code (https://mode-s.org/1090mhz/content/mode-s/3-surveillance.html).
func Squawk(id13 uint32) string {
	b := func(i int) uint32 { return id13 >> i & 1 }
	a := b(7)<<2 | b(9)<<1 | b(11)
	bb := b(1)<<2 | b(3)<<1 | b(5)
	c := b(8)<<2 | b(10)<<1 | b(12)
	d := b(0)<<2 | b(2)<<1 | b(4)
	return fmt.Sprintf("%d%d%d%d", a, bb, c, d)
}
