package modes

import (
	"errors"
	"fmt"
	"math"
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

	Identification   *Identification
	AirbornePosition *AirbornePosition
}

// AirbornePosition is the content of ADS-B type codes 9–18 (barometric altitude) and 20–22 (GNSS height).
type AirbornePosition struct {
	Altitude           *int // ft; nil when unavailable or invalid
	GNSS               bool // Altitude is GNSS height (TC 20–22) rather than barometric
	SurveillanceStatus int  // 0 none, 1 permanent alert, 2 temporary alert, 3 SPI
	CPR                CPRFrame
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
	case m.TC >= 9 && m.TC <= 18, m.TC >= 20 && m.TC <= 22:
		p := decodeAirbornePosition(msg, m.TC)
		m.AirbornePosition = &p
	default:
		return m, ErrUnsupportedTC
	}
	return m, nil
}

// decodeAirbornePosition decodes TC 9–18 and 20–22.
// ME layout: TC(5) SS(2) SAF(1) ALT(12) T(1) F(1) LAT-CPR(17) LON-CPR(17).
// The 1090 MHz Riddle, "Airborne position" (https://mode-s.org/decode/content/ads-b/3-airborne-position.html).
func decodeAirbornePosition(msg []byte, tc int) AirbornePosition {
	p := AirbornePosition{
		GNSS:               tc >= 20,
		SurveillanceStatus: int(bits(msg, 38, 2)),
		CPR: CPRFrame{
			Odd: bits(msg, 54, 1) == 1,
			Lat: uint32(bits(msg, 55, 17)),
			Lon: uint32(bits(msg, 72, 17)),
		},
	}
	alt := uint32(bits(msg, 41, 12))
	if p.GNSS {
		// GNSS height is a plain 12-bit value in metres (as in pyModeS); not verified against DO-260B.
		if alt != 0 {
			ft := int(math.Round(float64(alt) * 3.28084))
			p.Altitude = &ft
		}
	} else if ft, ok := Altitude12(alt); ok {
		p.Altitude = &ft
	}
	return p
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
