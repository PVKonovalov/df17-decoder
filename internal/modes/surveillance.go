package modes

import "fmt"

// AllCall is the content of a DF11 all-call reply / acquisition squitter.
type AllCall struct {
	Capability int // CA
	IID        int // interrogator identifier (CRC remainder); 0 for a spontaneous acquisition squitter
}

// Surveillance is the content of an address/parity reply (DF0/4/5/16/20/21).
// The address comes from the CRC remainder, so the frame is only trustworthy if Message.ICAO is already known.
type Surveillance struct {
	Altitude *int   // ft, DF0/4/16/20
	Squawk   string // 4 octal digits, DF5/21
	OnGround *bool  // from FS (DF4/5/20/21) or VS (DF0/16); nil when unknown
	Alert    bool   // FS alert bit: squawk recently changed or emergency
	SPI      bool   // FS special position identification
	BDS20    string // callsign from a Comm-B BDS 2,0 reply (DF20/21), "" otherwise
	CommB    *CommB // BDS 4,0/5,0/6,0 inferred from a DF20/21 MB field; nil when none fits
}

// decodeAllCall decodes DF11: CA 6–8, AA 9–32, PI 33–56. The remainder must be ≤ 0x7F (interrogator code).
// The 1090 MHz Riddle, "All-call reply" (https://mode-s.org/1090mhz/content/mode-s/2-allcall.html).
func decodeAllCall(msg []byte, m Message) (Message, error) {
	r := Remainder(msg)
	if r > 0x7F {
		return m, ErrCRC
	}
	m.ICAO = uint32(bits(msg, 9, 24))
	m.AllCall = &AllCall{Capability: int(bits(msg, 6, 3)), IID: int(r)}
	m.OnGround = groundFromCA(m.AllCall.Capability)
	return m, nil
}

// decodeSurveillance decodes address/parity replies. Layout: DF 1–5; FS 6–8 (DF4/5/20/21) or VS 6 (DF0/16);
// AC or ID 20–32; MB 33–88 (DF20/21); AP last 24 bits. The ICAO address is the CRC remainder.
// The 1090 MHz Riddle, "Surveillance reply" (https://mode-s.org/1090mhz/content/mode-s/3-surveillance.html)
// and "Elementary surveillance" for BDS 2,0 (https://mode-s.org/1090mhz/content/mode-s/6-els.html).
func decodeSurveillance(msg []byte, m Message) (Message, error) {
	m.ICAO = Remainder(msg)
	m.AddressParity = true
	s := &Surveillance{}

	switch m.DF {
	case 0, 16:
		s.OnGround = new(bits(msg, 6, 1) == 1)
	case 4, 5, 20, 21:
		fs := bits(msg, 6, 3)
		if fs > 5 {
			return m, fmt.Errorf("%w: flight status %d", ErrInvalid, fs)
		}
		switch fs {
		case 0, 2:
			s.OnGround = new(false)
		case 1, 3:
			s.OnGround = new(true)
		}
		s.Alert = fs >= 2 && fs <= 4
		s.SPI = fs == 4 || fs == 5
	}

	field := uint32(bits(msg, 20, 13))
	switch m.DF {
	case 0, 4, 16, 20:
		if ft, ok := Altitude13(field); ok {
			s.Altitude = &ft
		}
	case 5, 21:
		if field != 0 {
			s.Squawk = Squawk(field)
		}
	}

	if m.DF == 20 || m.DF == 21 {
		if bits(msg, 33, 8) == 0x20 {
			if cs, err := decodeCallsign(msg); err == nil {
				s.BDS20 = cs
			}
		}
		if s.BDS20 == "" {
			s.CommB = inferCommB(msg, s.Altitude)
		}
	}

	m.Surveillance = s
	m.OnGround = s.OnGround
	return m, nil
}

// groundFromCA maps the DF11/17 capability field: 4 on ground, 5 airborne, otherwise unknown.
func groundFromCA(ca int) *bool {
	switch ca {
	case 4:
		return new(true)
	case 5:
		return new(false)
	}
	return nil
}
