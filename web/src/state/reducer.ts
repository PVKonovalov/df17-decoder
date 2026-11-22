import type { Aircraft, Coverage, CoverageGrid, MapConfig, Playback, Receiver, Stats, TrackPoint } from '../api/types'

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
  editing: string | null // the aircraft whose reference data is being edited; its selection outlives it
  held: Aircraft | null // last known data of the edited aircraft after it left the snapshots
  pendingSelect: string | null // aircraft to select once a snapshot contains it (from a shared link)
  pendingAfterSeek: boolean // pendingSelect waits for the snapshots after a seek (the next full trails message)
  panSeq: number // incremented when the map should pan to the selection (selection from the table)
  playback: Playback | null // tape counter state; null when the server has no playback
  playbackError: string | null // the last error the server returned for a playback command
}

export type Action =
  | { type: 'connection'; conn: ConnState }
  | { type: 'receiver'; receiver: Receiver }
  | { type: 'map'; map: MapConfig }
  | { type: 'coverage'; coverage: Coverage }
  | { type: 'coverageGrid'; grid: CoverageGrid }
  | { type: 'stats'; stats: Stats }
  | { type: 'playback'; playback: Playback }
  | { type: 'playbackError'; message: string }
  | { type: 'clearPlaybackError' }
  | { type: 'trails'; windowMs: number; trails: Record<string, TrailPoint[]> }
  | { type: 'trailPoints'; points: Record<string, TrailPoint[]> }
  | { type: 'snapshot'; aircraft: Aircraft[] }
  | { type: 'select'; icao: string; pan?: boolean }
  | { type: 'clearSelection' }
  | { type: 'selectAfterSeek'; icao: string }
  | { type: 'editing'; icao: string | null }

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
  editing: null,
  held: null,
  pendingSelect: null,
  pendingAfterSeek: false,
  panSeq: 0,
  playback: null,
  playbackError: null,
}

export function reducer(state: State, action: Action): State {
  switch (action.type) {
    case 'connection':
      // a dropped connection ends its playback session: the server starts the next one live
      return state.conn === action.conn
        ? state
        : { ...state, conn: action.conn, playback: action.conn === 'connected' ? state.playback : null }
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
    case 'playback':
      return { ...state, playback: action.playback }
    case 'playbackError':
      return { ...state, playbackError: action.message }
    case 'clearPlaybackError':
      return state.playbackError === null ? state : { ...state, playbackError: null }
    case 'trails': {
      const trails = new Map<string, readonly TrailPoint[]>()
      for (const [icao, points] of Object.entries(action.trails)) {
        const trail = appendTrail([], points, action.windowMs)
        if (trail.length > 0) {
          trails.set(icao, trail)
        }
      }
      // a full trails message follows every seek: a selection waiting for it may now apply
      return { ...state, trails, trailWindowMs: action.windowMs, pendingAfterSeek: false }
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
        ...(action.icao !== state.selected && { editing: null, held: null }),
        pendingSelect: null,
        pendingAfterSeek: false,
        panSeq: action.pan === true ? state.panSeq + 1 : state.panSeq,
      }
    case 'clearSelection':
      return state.selected === null && state.pendingSelect === null
        ? state
        : { ...state, selected: null, editing: null, held: null, pendingSelect: null, pendingAfterSeek: false }
    case 'selectAfterSeek':
      return {
        ...state,
        selected: null,
        editing: null,
        held: null,
        pendingSelect: action.icao,
        pendingAfterSeek: true,
      }
    case 'editing':
      if (action.icao !== null) {
        return action.icao === state.selected ? { ...state, editing: action.icao } : state
      }
      // the edit ends: an aircraft that is no longer received closes with it
      return state.held === null
        ? { ...state, editing: null }
        : { ...state, editing: null, held: null, selected: null }
  }
}

/**
 * applySnapshot replaces the aircraft table with a full snapshot and drops trails and the selection of aircraft
 * that are gone, except an aircraft being edited: its selection stays, with its last known data in held. A pending
 * selection is applied (with a pan when the aircraft has a position) once the aircraft is
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
  const pending =
    state.pendingSelect === null || state.pendingAfterSeek ? undefined : aircraft.get(state.pendingSelect)
  if (pending !== undefined) {
    return {
      ...state,
      aircraft,
      trails,
      selected: pending.icao,
      editing: null,
      held: null,
      pendingSelect: null,
      panSeq: pending.hasPosition ? state.panSeq + 1 : state.panSeq,
    }
  }
  if (state.selected === null || aircraft.has(state.selected)) {
    return { ...state, aircraft, trails, held: null }
  }
  if (state.editing === state.selected) {
    const held = state.aircraft.get(state.selected) ?? state.held
    return { ...state, aircraft, trails, held }
  }
  return { ...state, aircraft, trails, selected: null, editing: null, held: null }
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
