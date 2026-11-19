package geo

import "math"

const earthRadiusNm = 3440.065

type Point struct {
	Lat, Lon float64
}

// DistanceNm returns the great-circle (haversine) distance between a and b in nautical miles.
func DistanceNm(a, b Point) float64 {
	φ1, φ2 := rad(a.Lat), rad(b.Lat)
	dφ, dλ := φ2-φ1, rad(b.Lon-a.Lon)
	h := math.Sin(dφ/2)*math.Sin(dφ/2) + math.Cos(φ1)*math.Cos(φ2)*math.Sin(dλ/2)*math.Sin(dλ/2)
	return 2 * earthRadiusNm * math.Asin(math.Sqrt(h))
}

// Bearing returns the initial true bearing from a to b in degrees, [0, 360).
func Bearing(a, b Point) float64 {
	φ1, φ2 := rad(a.Lat), rad(b.Lat)
	dλ := rad(b.Lon - a.Lon)
	y := math.Sin(dλ) * math.Cos(φ2)
	x := math.Cos(φ1)*math.Sin(φ2) - math.Sin(φ1)*math.Cos(φ2)*math.Cos(dλ)
	return math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
}

func rad(deg float64) float64 { return deg * math.Pi / 180 }
