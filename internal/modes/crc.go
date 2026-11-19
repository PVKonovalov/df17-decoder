package modes

// generator is the Mode S CRC-24 polynomial without its implicit x^24 term.
const generator = 0xFFF409

// Remainder returns the CRC-24 remainder over the whole frame (data bits divided by the generator,
// XORed with the trailing 24-bit parity field). It is 0 for an intact DF17/18 frame, the interrogator
// code for DF11 and the ICAO address for address/parity replies (DF0/4/5/16/20/21).
//
// The 1090 MHz Riddle, "Error control" (https://mode-s.org/1090mhz/content/ads-b/8-error-control.html).
func Remainder(msg []byte) uint32 {
	n := len(msg)*8 - 24
	var crc uint32
	for i := range n {
		bit := uint32(msg[i/8]>>(7-i%8)) & 1
		top := (crc>>23)&1 ^ bit
		crc = (crc << 1) & 0xFFFFFF
		if top == 1 {
			crc ^= generator
		}
	}
	return crc ^ uint32(bits(msg, n+1, 24))
}

// fixSingleBit looks for one flipped bit (outside the DF field) whose correction makes the remainder 0.
// It returns the repaired copy and the 1-based position of the corrected bit, or ok=false.
func fixSingleBit(msg []byte) (fixed []byte, pos int, ok bool) {
	fixed = make([]byte, len(msg))
	for i := 5; i < len(msg)*8; i++ {
		copy(fixed, msg)
		fixed[i/8] ^= 1 << (7 - i%8)
		if Remainder(fixed) == 0 {
			return fixed, i + 1, true
		}
	}
	return nil, 0, false
}
