package modes

import (
	"fmt"
	"math"
)

// Velocity is the content of ADS-B type code 19. Unavailable values are nil.
type Velocity struct {
	Subtype          int      // 1/2 ground speed (2 = supersonic, ×4), 3/4 airspeed (4 = supersonic, ×4)
	GroundSpeed      *float64 // kt
	Track            *float64 // degrees true
	Heading          *float64 // degrees (magnetic or true per aircraft), subtypes 3/4
	IAS              *int     // kt
	TAS              *int     // kt
	VerticalRate     *int     // ft/min, negative = descending
	VerticalRateBaro bool     // source of VerticalRate: true barometric, false GNSS
	GNSSBaroDiff     *int     // ft, GNSS altitude minus barometric altitude
}

// decodeVelocity decodes TC 19. ME layout (1-based ME bits, +32 for the frame):
// ST 6–8; subtypes 1/2: S-EW 14, V-EW 15–24, S-NS 25, V-NS 26–35;
// subtypes 3/4: SH 14, HDG 15–24, T 25, AS 26–35; all: VrSrc 36, S-Vr 37, Vr 38–46, S-Dif 49, dAlt 50–56.
// The 1090 MHz Riddle, "Airborne velocity" (https://mode-s.org/1090mhz/content/ads-b/5-airborne-velocity.html).
func decodeVelocity(msg []byte) (Velocity, error) {
	v := Velocity{Subtype: int(bits(msg, 38, 3))}
	scale := 1
	if v.Subtype == 2 || v.Subtype == 4 {
		scale = 4
	}

	switch v.Subtype {
	case 1, 2:
		ew, ns := int(bits(msg, 47, 10)), int(bits(msg, 58, 10))
		if ew != 0 && ns != 0 {
			vEW := float64((ew - 1) * scale)
			if bits(msg, 46, 1) == 1 {
				vEW = -vEW // west
			}
			vNS := float64((ns - 1) * scale)
			if bits(msg, 57, 1) == 1 {
				vNS = -vNS // south
			}
			gs := math.Hypot(vEW, vNS)
			trk := math.Mod(math.Atan2(vEW, vNS)*180/math.Pi+360, 360)
			v.GroundSpeed, v.Track = &gs, &trk
		}
	case 3, 4:
		if bits(msg, 46, 1) == 1 {
			hdg := float64(bits(msg, 47, 10)) * 360 / 1024
			v.Heading = &hdg
		}
		if as := int(bits(msg, 58, 10)); as != 0 {
			kt := (as - 1) * scale
			if bits(msg, 57, 1) == 1 {
				v.TAS = &kt
			} else {
				v.IAS = &kt
			}
		}
	default:
		return v, fmt.Errorf("%w: TC19 subtype %d", ErrUnsupportedTC, v.Subtype)
	}

	v.VerticalRateBaro = bits(msg, 68, 1) == 1
	if vr := int(bits(msg, 70, 9)); vr != 0 {
		rate := (vr - 1) * 64
		if bits(msg, 69, 1) == 1 {
			rate = -rate
		}
		v.VerticalRate = &rate
	}
	if d := int(bits(msg, 82, 7)); d != 0 {
		diff := (d - 1) * 25
		if bits(msg, 81, 1) == 1 {
			diff = -diff
		}
		v.GNSSBaroDiff = &diff
	}
	return v, nil
}
