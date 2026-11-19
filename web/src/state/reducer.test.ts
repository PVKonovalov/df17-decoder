import { describe, expect, it } from 'vitest'
import type { Aircraft } from '../api/types'
import { extendTrail, initialState, reducer, TRAIL_MAX_AGE_MS, type State } from './reducer'

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
  ac(icao, { lat, lon: 34.9, posTime: iso(ms), ...(altBaro !== undefined && { altBaro }) })

const snap = (state: State, ...aircraft: Aircraft[]) => reducer(state, { type: 'snapshot', aircraft })

describe('snapshot', () => {
  it('adds, updates and removes aircraft', () => {
    let s = snap(initialState, ac('738001'), ac('738002', { callsign: 'ELY1' }))
    expect([...s.aircraft.keys()]).toEqual(['738001', '738002'])

    s = snap(s, ac('738002', { callsign: 'ELY2' }), ac('738003'))
    expect([...s.aircraft.keys()]).toEqual(['738002', '738003'])
    expect(s.aircraft.get('738002')?.callsign).toBe('ELY2')
  })

  it('keeps a real altitude of 0', () => {
    const s = snap(initialState, at('738001', T0, 32, 0))
    expect(s.trails.get('738001')?.[0]?.alt).toBe(0)
  })

  it('builds trails from new positions only and drops trails of removed aircraft', () => {
    let s = snap(initialState, at('738001', T0, 32.0, 1000), ac('738002'))
    s = snap(s, at('738001', T0, 32.0, 1000)) // same posTime: no new point
    s = snap(s, at('738001', T0 + 1000, 32.1, 1100))
    expect(s.trails.get('738001')?.map((p) => p.lat)).toEqual([32.0, 32.1])
    expect(s.trails.has('738002')).toBe(false)

    s = snap(s, ac('738002'))
    expect(s.trails.has('738001')).toBe(false)
  })
})

describe('extendTrail', () => {
  it('trims points older than the max age before the newest point', () => {
    let trail = extendTrail([], at('a', T0, 1))
    trail = extendTrail(trail, at('a', T0 + 60_000, 2))
    trail = extendTrail(trail, at('a', T0 + TRAIL_MAX_AGE_MS + 30_000, 3))
    expect(trail.map((p) => p.lat)).toEqual([2, 3])
  })

  it('ignores aircraft without position and out-of-order positions', () => {
    const trail = extendTrail([], at('a', T0, 1))
    expect(extendTrail(trail, ac('a'))).toBe(trail)
    expect(extendTrail(trail, at('a', T0 - 1000, 2))).toBe(trail)
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

  it('ignores unknown aircraft and clears explicitly', () => {
    let s = snap(initialState, ac('738001'))
    expect(reducer(s, { type: 'select', icao: 'FFFFFF' })).toBe(s)
    s = reducer(s, { type: 'select', icao: '738001' })
    expect(reducer(s, { type: 'clearSelection' }).selected).toBeNull()
  })
})

describe('connection, receiver, stats', () => {
  it('stores the latest values', () => {
    let s = reducer(initialState, { type: 'connection', conn: 'connected' })
    s = reducer(s, { type: 'receiver', receiver: { lat: 32, lon: 35, maxRangeNm: 300 } })
    expect(s.conn).toBe('connected')
    expect(s.receiver?.maxRangeNm).toBe(300)
    expect(reducer(s, { type: 'connection', conn: 'connected' })).toBe(s)
  })
})
