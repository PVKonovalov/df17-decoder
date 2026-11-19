package modes

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ShortLen = 7  // 56-bit frame, bytes
	LongLen  = 14 // 112-bit frame, bytes
)

var (
	ErrLength        = errors.New("frame length does not match downlink format")
	ErrCRC           = errors.New("crc check failed")
	ErrUnsupported   = errors.New("unsupported downlink format")
	ErrBadCallsign   = errors.New("callsign contains invalid characters")
	ErrUnsupportedTC = errors.New("unsupported type code")
)

// Options controls optional decoder behaviour.
type Options struct {
	FixSingleBitErrors bool // repair one flipped bit in DF17 frames
}

// Message is a decoded Mode S frame. Only the sub-struct matching the content is non-nil.
type Message struct {
	DF       int
	ICAO     uint32
	NonICAO  bool // DF18 with a non-ICAO (anonymous) address
	Time     time.Time
	FixedBit int // 1-based position of a repaired bit, 0 when the frame was intact
	TC       int // ADS-B type code, 0 for non-ES frames

	Identification *Identification
}

// Identification is the content of ADS-B type codes 1–4.
type Identification struct {
	Callsign string
	Category string // emitter category, e.g. "A3"; letter from TC (4→A … 1→D), digit is CA
}

// DF returns the downlink format of msg (24 for any frame starting with binary 11).
func DF(msg []byte) int {
	df := int(bits(msg, 1, 5))
	if df >= 24 {
		return 24
	}
	return df
}

// Decode validates and decodes one raw frame received at t.
// The returned Message always carries DF and Time, even when err != nil, so callers can count drops.
func Decode(msg []byte, t time.Time, opt Options) (Message, error) {
	if len(msg) != ShortLen && len(msg) != LongLen {
		return Message{Time: t}, ErrLength
	}
	m := Message{DF: DF(msg), Time: t}
	if (m.DF >= 16) != (len(msg) == LongLen) {
		return m, ErrLength
	}

	switch m.DF {
	case 17, 18:
		return decodeExtendedSquitter(msg, m, opt)
	default:
		return m, ErrUnsupported
	}
}

// decodeExtendedSquitter handles DF17/18.
// The 1090 MHz Riddle, "ADS-B basics" (https://mode-s.org/decode/content/ads-b/1-basics.html).
func decodeExtendedSquitter(msg []byte, m Message, opt Options) (Message, error) {
	if Remainder(msg) != 0 {
		if !opt.FixSingleBitErrors || m.DF != 17 {
			return m, ErrCRC
		}
		fixed, pos, ok := fixSingleBit(msg)
		if !ok {
			return m, ErrCRC
		}
		msg, m.FixedBit = fixed, pos
	}

	if m.DF == 18 {
		switch cf := bits(msg, 6, 3); cf {
		case 0:
		case 1:
			m.NonICAO = true
		default:
			return m, fmt.Errorf("%w: DF18 CF=%d", ErrUnsupported, cf)
		}
	}

	m.ICAO = uint32(bits(msg, 9, 24))
	m.TC = int(bits(msg, 33, 5))

	switch {
	case m.TC >= 1 && m.TC <= 4:
		id, err := decodeIdentification(msg)
		if err != nil {
			return m, err
		}
		m.Identification = &id
	default:
		return m, ErrUnsupportedTC
	}
	return m, nil
}

const charset = "#ABCDEFGHIJKLMNOPQRSTUVWXYZ##### ###############0123456789######"

// decodeIdentification decodes TC 1–4: emitter category and 8-character callsign.
// The 1090 MHz Riddle, "Aircraft identification and category" (https://mode-s.org/decode/content/ads-b/2-identification.html).
func decodeIdentification(msg []byte) (Identification, error) {
	tc := int(bits(msg, 33, 5))
	ca := int(bits(msg, 38, 3))

	var cs [8]byte
	for i := range cs {
		cs[i] = charset[bits(msg, 41+6*i, 6)]
	}
	callsign := strings.TrimRight(string(cs[:]), " ")
	if strings.ContainsRune(callsign, '#') {
		return Identification{}, fmt.Errorf("%w: %q", ErrBadCallsign, callsign)
	}
	return Identification{
		Callsign: callsign,
		Category: fmt.Sprintf("%c%d", 'A'+4-tc, ca),
	}, nil
}

// ICAOString formats a 24-bit address as 6 upper-case hex digits.
func ICAOString(icao uint32) string {
	return fmt.Sprintf("%06X", icao)
}
