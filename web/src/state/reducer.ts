import type { Aircraft, Coverage, CoverageGrid, MapConfig, Receiver, Stats, TrackPoint } from '../api/types'

/** Trail window until the server's "trails" message arrives. */
export const DEFAULT_TRAIL_WINDOW_MS = 10 * 60 * 1000

export type ConnState = 'connecting' | 'connected' | 'reconnecting' | 'down'

export type TrailPoint = TrackPoint

export interface State {
  conn: ConnState
  receiver: Receiver | null
  map: MapConfig | null
  coverage: Coverage | null
  coverageGrid: CoverageGrid | null
  stats: Stats | null
  aircraft: ReadonlyMap<string, Aircraft> // keyed by ICAO, in server order (sorted by ICAO)
  trails: ReadonlyMap<string, readonly TrailPoint[]> // from the server's track buffer, by ICAO
  trailWindowMs: number // data time kept before each trail's newest point
  selected: string | null
  pendingSelect: string | null // aircraft to select once a snapshot contains it (from a shared link)
  panSeq: number // incremented when the map should pan to the selection (selection from the table)
}

export type Action =
  | { type: 'connection'; conn: ConnState }
  | { type: 'receiver'; receiver: Receiver }
  | { type: 'map'; map: MapConfig }
  | { type: 'coverage'; coverage: Coverage }
  | { type: 'coverageGrid'; grid: CoverageGrid }
  | { type: 'stats'; stats: Stats }
  | { type: 'trails'; windowMs: number; trails: Record<string, TrailPoint[]> }
  | { type: 'trailPoints'; points: Record<string, TrailPoint[]> }
  | { type: 'snapshot'; aircraft: Aircraft[] }
  | { type: 'select'; icao: string; pan?: boolean }
  | { type: 'clearSelection' }

export const initialState: State = {
  conn: 'connecting',
  receiver: null,
  map: null,
  coverage: null,
  coverageGrid: null,
  stats: null,
  aircraft: new Map(),
  trails: new Map(),
  trailWindowMs: DEFAULT_TRAIL_WINDOW_MS,
  selected: null,
  pendingSelect: null,
  panSeq: 0,
}

export function reducer(state: State, action: Action): State {
  switch (action.type) {
    case 'connection':
      return state.conn === action.conn ? state : { ...state, conn: action.conn }
    case 'receiver':
      return { ...state, receiver: action.receiver }
    case 'map':
      return { ...state, map: action.map }
    case 'coverage':
      return { ...state, coverage: action.coverage }
    case 'coverageGrid':
      return { ...state, coverageGrid: action.grid }
    case 'stats':
      return { ...state, stats: action.stats }
    case 'trails': {
      const trails = new Map<string, readonly TrailPoint[]>()
      for (const [icao, points] of Object.entries(action.trails)) {
        const trail = appendTrail([], points, action.windowMs)
        if (trail.length > 0) {
          trails.set(icao, trail)
        }
      }
      return { ...state, trails, trailWindowMs: action.windowMs }
    }
    case 'trailPoints':
      return applyTrailPoints(state, action.points)
    case 'snapshot':
      return applySnapshot(state, action.aircraft)
    case 'select':
      if (!state.aircraft.has(action.icao)) {
        return state
      }
      return {
        ...state,
        selected: action.icao,
        pendingSelect: null,
        panSeq: action.pan === true ? state.panSeq + 1 : state.panSeq,
      }
    case 'clearSelection':
      return state.selected === null && state.pendingSelect === null
        ? state
        : { ...state, selected: null, pendingSelect: null }
  }
}

/**
 * applySnapshot replaces the aircraft table with a full snapshot and drops trails and the selection of aircraft
 * that are gone. A pending selection is applied (with a pan when the aircraft has a position) once the aircraft is
 * in the snapshot.
 */
function applySnapshot(state: State, list: Aircraft[]): State {
  const aircraft = new Map<string, Aircraft>()
  for (const a of list) {
    aircraft.set(a.icao, a)
  }
  let trails = state.trails
  if ([...trails.keys()].some((icao) => !aircraft.has(icao))) {
    trails = new Map([...trails].filter(([icao]) => aircraft.has(icao)))
  }
  const pending = state.pendingSelect === null ? undefined : aircraft.get(state.pendingSelect)
  if (pending !== undefined) {
    return {
      ...state,
      aircraft,
      trails,
      selected: pending.icao,
      pendingSelect: null,
      panSeq: pending.hasPosition ? state.panSeq + 1 : state.panSeq,
    }
  }
  const selected = state.selected !== null && aircraft.has(state.selected) ? state.selected : null
  return { ...state, aircraft, trails, selected }
}

/** applyTrailPoints appends new server points to the trails; the state is unchanged when nothing is new. */
function applyTrailPoints(state: State, points: Record<string, TrailPoint[]>): State {
  let trails: Map<string, readonly TrailPoint[]> | undefined
  for (const [icao, pts] of Object.entries(points)) {
    const old = state.trails.get(icao) ?? []
    const trail = appendTrail(old, pts, state.trailWindowMs)
    if (trail !== old) {
      trails ??= new Map(state.trails)
      trails.set(icao, trail)
    }
  }
  return trails === undefined ? state : { ...state, trails }
}

/**
 * appendTrail appends the points newer than the trail's last one and drops points older than windowMs before the
 * newest (data time, so replays trim correctly). Returns the same array when nothing was appended.
 */
export function appendTrail(trail: readonly TrailPoint[], points: readonly TrailPoint[], windowMs: number): readonly TrailPoint[] {
  const last = trail.at(-1)?.t ?? -Infinity
  const fresh = points.filter((p) => p.t > last)
  const newest = fresh.at(-1)
  if (newest === undefined) {
    return trail
  }
  const next = [...trail, ...fresh]
  const start = next.findIndex((p) => p.t >= newest.t - windowMs)
  return start > 0 ? next.slice(start) : next
}
