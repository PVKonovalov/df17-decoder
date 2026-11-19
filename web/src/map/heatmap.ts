import type { CoverageGrid } from '../api/types'
import { initialBearing } from './coverage'
import { SIGNAL_MAX_DB, SIGNAL_MIN_DB, signalColor } from './style'

export type HeatMetric = 'signal' | 'count'
export type HeatBand = 'all' | 'low' | 'mid' | 'high'

export const HEAT_BANDS: readonly { band: HeatBand; label: string }[] = [
  { band: 'all', label: 'All altitudes' },
  { band: 'low', label: 'Below 10,000 ft' },
  { band: 'mid', label: '10,000–25,000 ft' },
  { band: 'high', label: '25,000 ft and above' },
]

/** HeatCell is one grid cell after merging the selected altitude bands. Signal values are in dBFS. */
export interface HeatCell {
  bucket: number
  ring: number
  n: number // positions
  sn: number // positions with a signal level
  avg?: number // mean of linear power
  min?: number
  max?: number
}

/**
 * heatCells merges the cells of band ('all': every band, including unknown altitude) by bucket and ring. The
 * signal average of merged cells is weighted by their positions with a signal level, in linear power.
 */
export function heatCells(grid: CoverageGrid | null, band: HeatBand): HeatCell[] {
  if (grid === null) {
    return []
  }
  const merged = new Map<string, HeatCell & { pow: number }>()
  for (const b of grid.bands) {
    if (band !== 'all' && b.name !== band) {
      continue
    }
    for (const [bucket, ring, n, sn, avg, min, max] of b.cells) {
      const key = `${bucket}/${ring}`
      let c = merged.get(key)
      if (c === undefined) {
        c = { bucket, ring, n: 0, sn: 0, pow: 0 }
        merged.set(key, c)
      }
      c.n += n
      if (sn > 0) {
        c.sn += sn
        c.pow += sn * 10 ** (avg / 10)
        c.min = c.min === undefined ? min : Math.min(c.min, min)
        c.max = c.max === undefined ? max : Math.max(c.max, max)
      }
    }
  }
  return [...merged.values()].map(({ pow, ...c }) => (c.sn > 0 ? { ...c, avg: 10 * Math.log10(pow / c.sn) } : c))
}

/** hasSignal reports whether any cell of the grid has a signal level (Beast input). */
export function hasSignal(grid: CoverageGrid | null): boolean {
  return grid?.bands.some((b) => b.cells.some((c) => c[3] > 0)) ?? false
}

/** countColor maps n on a log scale from 1 (red) to max (green). */
export function countColor(n: number, max: number): string {
  const x = max > 1 ? Math.log(Math.max(n, 1)) / Math.log(max) : 1
  return signalColor(SIGNAL_MIN_DB + x * (SIGNAL_MAX_DB - SIGNAL_MIN_DB))
}

/** heatColor is the fill of cell c for metric; max is the largest position count among the drawn cells. */
export function heatColor(c: HeatCell, metric: HeatMetric, max: number): string {
  return metric === 'signal' ? signalColor(c.avg) : countColor(c.n, max)
}

const EARTH_NM = 3440.065
const RAD = Math.PI / 180

/** destination returns the point d nautical miles from (lat, lon) along true bearing brg. */
export function destination(lat: number, lon: number, brg: number, d: number): [number, number] {
  const δ = d / EARTH_NM
  const θ = brg * RAD
  const φ1 = lat * RAD
  const φ2 = Math.asin(Math.sin(φ1) * Math.cos(δ) + Math.cos(φ1) * Math.sin(δ) * Math.cos(θ))
  const λ2 = lon * RAD + Math.atan2(Math.sin(θ) * Math.sin(δ) * Math.cos(φ1), Math.cos(δ) - Math.sin(φ1) * Math.sin(φ2))
  return [φ2 / RAD, ((λ2 / RAD + 540) % 360) - 180]
}

/** distanceNm is the great-circle distance in nautical miles between two points. */
export function distanceNm(lat1: number, lon1: number, lat2: number, lon2: number): number {
  const a =
    Math.sin(((lat2 - lat1) * RAD) / 2) ** 2 +
    Math.cos(lat1 * RAD) * Math.cos(lat2 * RAD) * Math.sin(((lon2 - lon1) * RAD) / 2) ** 2
  return 2 * EARTH_NM * Math.asin(Math.min(1, Math.sqrt(a)))
}

/** cellPolygon is the wedge of cell c around the grid's receiver: the outer arc, then the inner one reversed. */
export function cellPolygon(grid: CoverageGrid, c: HeatCell): [number, number][] {
  const { lat, lon } = grid.receiver
  const from = c.bucket * grid.bucketDeg
  const inner = c.ring * grid.ringNm
  const outer = inner + grid.ringNm
  const steps = [0, 0.5, 1].map((f) => from + f * grid.bucketDeg)
  const out = steps.map((b) => destination(lat, lon, b, outer))
  if (inner === 0) {
    return [...out, [lat, lon]]
  }
  return [...out, ...steps.toReversed().map((b) => destination(lat, lon, b, inner))]
}

/** cellAt returns the bucket and ring of (lat, lon) as seen from the grid's receiver. */
export function cellAt(grid: CoverageGrid, lat: number, lon: number): { bucket: number; ring: number } {
  const { receiver: r } = grid
  const buckets = Math.round(360 / grid.bucketDeg)
  return {
    bucket: Math.floor(initialBearing(r.lat, r.lon, lat, lon) / grid.bucketDeg) % buckets,
    ring: Math.floor(distanceNm(r.lat, r.lon, lat, lon) / grid.ringNm),
  }
}

const deg = (d: number) => String(Math.round(d) % 360).padStart(3, '0')

/** heatLabel describes a cell, e.g. "044–046° · 100–105 nm · avg -18.3 dBFS (-24.0…-12.1) · 312 positions". */
export function heatLabel(grid: CoverageGrid, c: HeatCell): string {
  const from = c.bucket * grid.bucketDeg
  const parts = [
    `${deg(from)}–${deg(from + grid.bucketDeg)}°`,
    `${c.ring * grid.ringNm}–${(c.ring + 1) * grid.ringNm} nm`,
  ]
  if (c.avg !== undefined && c.min !== undefined && c.max !== undefined) {
    parts.push(`avg ${c.avg.toFixed(1)} dBFS (${c.min.toFixed(1)}…${c.max.toFixed(1)})`)
  }
  parts.push(`${c.n.toLocaleString('en-US')} position${c.n === 1 ? '' : 's'}`)
  return parts.join(' · ')
}
