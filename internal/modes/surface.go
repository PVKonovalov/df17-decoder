package modes

import "math"

// SurfacePosition is the content of ADS-B type codes 5–8. Unavailable values are nil.
type SurfacePosition struct {
	GroundSpeed *float64 // kt, from the movement field
	Track       *float64 // degrees true, only when the track status bit is set
	CPR         CPRFrame // decode with CPRSurfaceLocal
}

// decodeSurfacePosition decodes TC 5–8. ME layout: MOV 6–12, S 13, TRK 14–20, F 22, LAT 23–39, LON 40–56.
// The 1090 MHz Riddle, "Surface position" (https://mode-s.org/1090mhz/content/ads-b/4-surface-position.html).
func decodeSurfacePosition(msg []byte) SurfacePosition {
	p := SurfacePosition{
		GroundSpeed: Movement(int(bits(msg, 38, 7))),
		CPR: CPRFrame{
			Odd: bits(msg, 54, 1) == 1,
			Lat: uint32(bits(msg, 55, 17)),
			Lon: uint32(bits(msg, 72, 17)),
		},
	}
	if bits(msg, 45, 1) == 1 {
		p.Track = new(float64(bits(msg, 46, 7)) * 360 / 128)
	}
	return p
}

var (
	movementCodes = []int{2, 9, 13, 39, 94, 109, 124}
	movementKt    = []float64{0.125, 1, 2, 15, 70, 100, 175}
)

// Movement converts the 7-bit surface movement code to ground speed in kt: nil for 0 (unknown) and the reserved
// codes 125–127, 0 for 1 (stopped), 175 for 124 (175 kt or more), piecewise linear in between.
// The 1090 MHz Riddle, "Surface position" → "Movement".
func Movement(code int) *float64 {
	switch {
	case code == 0 || code > 124:
		return nil
	case code == 1:
		return new(0.0)
	}
	for i := 1; i < len(movementCodes); i++ {
		if code < movementCodes[i] {
			step := (movementKt[i] - movementKt[i-1]) / float64(movementCodes[i]-movementCodes[i-1])
			return new(movementKt[i-1] + float64(code-movementCodes[i-1])*step)
		}
	}
	return new(175.0)
}

// CPRSurfaceLocal decodes one surface frame relative to a reference within half a zone (~45 NM). Surface CPR
// uses 90° zones, so the reference picks one of four candidate positions.
// The 1090 MHz Riddle, "Surface position" → "Locally unambiguous position decoding".
func CPRSurfaceLocal(f CPRFrame, refLat, refLon float64) (lat, lon float64) {
	i := 0.0
	if f.Odd {
		i = 1
	}
	latC, lonC := float64(f.Lat)/cprScale, float64(f.Lon)/cprScale

	dLat := 90 / (4*nz - i)
	j := math.Floor(refLat/dLat) + math.Floor(mod(refLat, dLat)/dLat-latC+0.5)
	lat = dLat * (j + latC)

	dLon := 90 / math.Max(float64(NL(lat))-i, 1)
	m := math.Floor(refLon/dLon) + math.Floor(mod(refLon, dLon)/dLon-lonC+0.5)
	lon = dLon * (m + lonC)
	return lat, lon
}

// EncodeSurfaceCPR is the inverse of CPRSurfaceLocal, used to build synthetic test frames.
func EncodeSurfaceCPR(lat, lon float64, odd bool) CPRFrame {
	i := 0.0
	if odd {
		i = 1
	}
	dLat := 90 / (4*nz - i)
	yz := math.Floor(cprScale*mod(lat, dLat)/dLat + 0.5)
	rLat := dLat * (yz/cprScale + math.Floor(lat/dLat))
	dLon := 90 / math.Max(float64(NL(rLat))-i, 1)
	xz := math.Floor(cprScale*mod(lon, dLon)/dLon + 0.5)
	return CPRFrame{Odd: odd, Lat: uint32(yz) & (cprScale - 1), Lon: uint32(xz) & (cprScale - 1)}
}
