package modes

import "math"

// BDS40 is "selected vertical intention" (Comm-B register 4,0). Unavailable values are nil.
type BDS40 struct {
	MCPAltitude *int     // ft, MCP/FCU selected altitude
	FMSAltitude *int     // ft, FMS selected altitude
	BaroSetting *float64 // hPa
}

// BDS50 is "track and turn report" (Comm-B register 5,0). Unavailable values are nil.
type BDS50 struct {
	Roll        *float64 // degrees, positive = right wing down
	Track       *float64 // degrees true
	GroundSpeed *float64 // kt
	TrackRate   *float64 // degrees/s
	TAS         *int     // kt
}

// BDS60 is "heading and speed report" (Comm-B register 6,0). Unavailable values are nil.
type BDS60 struct {
	Heading          *float64 // degrees magnetic
	IAS              *int     // kt
	Mach             *float64
	VerticalRateBaro *int // ft/min
	VerticalRateINS  *int // ft/min
}

// CommB holds the registers a DF20/21 MB field may contain. The MB field does not name its register (except
// BDS 2,0), so each register's layout is checked for consistent status bits, zero reserved bits and plausible
// values. Every register that fits is set; Ambiguous is true when more than one does, and the caller must pick
// one using other knowledge of the aircraft, or drop the reply.
type CommB struct {
	BDS40     *BDS40
	BDS50     *BDS50
	BDS60     *BDS60
	Ambiguous bool
}

// mb reads MB bits (1-based within the 56-bit MB field, frame bits 33–88).
func mb(msg []byte, start, length int) uint64 { return bits(msg, 32+start, length) }

// statusOK reports whether a value with status bit at s and value bits [from, from+n) is consistent:
// status 0 requires all value bits to be 0.
func statusOK(msg []byte, s, from, n int) bool {
	return mb(msg, s, 1) == 1 || mb(msg, from, n) == 0
}

// signed decodes a sign bit at s followed by n magnitude bits (two's complement) as an integer.
func signed(msg []byte, s, n int) int {
	v := int(mb(msg, s+1, n))
	if mb(msg, s, 1) == 1 {
		v -= 1 << n
	}
	return v
}

// inferCommB tries BDS 4,0, 5,0 and 6,0 on the MB field of a DF20/21 frame; altFt is the altitude from the same
// reply (nil when unknown), used to check IAS against Mach. Returns nil when no register fits.
// The 1090 MHz Riddle, "Enhanced Mode S" (https://mode-s.org/1090mhz/content/mode-s/7-ehs.html).
func inferCommB(msg []byte, altFt *int) *CommB {
	c := &CommB{BDS40: decodeBDS40(msg), BDS50: decodeBDS50(msg), BDS60: decodeBDS60(msg, altFt)}
	n := 0
	for _, ok := range []bool{c.BDS40 != nil, c.BDS50 != nil, c.BDS60 != nil} {
		if ok {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	c.Ambiguous = n > 1
	return c
}

// decodeBDS40 decodes MB as BDS 4,0, or returns nil when the layout does not fit. MB layout: MCP status 1,
// alt 2–13 (16 ft); FMS status 14, alt 15–26; baro status 27, 28–39 (0.1 hPa above 800); reserved 40–47;
// mode status 48, VNAV 49, alt hold 50, approach 51; reserved 52–53; source status 54, source 55–56.
func decodeBDS40(msg []byte) *BDS40 {
	if mb(msg, 40, 8) != 0 || mb(msg, 52, 2) != 0 ||
		!statusOK(msg, 1, 2, 12) || !statusOK(msg, 14, 15, 12) || !statusOK(msg, 27, 28, 12) ||
		!statusOK(msg, 48, 49, 3) || !statusOK(msg, 54, 55, 2) {
		return nil
	}
	var b BDS40
	if mb(msg, 1, 1) == 1 {
		b.MCPAltitude = new(int(mb(msg, 2, 12)) * 16)
	}
	if mb(msg, 14, 1) == 1 {
		b.FMSAltitude = new(int(mb(msg, 15, 12)) * 16)
	}
	if mb(msg, 27, 1) == 1 {
		b.BaroSetting = new(math.Round((800+float64(mb(msg, 28, 12))*0.1)*10) / 10)
	}
	switch {
	case b.MCPAltitude == nil && b.FMSAltitude == nil && b.BaroSetting == nil:
		return nil
	case b.MCPAltitude != nil && (*b.MCPAltitude == 0 || *b.MCPAltitude > 50000),
		b.FMSAltitude != nil && (*b.FMSAltitude == 0 || *b.FMSAltitude > 50000),
		b.BaroSetting != nil && (*b.BaroSetting < 900 || *b.BaroSetting > 1100):
		return nil
	}
	return &b
}

// decodeBDS50 decodes MB as BDS 5,0, or returns nil when the layout does not fit. MB layout: roll status 1,
// sign 2, 3–11 (45/256°); track status 12, sign 13, 14–23 (90/512°); GS status 24, 25–34 (2 kt);
// track rate status 35, sign 36, 37–45 (8/256 °/s); TAS status 46, 47–56 (2 kt).
func decodeBDS50(msg []byte) *BDS50 {
	if !statusOK(msg, 1, 2, 10) || !statusOK(msg, 12, 13, 11) || !statusOK(msg, 24, 25, 10) ||
		!statusOK(msg, 35, 36, 10) || !statusOK(msg, 46, 47, 10) {
		return nil
	}
	var b BDS50
	if mb(msg, 1, 1) == 1 {
		b.Roll = new(float64(signed(msg, 2, 9)) * 45 / 256)
	}
	if mb(msg, 12, 1) == 1 {
		b.Track = new(math.Mod(float64(signed(msg, 13, 10))*90/512+360, 360))
	}
	if mb(msg, 24, 1) == 1 {
		b.GroundSpeed = new(float64(mb(msg, 25, 10)) * 2)
	}
	if mb(msg, 35, 1) == 1 {
		b.TrackRate = new(float64(signed(msg, 36, 9)) * 8 / 256)
	}
	if mb(msg, 46, 1) == 1 {
		b.TAS = new(int(mb(msg, 47, 10)) * 2)
	}
	switch {
	case b.Roll == nil && b.Track == nil && b.GroundSpeed == nil && b.TAS == nil:
		return nil
	case b.Roll != nil && math.Abs(*b.Roll) > 50,
		b.GroundSpeed != nil && *b.GroundSpeed > 600,
		b.TAS != nil && *b.TAS > 600,
		b.GroundSpeed != nil && b.TAS != nil && math.Abs(*b.GroundSpeed-float64(*b.TAS)) > 200:
		return nil
	}
	return &b
}

// decodeBDS60 decodes MB as BDS 6,0, or returns nil when the layout does not fit. MB layout: heading status 1,
// sign 2, 3–12 (90/512°); IAS status 13, 14–23 (1 kt); Mach status 24, 25–34 (0.004); baro vertical rate
// status 35, sign 36, 37–45 (32 ft/min); inertial vertical rate status 46, sign 47, 48–56 (32 ft/min).
// With altFt known, IAS and Mach must agree within 0.1 Mach in the standard atmosphere.
func decodeBDS60(msg []byte, altFt *int) *BDS60 {
	if !statusOK(msg, 1, 2, 11) || !statusOK(msg, 13, 14, 10) || !statusOK(msg, 24, 25, 10) ||
		!statusOK(msg, 35, 36, 10) || !statusOK(msg, 46, 47, 10) {
		return nil
	}
	var b BDS60
	if mb(msg, 1, 1) == 1 {
		b.Heading = new(math.Mod(float64(signed(msg, 2, 10))*90/512+360, 360))
	}
	if mb(msg, 13, 1) == 1 {
		b.IAS = new(int(mb(msg, 14, 10)))
	}
	if mb(msg, 24, 1) == 1 {
		b.Mach = new(float64(mb(msg, 25, 10)) * 2.048 / 512)
	}
	if mb(msg, 35, 1) == 1 {
		b.VerticalRateBaro = new(signed(msg, 36, 9) * 32)
	}
	if mb(msg, 46, 1) == 1 {
		b.VerticalRateINS = new(signed(msg, 47, 9) * 32)
	}
	abs := func(v int) int { return max(v, -v) }
	switch {
	case b.Heading == nil && b.IAS == nil && b.Mach == nil && b.VerticalRateBaro == nil && b.VerticalRateINS == nil:
		return nil
	case b.IAS != nil && (*b.IAS == 0 || *b.IAS > 500),
		b.Mach != nil && (*b.Mach == 0 || *b.Mach > 1),
		b.VerticalRateBaro != nil && abs(*b.VerticalRateBaro) > 6000,
		b.VerticalRateINS != nil && abs(*b.VerticalRateINS) > 6000,
		b.VerticalRateBaro != nil && b.VerticalRateINS != nil && abs(*b.VerticalRateBaro-*b.VerticalRateINS) > 2000,
		altFt != nil && b.IAS != nil && b.Mach != nil && math.Abs(CASToMach(float64(*b.IAS), float64(*altFt))-*b.Mach) > 0.1:
		return nil
	}
	return &b
}

// CASToMach converts calibrated airspeed (kt) at pressure altitude (ft) to Mach in the ISA standard atmosphere.
func CASToMach(casKt, altFt float64) float64 {
	const (
		p0 = 101325.0 // Pa
		a0 = 661.47   // kt, speed of sound at sea level
	)
	h := altFt * 0.3048
	p := p0 * math.Pow(1-2.25577e-5*h, 5.25588)
	if h > 11000 {
		p = 22632 * math.Exp(-(h-11000)/6341.62)
	}
	qc := p0 * (math.Pow(1+0.2*(casKt/a0)*(casKt/a0), 3.5) - 1)
	return math.Sqrt(5 * (math.Pow(qc/p+1, 2.0/7) - 1))
}
