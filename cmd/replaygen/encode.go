package main

import (
	"math"
	"strings"

	"github.com/PVKonovalov/df17-decoder/internal/modes"
)

const charset = "#ABCDEFGHIJKLMNOPQRSTUVWXYZ##### ###############0123456789######"

// fields packs (value, width) pairs MSB first into the top n bits of a uint64, n = sum of widths ≤ 64.
func fields(width int, fs ...[2]uint64) uint64 {
	var v uint64
	n := 0
	for _, f := range fs {
		v = v<<f[1] | f[0]&(1<<f[1]-1)
		n += int(f[1])
	}
	return v << (width - n)
}

// extendedSquitter builds a DF17 frame for icao with the 56-bit ME field and valid parity.
func extendedSquitter(icao uint32, me uint64) []byte {
	msg := make([]byte, modes.LongLen)
	msg[0] = 17<<3 | 5
	msg[1], msg[2], msg[3] = byte(icao>>16), byte(icao>>8), byte(icao)
	for i := range 7 {
		msg[4+i] = byte(me >> (48 - 8*i))
	}
	setParity(msg, 0)
	return msg
}

// shortReply builds a 56-bit frame from its 32-bit header; the parity field is CRC XOR ap
// (ap = ICAO address for address/parity replies, 0 for DF11 with interrogator code 0).
func shortReply(head uint32, ap uint32) []byte {
	msg := []byte{byte(head >> 24), byte(head >> 16), byte(head >> 8), byte(head), 0, 0, 0}
	setParity(msg, ap)
	return msg
}

func setParity(msg []byte, ap uint32) {
	n := len(msg)
	msg[n-3], msg[n-2], msg[n-1] = 0, 0, 0
	p := modes.Remainder(msg) ^ ap
	msg[n-3], msg[n-2], msg[n-1] = byte(p>>16), byte(p>>8), byte(p)
}

// identification: TC 4, category A3, 8-character callsign.
func identification(icao uint32, callsign string) []byte {
	cs := [][2]uint64{{4, 5}, {3, 3}}
	padded := (strings.ToUpper(callsign) + "        ")[:8]
	for _, c := range padded {
		cs = append(cs, [2]uint64{uint64(strings.IndexRune(charset, c)), 6})
	}
	return extendedSquitter(icao, fields(56, cs...))
}

// altitude12 encodes feet as the ES 12-bit altitude field with Q=1 (25 ft steps).
func altitude12(ft float64) uint64 {
	n := uint64(math.Max(0, math.Round((ft+1000)/25)))
	return (n&0x7F0)<<1 | 1<<4 | n&0xF
}

// airbornePosition: TC 11 with barometric altitude and a CPR frame.
func airbornePosition(icao uint32, lat, lon, ft float64, odd bool) []byte {
	cpr := modes.EncodeCPR(lat, lon, odd)
	f := uint64(0)
	if odd {
		f = 1
	}
	return extendedSquitter(icao, fields(56,
		[2]uint64{11, 5}, [2]uint64{0, 2}, [2]uint64{0, 1}, [2]uint64{altitude12(ft), 12},
		[2]uint64{0, 1}, [2]uint64{f, 1}, [2]uint64{uint64(cpr.Lat), 17}, [2]uint64{uint64(cpr.Lon), 17},
	))
}

// velocity: TC 19 subtype 1 (ground speed from east/north components) and barometric vertical rate.
func velocity(icao uint32, gs, trackDeg, vrFpm float64) []byte {
	rad := trackDeg * math.Pi / 180
	vx, vy := gs*math.Sin(rad), gs*math.Cos(rad)
	sign := func(v float64) uint64 {
		if v < 0 {
			return 1
		}
		return 0
	}
	comp := func(v float64) uint64 { return uint64(math.Min(math.Round(math.Abs(v))+1, 1023)) }
	vr := uint64(math.Min(math.Round(math.Abs(vrFpm)/64)+1, 511))
	return extendedSquitter(icao, fields(56,
		[2]uint64{19, 5}, [2]uint64{1, 3}, [2]uint64{0, 1}, [2]uint64{0, 1}, [2]uint64{0, 3},
		[2]uint64{sign(vx), 1}, [2]uint64{comp(vx), 10}, [2]uint64{sign(vy), 1}, [2]uint64{comp(vy), 10},
		[2]uint64{1, 1}, [2]uint64{sign(vrFpm), 1}, [2]uint64{vr, 9},
	))
}

// identity13 encodes a 4-digit octal squawk into the 13-bit C1 A1 C2 A2 C4 A4 X B1 D1 B2 D2 B4 D4 field.
func identity13(squawk string) uint32 {
	d := func(i int) uint32 { return uint32(squawk[i] - '0') }
	a, b, c, dd := d(0), d(1), d(2), d(3)
	bit := func(v uint32, w uint32, pos int) uint32 { return (v / w & 1) << pos }
	return bit(c, 1, 12) | bit(a, 1, 11) | bit(c, 2, 10) | bit(a, 2, 9) | bit(c, 4, 8) | bit(a, 4, 7) |
		bit(b, 1, 5) | bit(dd, 1, 4) | bit(b, 2, 3) | bit(dd, 2, 2) | bit(b, 4, 1) | bit(dd, 4, 0)
}

// emergencyStatus: TC 28 subtype 1 with emergency state (1 = general) and squawk.
func emergencyStatus(icao uint32, state uint64, squawk string) []byte {
	return extendedSquitter(icao, fields(56,
		[2]uint64{28, 5}, [2]uint64{1, 3}, [2]uint64{state, 3}, [2]uint64{uint64(identity13(squawk)), 13},
	))
}

// allCall: DF11 with capability 5 and interrogator code 0.
func allCall(icao uint32) []byte {
	return shortReply(11<<27|5<<24|icao, 0)
}

// ac13 encodes feet as the 13-bit AC field with M=0, Q=1 (25 ft steps).
func ac13(ft float64) uint32 {
	n := uint32(math.Max(0, math.Round((ft+1000)/25)))
	return n>>5<<7 | (n>>4&1)<<5 | 1<<4 | n&0xF
}

// altitudeReply: DF4, airborne (FS 0), address/parity.
func altitudeReply(icao uint32, ft float64) []byte {
	return shortReply(4<<27|ac13(ft), icao)
}

// identityReply: DF5, airborne (FS 0), address/parity.
func identityReply(icao uint32, squawk string) []byte {
	return shortReply(5<<27|identity13(squawk), icao)
}

// surfacePosition: TC 6 surface position with movement and a valid ground track, CA 4 (on ground).
func surfacePosition(icao uint32, lat, lon, gs, trackDeg float64, odd bool) []byte {
	cpr := modes.EncodeSurfaceCPR(lat, lon, odd)
	f := uint64(0)
	if odd {
		f = 1
	}
	trk := uint64(math.Round(math.Mod(trackDeg+360, 360)*128/360)) % 128
	return onGround(extendedSquitter(icao, fields(56,
		[2]uint64{6, 5}, [2]uint64{movement(gs), 7}, [2]uint64{1, 1}, [2]uint64{trk, 7},
		[2]uint64{0, 1}, [2]uint64{f, 1}, [2]uint64{uint64(cpr.Lat), 17}, [2]uint64{uint64(cpr.Lon), 17},
	)))
}

// movement is the inverse of modes.Movement: the 7-bit code nearest to gs kt (1 = stopped, 124 = 175 kt or more).
func movement(gs float64) uint64 {
	codes := []float64{2, 9, 13, 39, 94, 109, 124}
	kt := []float64{0.125, 1, 2, 15, 70, 100, 175}
	if gs < kt[0] {
		return 1
	}
	for i := 1; i < len(codes); i++ {
		if gs < kt[i] {
			return uint64(codes[i-1] + math.Round((gs-kt[i-1])*(codes[i]-codes[i-1])/(kt[i]-kt[i-1])))
		}
	}
	return 124
}

// onGround sets CA 4 (on ground) in a DF17 frame and recomputes its parity.
func onGround(msg []byte) []byte {
	msg[0] = 17<<3 | 4
	setParity(msg, 0)
	return msg
}
