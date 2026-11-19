package modes

import (
	"errors"
	"math"
)

const (
	nz       = 15
	cprScale = 1 << 17
)

var ErrCPRZoneMismatch = errors.New("cpr: even and odd frames are in different longitude zones")

// CPRFrame is the compact position from one airborne position message.
type CPRFrame struct {
	Odd bool
	Lat uint32 // 17-bit YZ
	Lon uint32 // 17-bit XZ
}

// NL returns the number of longitude zones at latitude lat (degrees), closed form.
// The 1090 MHz Riddle, "Compact Position Reporting" → "Number of longitude zones" (https://mode-s.org/1090mhz/content/ads-b/3-airborne-position.html).
func NL(lat float64) int {
	lat = math.Abs(lat)
	switch {
	case lat == 0:
		return 59
	case lat == 87:
		return 2
	case lat > 87:
		return 1
	}
	a := 1 - math.Cos(math.Pi/(2*nz))
	c := math.Cos(math.Pi / 180 * lat)
	return int(math.Floor(2 * math.Pi / math.Acos(1-a/(c*c))))
}

// CPRGlobal decodes an even/odd pair without a reference. oddNewest selects which frame's position is
// returned (the most recent one). Returns ErrCPRZoneMismatch when the two latitudes fall in different NL zones.
// The 1090 MHz Riddle, "Airborne position" → "Globally unambiguous position decoding".
func CPRGlobal(even, odd CPRFrame, oddNewest bool) (lat, lon float64, err error) {
	latE, lonE := float64(even.Lat)/cprScale, float64(even.Lon)/cprScale
	latO, lonO := float64(odd.Lat)/cprScale, float64(odd.Lon)/cprScale

	const dLatE, dLatO = 360.0 / (4 * nz), 360.0 / (4*nz - 1)
	j := math.Floor(59*latE - 60*latO + 0.5)

	rLatE := dLatE * (mod(j, 60) + latE)
	rLatO := dLatO * (mod(j, 59) + latO)
	if rLatE >= 270 {
		rLatE -= 360
	}
	if rLatO >= 270 {
		rLatO -= 360
	}
	if NL(rLatE) != NL(rLatO) {
		return 0, 0, ErrCPRZoneMismatch
	}

	nl := NL(rLatE)
	m := math.Floor(lonE*float64(nl-1) - lonO*float64(nl) + 0.5)
	if oddNewest {
		lat = rLatO
		ni := float64(max(nl-1, 1))
		lon = 360 / ni * (mod(m, ni) + lonO)
	} else {
		lat = rLatE
		ni := float64(max(nl, 1))
		lon = 360 / ni * (mod(m, ni) + lonE)
	}
	if lon >= 180 {
		lon -= 360
	}
	return lat, lon, nil
}

// CPRLocal decodes one frame relative to a reference position that is within half a zone (~180 NM).
// The 1090 MHz Riddle, "Airborne position" → "Locally unambiguous position decoding".
func CPRLocal(f CPRFrame, refLat, refLon float64) (lat, lon float64) {
	i := 0.0
	if f.Odd {
		i = 1
	}
	latC, lonC := float64(f.Lat)/cprScale, float64(f.Lon)/cprScale

	dLat := 360 / (4*nz - i)
	j := math.Floor(refLat/dLat) + math.Floor(mod(refLat, dLat)/dLat-latC+0.5)
	lat = dLat * (j + latC)

	dLon := 360 / math.Max(float64(NL(lat))-i, 1)
	m := math.Floor(refLon/dLon) + math.Floor(mod(refLon, dLon)/dLon-lonC+0.5)
	lon = dLon * (m + lonC)
	return lat, lon
}

// EncodeCPR is the inverse of the decoders (airborne, 17-bit), used to build synthetic test frames.
// The 1090 MHz Riddle, "Compact Position Reporting" → encoding.
func EncodeCPR(lat, lon float64, odd bool) CPRFrame {
	i := 0.0
	if odd {
		i = 1
	}
	dLat := 360 / (4*nz - i)
	yz := math.Floor(cprScale*mod(lat, dLat)/dLat + 0.5)
	rLat := dLat * (yz/cprScale + math.Floor(lat/dLat))
	dLon := 360 / math.Max(float64(NL(rLat))-i, 1)
	xz := math.Floor(cprScale*mod(lon, dLon)/dLon + 0.5)
	return CPRFrame{Odd: odd, Lat: uint32(yz) & (cprScale - 1), Lon: uint32(xz) & (cprScale - 1)}
}

// mod is the always-non-negative modulo used by the CPR formulas.
func mod(x, y float64) float64 {
	return x - y*math.Floor(x/y)
}
