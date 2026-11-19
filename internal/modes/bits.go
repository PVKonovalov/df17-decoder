package modes

// bits returns length (≤64) bits of msg starting at the 1-based bit position start, MSB first,
// matching the bit numbering used in The 1090 MHz Riddle. The caller guarantees the range is inside msg.
func bits(msg []byte, start, length int) uint64 {
	var v uint64
	for i := start - 1; i < start-1+length; i++ {
		v = v<<1 | uint64(msg[i/8]>>(7-i%8)&1)
	}
	return v
}
