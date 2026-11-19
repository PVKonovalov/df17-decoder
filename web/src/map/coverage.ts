import type { Coverage, CoveragePoint } from '../api/types'

/** coveragePositions are the outline's polygon vertices in bearing order; fewer than 3 points give none. */
export function coveragePositions(c: Coverage | null): [number, number][] {
  if (c === null || c.points.length < 3) {
    return []
  }
  return c.points.map((p) => [p.lat, p.lon])
}

/** initialBearing is the true bearing in degrees [0, 360) from (lat1, lon1) to (lat2, lon2). */
export function initialBearing(lat1: number, lon1: number, lat2: number, lon2: number): number {
  const rad = Math.PI / 180
  const φ1 = lat1 * rad
  const φ2 = lat2 * rad
  const dλ = (lon2 - lon1) * rad
  const y = Math.sin(dλ) * Math.cos(φ2)
  const x = Math.cos(φ1) * Math.sin(φ2) - Math.sin(φ1) * Math.cos(φ2) * Math.cos(dλ)
  return ((Math.atan2(y, x) / rad) % 360 + 360) % 360
}

/** coverageAt is the outline point in the bearing bucket of (lat, lon) as seen from the receiver, if any. */
export function coverageAt(c: Coverage, lat: number, lon: number): CoveragePoint | undefined {
  const bucket = (b: number) => Math.floor(b / c.bucketDeg) % Math.round(360 / c.bucketDeg)
  const want = bucket(initialBearing(c.receiver.lat, c.receiver.lon, lat, lon))
  return c.points.find((p) => bucket(p.bearing) === want)
}

/** coverageLabel describes an outline point, e.g. "142 nm at 045° · FL350". */
export function coverageLabel(p: CoveragePoint): string {
  const brg = String(Math.round(p.bearing) % 360).padStart(3, '0')
  const alt = p.alt === undefined ? '' : ` · ${p.alt >= 18_000 ? `FL${String(Math.round(p.alt / 100)).padStart(3, '0')}` : `${p.alt.toLocaleString('en-US')} ft`}`
  return `${p.distanceNm.toFixed(0)} nm at ${brg}°${alt}`
}
