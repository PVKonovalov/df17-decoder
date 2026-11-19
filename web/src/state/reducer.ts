import type { Aircraft, Receiver, Stats } from '../api/types'

export const TRAIL_MAX_AGE_MS = 5 * 60 * 1000
export const TRAIL_MAX_POINTS = 1000

export type ConnState = 'connecting' | 'connected' | 'reconnecting' | 'down'

export interface TrailPoint {
  lat: number
  lon: number
  alt: number | undefined // ft, baro preferred
  t: number // posTime, ms since epoch
}

export interface State {
  conn: ConnState
  receiver: Receiver | null
  stats: Stats | null
  aircraft: ReadonlyMap<string, Aircraft> // keyed by ICAO, in server order (sorted by ICAO)
  trails: ReadonlyMap<string, readonly TrailPoint[]>
  selected: string | null
  panSeq: number // incremented when the map should pan to the selection (selection from the table)
}

export type Action =
  | { type: 'connection'; conn: ConnState }
  | { type: 'receiver'; receiver: Receiver }
  | { type: 'stats'; stats: Stats }
  | { type: 'snapshot'; aircraft: Aircraft[] }
  | { type: 'select'; icao: string; pan?: boolean }
  | { type: 'clearSelection' }

export const initialState: State = {
  conn: 'connecting',
  receiver: null,
  stats: null,
  aircraft: new Map(),
  trails: new Map(),
  selected: null,
  panSeq: 0,
}

export function reducer(state: State, action: Action): State {
  switch (action.type) {
    case 'connection':
      return state.conn === action.conn ? state : { ...state, conn: action.conn }
    case 'receiver':
      return { ...state, receiver: action.receiver }
    case 'stats':
      return { ...state, stats: action.stats }
    case 'snapshot':
      return applySnapshot(state, action.aircraft)
    case 'select':
      if (!state.aircraft.has(action.icao)) {
        return state
      }
      return { ...state, selected: action.icao, panSeq: action.pan === true ? state.panSeq + 1 : state.panSeq }
    case 'clearSelection':
      return state.selected === null ? state : { ...state, selected: null }
  }
}

/**
 * applySnapshot replaces the aircraft table with a full snapshot, extends trails with new positions and drops
 * trails and the selection of aircraft that are gone.
 */
function applySnapshot(state: State, list: Aircraft[]): State {
  const aircraft = new Map<string, Aircraft>()
  const trails = new Map<string, readonly TrailPoint[]>()
  for (const a of list) {
    aircraft.set(a.icao, a)
    const trail = extendTrail(state.trails.get(a.icao) ?? [], a)
    if (trail.length > 0) {
      trails.set(a.icao, trail)
    }
  }
  const selected = state.selected !== null && aircraft.has(state.selected) ? state.selected : null
  return { ...state, aircraft, trails, selected }
}

/**
 * extendTrail appends the aircraft's position if it is newer than the trail's last point and drops points
 * older than TRAIL_MAX_AGE_MS before the newest one (data time, so replays trim correctly).
 * Returns the same array when nothing changed.
 */
export function extendTrail(trail: readonly TrailPoint[], a: Aircraft): readonly TrailPoint[] {
  if (a.lat === undefined || a.lon === undefined || a.posTime === undefined) {
    return trail
  }
  const t = Date.parse(a.posTime)
  const last = trail.at(-1)
  if (Number.isNaN(t) || (last !== undefined && t <= last.t)) {
    return trail
  }
  const next = [...trail, { lat: a.lat, lon: a.lon, alt: a.altBaro ?? a.altGnss, t }]
  const cutoff = t - TRAIL_MAX_AGE_MS
  let start = next.findIndex((p) => p.t >= cutoff)
  start = Math.max(start, next.length - TRAIL_MAX_POINTS)
  return start > 0 ? next.slice(start) : next
}
