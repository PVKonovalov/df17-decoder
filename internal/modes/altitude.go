package modes

// Altitude12 decodes the 12-bit ADS-B altitude field (TC 9–18): C1 A1 C2 A2 C4 A4 B1 Q B2 D2 B4 D4.
// Q=1: 25 ft steps; Q=0: Gillham code in 100 ft steps. ok=false when unavailable (all zero) or invalid.
// The 1090 MHz Riddle, "Airborne position" → "Altitude decoding" (https://mode-s.org/decode/content/ads-b/3-airborne-position.html).
func Altitude12(field uint32) (ft int, ok bool) {
	if field == 0 {
		return 0, false
	}
	if field&0x10 != 0 {
		n := (field>>5)<<4 | field&0xF
		return int(n)*25 - 1000, true
	}
	return Gillham(field&0xFC0<<1 | field&0x3F)
}

// Altitude13 decodes the 13-bit AC field of DF0/4/16/20: C1 A1 C2 A2 C4 A4 M B1 Q B2 D2 B4 D4.
// M=1 (metric) is not supported and returns ok=false.
// The 1090 MHz Riddle, "Mode S surveillance replies" (https://mode-s.org/decode/content/mode-s/3-surveillance.html).
func Altitude13(field uint32) (ft int, ok bool) {
	if field == 0 || field&0x40 != 0 {
		return 0, false
	}
	if field&0x10 != 0 {
		n := (field>>7)<<5 | (field>>5&1)<<4 | field&0xF
		return int(n)*25 - 1000, true
	}
	return Gillham(field)
}

// Gillham decodes a 13-bit AC field (M and Q/D1 must be 0) holding a Gillham (Gray) altitude in 100 ft steps.
// The 500 ft part is the Gray code D2 D4 A1 A2 A4 B1 B2 B4; the 100 ft part is C1 C2 C4 restricted to the
// five-state cycle 001, 011, 010, 110, 100, reflected when the 500 ft count is odd.
// The 1090 MHz Riddle, "Mode S surveillance replies" → Gillham code.
func Gillham(ac13 uint32) (ft int, ok bool) {
	b := func(i int) uint32 { return ac13 >> i & 1 }
	if b(4) != 0 || b(6) != 0 {
		return 0, false
	}

	gray500 := b(2)<<7 | b(0)<<6 | b(11)<<5 | b(9)<<4 | b(7)<<3 | b(5)<<2 | b(3)<<1 | b(1)
	n500 := int(grayToBinary(gray500))

	var n100 int
	switch b(12)<<2 | b(10)<<1 | b(8) {
	case 0b001:
		n100 = 1
	case 0b011:
		n100 = 2
	case 0b010:
		n100 = 3
	case 0b110:
		n100 = 4
	case 0b100:
		n100 = 5
	default:
		return 0, false
	}
	if n500%2 == 1 {
		n100 = 6 - n100
	}
	return n500*500 + n100*100 - 1300, true
}

func grayToBinary(g uint32) uint32 {
	for s := g >> 1; s != 0; s >>= 1 {
		g ^= s
	}
	return g
}
