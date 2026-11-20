import { describe, expect, it } from 'vitest'
import type { Aircraft } from '../api/types'
import { appendTrail, DEFAULT_TRAIL_WINDOW_MS, initialState, reducer, type State, type TrailPoint } from './reducer'

const T0 = Date.parse('2026-11-19T10:00:00Z')
const iso = (ms: number) => new Date(ms).toISOString()

function ac(icao: string, extra: Partial<Aircraft> = {}): Aircraft {
  return {
    icao,
    firstSeen: iso(T0),
    lastSeen: iso(T0),
    messages: 1,
    messagesByDf: { '17': 1 },
    hasPosition: extra.lat !== undefined,
    adsb: true,
    ...extra,
  }
}

const at = (icao: string, ms: number, lat: number, altBaro?: number) =>
  ac(icao, { lat, lon: 22.9, posTime: iso(ms), ...(altBaro !== undefined && { altBaro }) })

const snap = (state: State, ...aircraft: Aircraft[]) => reducer(state, { type: 'snapshot', aircraft })

describe('snapshot', () => {
  it('adds, updates and removes aircraft', () => {
    let s = snap(initialState, ac('738001'), ac('738002', { callsign: 'ELY1' }))
    expect([...s.aircraft.keys()]).toEqual(['738001', '738002'])

    s = snap(s, ac('738002', { callsign: 'ELY2' }), ac('738003'))
    expect([...s.aircraft.keys()]).toEqual(['738002', '738003'])
    expect(s.aircraft.get('738002')?.callsign).toBe('ELY2')
  })

})

const pt = (ms: number, lat: number, alt?: number): TrailPoint => ({ t: ms, lat, lon: 22.9, ...(alt !== undefined && { alt }) })
const lats = (s: State, icao: string) => s.trails.get(icao)?.map((p) => p.lat)

describe('trails', () => {
  it('starts from the server trails and its window', () => {
    let s = reducer(initialState, { type: 'trailPoints', points: { '738009': [pt(T0, 9)] } })
    s = reducer(s, { type: 'trails', windowMs: 60_000, trails: { '738001': [pt(T0, 1, 0), pt(T0 + 1000, 2)], '738002': [] } })
    expect(s.trailWindowMs).toBe(60_000)
    expect([...s.trails.keys()]).toEqual(['738001'])
    expect(s.trails.get('738001')?.[0]?.alt).toBe(0)
  })

  it('appends only new points and trims to the server window', () => {
    let s = reducer(initialState, { type: 'trails', windowMs: 60_000, trails: { '738001': [pt(T0, 1)] } })
    s = reducer(s, { type: 'trailPoints', points: { '738001': [pt(T0, 1), pt(T0 + 30_000, 2)], '738002': [pt(T0, 5)] } })
    expect(lats(s, '738001')).toEqual([1, 2])
    expect(lats(s, '738002')).toEqual([5])

    s = reducer(s, { type: 'trailPoints', points: { '738001': [pt(T0 + 70_000, 3)] } })
    expect(lats(s, '738001')).toEqual([2, 3])
  })

  it('keeps the state when no point is new', () => {
    const s = reducer(initialState, { type: 'trails', windowMs: 60_000, trails: { '738001': [pt(T0, 1)] } })
    expect(reducer(s, { type: 'trailPoints', points: {} })).toBe(s)
    expect(reducer(s, { type: 'trailPoints', points: { '738001': [pt(T0, 1)] } })).toBe(s)
  })

  it('drops trails of aircraft gone from the snapshot', () => {
    let s = reducer(initialState, { type: 'trails', windowMs: 60_000, trails: { '738001': [pt(T0, 1)], '738002': [pt(T0, 2)] } })
    s = snap(s, ac('738001'), ac('738002'))
    const kept = s.trails
    s = snap(s, ac('738001'), ac('738002'))
    expect(s.trails).toBe(kept)
    s = snap(s, ac('738002'))
    expect([...s.trails.keys()]).toEqual(['738002'])
  })
})

describe('appendTrail', () => {
  it('uses the default window before the server sends one', () => {
    expect(initialState.trailWindowMs).toBe(DEFAULT_TRAIL_WINDOW_MS)
    const trail = appendTrail([pt(T0, 1)], [pt(T0 + DEFAULT_TRAIL_WINDOW_MS + 1, 2)], DEFAULT_TRAIL_WINDOW_MS)
    expect(trail.map((p) => p.lat)).toEqual([2])
  })

  it('ignores out-of-order points', () => {
    const trail = appendTrail([], [pt(T0, 1)], 60_000)
    expect(appendTrail(trail, [pt(T0 - 1000, 2)], 60_000)).toBe(trail)
  })
})

describe('selection', () => {
  it('survives snapshots and is cleared when the aircraft disappears', () => {
    let s = snap(initialState, ac('738001'), ac('738002'))
    s = reducer(s, { type: 'select', icao: '738002' })
    expect(s.selected).toBe('738002')

    s = snap(s, ac('738001'), ac('738002'))
    expect(s.selected).toBe('738002')

    s = snap(s, ac('738001'))
    expect(s.selected).toBeNull()
  })

  it('requests a pan only for selections that ask for it', () => {
    let s = snap(initialState, ac('738001'))
    s = reducer(s, { type: 'select', icao: '738001' })
    expect(s.panSeq).toBe(0)
    s = reducer(s, { type: 'select', icao: '738001', pan: true })
    expect(s.panSeq).toBe(1)
  })

  it('ignores unknown aircraft and clears explicitly', () => {
    let s = snap(initialState, ac('738001'))
    expect(reducer(s, { type: 'select', icao: 'FFFFFF' })).toBe(s)
    s = reducer(s, { type: 'select', icao: '738001' })
    expect(reducer(s, { type: 'clearSelection' }).selected).toBeNull()
  })
})

describe('pending selection (shared link)', () => {
  it('selects the aircraft and pans once a snapshot contains it', () => {
    let s: State = { ...initialState, pendingSelect: '738002' }
    s = snap(s, ac('738001'))
    expect([s.selected, s.pendingSelect, s.panSeq]).toEqual([null, '738002', 0])
    s = snap(s, ac('738001'), at('738002', T0, 25))
    expect([s.selected, s.pendingSelect, s.panSeq]).toEqual(['738002', null, 1])
  })

  it('selects without a pan when the aircraft has no position', () => {
    const s = snap({ ...initialState, pendingSelect: '738001' }, ac('738001'))
    expect([s.selected, s.panSeq]).toEqual(['738001', 0])
  })

  it('is cancelled by a manual selection or clearing', () => {
    let s = snap({ ...initialState, pendingSelect: '738002' }, ac('738001'))
    s = reducer(s, { type: 'select', icao: '738001' })
    expect(snap(s, ac('738001'), ac('738002')).selected).toBe('738001')

    s = reducer({ ...initialState, pendingSelect: '738002' }, { type: 'clearSelection' })
    expect(s.pendingSelect).toBeNull()
    expect(snap(s, ac('738002')).selected).toBeNull()
  })
})

describe('connection, receiver, stats', () => {
  it('stores the latest values', () => {
    let s = reducer(initialState, { type: 'connection', conn: 'connected' })
    s = reducer(s, { type: 'receiver', receiver: { lat: 25, lon: 23, maxRangeNm: 300 } })
    s = reducer(s, { type: 'map', map: { tileUrl: 'http://t/{z}/{x}/{y}', attribution: '', maxZoom: 18, darkFilter: true } })
    expect(s.map?.maxZoom).toBe(18)
    s = reducer(s, { type: 'coverage', coverage: { receiver: { lat: 25, lon: 23 }, bucketDeg: 2, points: [] } })
    expect(s.coverage?.bucketDeg).toBe(2)
    s = reducer(s, { type: 'coverageGrid', grid: { receiver: { lat: 25, lon: 23 }, bucketDeg: 2, ringNm: 5, bands: [] } })
    expect(s.coverageGrid?.ringNm).toBe(5)
    expect(s.conn).toBe('connected')
    expect(s.receiver?.maxRangeNm).toBe(300)
    expect(reducer(s, { type: 'connection', conn: 'connected' })).toBe(s)
  })
})

describe('playback', () => {
  const live = { mode: 'live', t: iso(T0), speed: 1, playing: true } as const

  it('keeps the latest status and error', () => {
    let s = reducer({ ...initialState, conn: 'connected' }, { type: 'playback', playback: live })
    expect(s.playback).toEqual(live)
    const paused = { ...live, mode: 'playback', playing: false } as const
    s = reducer(s, { type: 'playback', playback: paused })
    expect(s.playback).toEqual(paused)
    s = reducer(s, { type: 'playbackError', message: 'too many playback sessions' })
    expect(s.playbackError).toBe('too many playback sessions')
  })

  it('forgets the status when the connection drops', () => {
    let s = reducer({ ...initialState, conn: 'connected' }, { type: 'playback', playback: live })
    s = reducer(s, { type: 'connection', conn: 'reconnecting' })
    expect(s.playback).toBeNull()
  })
})

describe('selectAfterSeek', () => {
  it('waits for the trails of the seek before selecting', () => {
    let s = reducer(initialState, { type: 'snapshot', aircraft: [ac('4840D6')] })
    s = reducer(s, { type: 'selectAfterSeek', icao: '4840D6' })
    s = reducer(s, { type: 'snapshot', aircraft: [ac('4840D6')] }) // live, before the seek took effect
    expect(s.selected).toBeNull()
    s = reducer(s, { type: 'trails', windowMs: DEFAULT_TRAIL_WINDOW_MS, trails: {} })
    s = reducer(s, { type: 'snapshot', aircraft: [] })
    expect(s.pendingSelect).toBe('4840D6')
    s = reducer(s, { type: 'snapshot', aircraft: [ac('4840D6', { lat: 1, lon: 2 })] })
    expect(s.selected).toBe('4840D6')
    expect(s.panSeq).toBe(1)
  })
})
