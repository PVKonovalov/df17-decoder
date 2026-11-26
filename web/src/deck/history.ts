import type { Timeline } from '../api/types'

export const SEEK_BEFORE_FLIGHT_MS = 30_000
const MINUTE = 60_000
const TARGET_BUCKETS = 400

/**
 * timelineQuery returns the range and bucket to fetch the density strip for the window [from, to]: padded and
 * snapped to a quarter of the window, so the query only changes after the window has moved that far, with whole
 * minute buckets (about 400 of them).
 */
export function timelineQuery(from: number, to: number): { from: number; to: number; bucketMs: number } {
  const q = Math.max(Math.round((to - from) / 4), MINUTE)
  const qFrom = Math.floor(from / q) * q - q
  const qTo = Math.ceil(to / q) * q + q
  const bucketMs = Math.max(Math.ceil((qTo - qFrom) / TARGET_BUCKETS / MINUTE), 1) * MINUTE
  return { from: qFrom, to: qTo, bucketMs }
}

/**
 * densityPaths returns SVG area paths in a 1000 × height box for the window [winFrom, winTo]: all flights and the
 * ADS-B ones, scaled to the busiest bucket in the window.
 */
export function densityPaths(tl: Timeline, bucketMs: number, winFrom: number, winTo: number, height: number) {
  const start = Date.parse(tl.from)
  const x = (ms: number) => (((ms - winFrom) / (winTo - winFrom)) * 1000).toFixed(1)
  const shown = tl.buckets
    .map((b, i) => ({ t: start + i * bucketMs, all: b[0] + b[1], adsb: b[0] }))
    .filter((b) => b.t + bucketMs > winFrom && b.t < winTo)
  const peak = Math.max(1, ...shown.map((b) => b.all))
  const area = (v: (b: (typeof shown)[number]) => number) => {
    if (shown.length === 0) {
      return ''
    }
    const y = (n: number) => (height - (n / peak) * height).toFixed(1)
    const steps = shown.map((b) => `L${x(b.t)},${y(v(b))}L${x(b.t + bucketMs)},${y(v(b))}`).join('')
    return `M${x(shown[0]?.t ?? 0)},${height}${steps}L${x((shown.at(-1)?.t ?? 0) + bucketMs)},${height}Z`
  }
  return { all: area((b) => b.all), adsb: area((b) => b.adsb), peak }
}

/**
 * flightSeek returns where to seek for a stored flight: SEEK_BEFORE_FLIGHT_MS before it was first seen, no earlier
 * than the tape's start (rounded up to a whole second, as seeks are); null when the flight ended before the tape
 * starts (retention removed it).
 */
export function flightSeek(firstSeen: string, lastSeen: string, tapeStart: string | undefined): string | null {
  const start = tapeStart === undefined ? -Infinity : Math.ceil(Date.parse(tapeStart) / 1000) * 1000
  if (Date.parse(lastSeen) < start) {
    return null
  }
  return new Date(Math.max(Date.parse(firstSeen) - SEEK_BEFORE_FLIGHT_MS, start)).toISOString()
}
