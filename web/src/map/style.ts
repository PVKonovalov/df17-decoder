import type { Aircraft } from '../api/types'
import type { TrailPoint } from '../state/reducer'

export const ALT_MAX_FT = 40_000
export const STALE_POSITION_MS = 30_000
export const NO_ALT_COLOR = 'hsl(0 0% 55%)'
export const GROUND_COLOR = 'hsl(30 25% 45%)'

/** Hue stops from the ground to ALT_MAX_FT: orange, yellow, green, cyan, blue, magenta. */
const HUE_STOPS: readonly (readonly [number, number])[] = [
  [0, 20],
  [4_000, 50],
  [10_000, 120],
  [20_000, 190],
  [30_000, 240],
  [ALT_MAX_FT, 300],
]

/** altitudeHue maps feet to a hue on the continuous scale, clamped to [0, ALT_MAX_FT]. */
export function altitudeHue(ft: number): number {
  const x = Math.min(Math.max(ft, 0), ALT_MAX_FT)
  for (let i = 1; i < HUE_STOPS.length; i++) {
    const [a0, h0] = HUE_STOPS[i - 1] as readonly [number, number]
    const [a1, h1] = HUE_STOPS[i] as readonly [number, number]
    if (x <= a1) {
      return h0 + ((x - a0) / (a1 - a0)) * (h1 - h0)
    }
  }
  return 300
}

/** altitudeColor returns the CSS color for an altitude in feet; undefined (unknown) is grey. */
export function altitudeColor(ft: number | undefined, onGround = false): string {
  if (onGround) {
    return GROUND_COLOR
  }
  if (ft === undefined) {
    return NO_ALT_COLOR
  }
  return `hsl(${Math.round(altitudeHue(ft))} 85% 45%)`
}

/** Signal scale (Beast RSSI, dBFS): red at SIGNAL_MIN_DB and below, through yellow, to green at SIGNAL_MAX_DB. */
export const SIGNAL_MIN_DB = -36
export const SIGNAL_MAX_DB = -3

/** ColorMode selects what marker colors show. */
export type ColorMode = 'altitude' | 'signal'

/** signalColor returns the CSS color for an RSSI in dBFS; undefined (no signal level) is grey. */
export function signalColor(db: number | undefined): string {
  if (db === undefined) {
    return NO_ALT_COLOR
  }
  const x = (Math.min(Math.max(db, SIGNAL_MIN_DB), SIGNAL_MAX_DB) - SIGNAL_MIN_DB) / (SIGNAL_MAX_DB - SIGNAL_MIN_DB)
  return `hsl(${Math.round(x * 120)} 80% 42%)`
}

/** markerColor is the aircraft's marker color in the given mode. */
export function markerColor(a: Aircraft, mode: ColorMode): string {
  return mode === 'signal' ? signalColor(a.rssi) : altitudeColor(aircraftAltitude(a), a.onGround === true)
}

/** aircraftAltitude prefers barometric altitude, then GNSS. */
export function aircraftAltitude(a: Aircraft): number | undefined {
  return a.altBaro ?? a.altGnss
}

/** markerLabel is the callsign (or ICAO) plus altitude in hundreds of feet, e.g. "WZZ314 040", "4XABC GND". */
export function markerLabel(a: Aircraft): string {
  const name = a.callsign ?? a.icao
  if (a.onGround === true) {
    return `${name} GND`
  }
  const alt = aircraftAltitude(a)
  if (alt === undefined) {
    return name
  }
  const hundreds = Math.round(alt / 100)
  return `${name} ${hundreds < 0 ? hundreds : String(hundreds).padStart(3, '0')}`
}

/** dataNow is the newest lastSeen in the table: "now" in data time, so replays fade correctly. */
export function dataNow(aircraft: Iterable<Aircraft>): number {
  let now = 0
  for (const a of aircraft) {
    now = Math.max(now, Date.parse(a.lastSeen) || 0)
  }
  return now
}

/** isStale reports whether the aircraft's position is older than STALE_POSITION_MS at data time now. */
export function isStale(a: Aircraft, now: number): boolean {
  if (a.posTime === undefined) {
    return true
  }
  return now - Date.parse(a.posTime) > STALE_POSITION_MS
}

/** rotation is the marker rotation in degrees: track, else heading, else 0 (pointing north). */
export function rotation(a: Aircraft): number {
  return a.track ?? a.heading ?? 0
}

export interface TrailSegment {
  color: string
  points: [number, number][]
}

/**
 * trailSegments splits a trail into polylines of one altitude color each (500 ft buckets). Consecutive
 * segments share their boundary point so the line is continuous.
 */
export function trailSegments(trail: readonly TrailPoint[]): TrailSegment[] {
  const out: TrailSegment[] = []
  let cur: TrailSegment | undefined
  let curBucket: number | undefined = NaN
  for (const p of trail) {
    const bucket = p.alt === undefined ? undefined : Math.round(p.alt / 500)
    const ll: [number, number] = [p.lat, p.lon]
    if (cur === undefined || bucket !== curBucket) {
      const prev = cur?.points.at(-1)
      cur = { color: altitudeColor(bucket === undefined ? undefined : bucket * 500), points: prev ? [prev, ll] : [ll] }
      curBucket = bucket
      out.push(cur)
    } else {
      cur.points.push(ll)
    }
  }
  return out.filter((s) => s.points.length > 1)
}

/** destinationNorth returns the point nm nautical miles due north of lat/lon. */
export function destinationNorth(lat: number, lon: number, nm: number): [number, number] {
  return [lat + nm / 60, lon]
}
