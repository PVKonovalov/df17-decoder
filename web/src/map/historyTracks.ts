import type { HistoryTrack } from '../api/history'
import { escapeHtml } from './waypoints'
import { altitudeColor } from './style'

export const TRACK_HOVER_PX = 8

export interface TrackSegment {
  color: string
  points: [number, number][]
}

/**
 * trackSegments splits a stored track into polylines of one altitude color (500 ft buckets, as live trails);
 * consecutive segments share their boundary point.
 */
export function trackSegments(tr: HistoryTrack): TrackSegment[] {
  const out: TrackSegment[] = []
  let cur: TrackSegment | undefined
  let curBucket: number | null = NaN
  for (let i = 0; i < tr.t.length; i++) {
    const alt = tr.alt[i] ?? null
    const bucket = alt === null ? null : Math.round(alt / 500)
    const ll: [number, number] = [tr.lat[i] ?? 0, tr.lon[i] ?? 0]
    if (cur === undefined || bucket !== curBucket) {
      const prev = cur?.points.at(-1)
      cur = { color: altitudeColor(bucket === null ? undefined : bucket * 500), points: prev ? [prev, ll] : [ll] }
      curBucket = bucket
      out.push(cur)
    } else {
      cur.points.push(ll)
    }
  }
  return out.filter((s) => s.points.length > 1)
}

/** TrackHit is one point of a shown track. */
export interface TrackHit {
  flight: number
  index: number
}

/** nearestPoint returns the point within maxPx of (x, y) closest to it, from per-track projected points. */
export function nearestPoint(
  projected: readonly { flight: number; xs: Float64Array; ys: Float64Array }[],
  x: number,
  y: number,
  maxPx = TRACK_HOVER_PX,
): TrackHit | null {
  let best: TrackHit | null = null
  let bestD = maxPx * maxPx
  for (const p of projected) {
    for (let i = 0; i < p.xs.length; i++) {
      const dx = (p.xs[i] ?? 0) - x
      const dy = (p.ys[i] ?? 0) - y
      const d = dx * dx + dy * dy
      if (d <= bestD) {
        bestD = d
        best = { flight: p.flight, index: i }
      }
    }
  }
  return best
}

/** trackBounds returns [[south, west], [north, east]] of the tracks, or null when they have no points. */
export function trackBounds(tracks: readonly HistoryTrack[]): [[number, number], [number, number]] | null {
  let s = Infinity
  let w = Infinity
  let n = -Infinity
  let e = -Infinity
  for (const tr of tracks) {
    for (let i = 0; i < tr.t.length; i++) {
      const lat = tr.lat[i] ?? 0
      const lon = tr.lon[i] ?? 0
      s = Math.min(s, lat)
      n = Math.max(n, lat)
      w = Math.min(w, lon)
      e = Math.max(e, lon)
    }
  }
  return s === Infinity ? null : [
    [s, w],
    [n, e],
  ]
}

/** pointTooltip describes a track point: its time, altitude, ground speed and track; Start/End on the ends. */
export function pointTooltip(tr: HistoryTrack, i: number, label?: string): string {
  const end = i === 0 ? 'Start' : i === tr.t.length - 1 ? 'End' : ''
  const alt = tr.alt[i]
  const gs = tr.gs[i]
  const trk = tr.track[i]
  const parts = [
    alt === null || alt === undefined ? '' : `${alt.toLocaleString('en-US')} ft`,
    gs === null || gs === undefined ? '' : `${gs.toFixed(0)} kt`,
    trk === null || trk === undefined ? '' : `${trk.toFixed(0)}°`,
  ].filter((p) => p !== '')
  const head = [label, end].filter((v) => v !== undefined && v !== '').join(' · ')
  return [
    head === '' ? '' : `<strong>${escapeHtml(head)}</strong>`,
    new Date(tr.t[i] ?? 0).toLocaleString(),
    parts.join(' · '),
  ]
    .filter((v) => v !== '')
    .join('<br>')
}
