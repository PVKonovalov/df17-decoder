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

  it('keeps an aircraft being edited selected after it disappears, with its last data', () => {
    let s = snap(initialState, ac('738001'), ac('738002', { callsign: 'ELY1' }))
    s = reducer(s, { type: 'select', icao: '738002' })
    expect(reducer(s, { type: 'editing', icao: '738001' }).editing).toBeNull()
    s = reducer(s, { type: 'editing', icao: '738002' })

    s = snap(s, ac('738001'))
    expect([s.selected, s.editing, s.held?.callsign]).toEqual(['738002', '738002', 'ELY1'])
    s = snap(s, ac('738001'))
    expect(s.held?.callsign).toBe('ELY1')

    const back = snap(s, ac('738001'), ac('738002', { callsign: 'ELY2' }))
    expect([back.selected, back.held]).toEqual(['738002', null])
    expect(reducer(back, { type: 'editing', icao: null }).selected).toBe('738002')

    s = reducer(s, { type: 'editing', icao: null })
    expect([s.selected, s.editing, s.held]).toEqual([null, null, null])
  })

  it('ends editing when another aircraft is selected or the selection is cleared', () => {
    let s = snap(initialState, ac('738001'), ac('738002'))
    s = reducer(reducer(s, { type: 'select', icao: '738002' }), { type: 'editing', icao: '738002' })
    expect(reducer(s, { type: 'select', icao: '738002', pan: true }).editing).toBe('738002')
    expect(reducer(s, { type: 'select', icao: '738001' }).editing).toBeNull()
    expect(reducer(s, { type: 'clearSelection' }).editing).toBeNull()
    s = snap(reducer(s, { type: 'clearSelection' }), ac('738001'))
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
    s = reducer(s, {
      type: 'map',
      map: {
        tileUrl: 'http://t/{z}/{x}/{y}',
        attribution: '',
        maxZoom: 18,
        darkFilter: true,
        waypointLabelMinZoom: 9,
        waypoints: [],
      },
    })
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

describe('history-only selection', () => {
  const withMap = snap(initialState, at('738001', T0, 25.1), ac('738002'))

  it('selects an aircraft on the map, panning only when it has a position', () => {
    const s = reducer(withMap, { type: 'showHistory', icao: '738001' })
    expect([s.selected, s.historyIcao, s.panSeq]).toEqual(['738001', null, withMap.panSeq + 1])
    const n = reducer(withMap, { type: 'showHistory', icao: '738002' })
    expect([n.selected, n.panSeq]).toEqual(['738002', withMap.panSeq])
  })

  it('opens the history of an aircraft not on the map', () => {
    const sel = reducer(withMap, { type: 'select', icao: '738001' })
    const s = reducer(sel, { type: 'showHistory', icao: 'AB12CD' })
    expect([s.selected, s.historyIcao]).toEqual([null, 'AB12CD'])
    expect(reducer(s, { type: 'clearSelection' }).historyIcao).toBeNull()
    expect(reducer(s, { type: 'select', icao: '738002' })).toMatchObject({ selected: '738002', historyIcao: null })
  })

  it('becomes the selection when the aircraft appears', () => {
    const s = reducer(withMap, { type: 'showHistory', icao: 'AB12CD' })
    expect(snap(s, ac('738001')).historyIcao).toBe('AB12CD')
    const back = snap(s, ac('AB12CD'))
    expect([back.selected, back.historyIcao]).toEqual(['AB12CD', null])
  })
})

describe('history tracks', () => {
  const base = snap(initialState, at('738001', T0, 25.1), ac('738002'))
  const sel = reducer(base, { type: 'select', icao: '738001' })
  const show = (s: State, icao: string, ids: number[]) => reducer(s, { type: 'historyTracks', icao, ids })

  it('keeps at most 20 distinct tracks', () => {
    const ids = Array.from({ length: 25 }, (_, i) => i + 1)
    const s = show(sel, '738001', [3, 3, ...ids])
    expect(s.historyTracks).toHaveLength(20)
    expect(s.historyTracks.slice(0, 3)).toEqual([3, 1, 2])
    expect(s.historyFor).toBe('738001')
  })

  it('fits once per aircraft, and again on request', () => {
    const a = show(sel, '738001', [1])
    expect(a.fitSeq).toBe(sel.fitSeq + 1)
    const b = show(show(a, '738001', []), '738001', [1, 2])
    expect(b.fitSeq).toBe(a.fitSeq)
    expect(reducer(b, { type: 'fitTracks' }).fitSeq).toBe(b.fitSeq + 1)
    expect(reducer(sel, { type: 'fitTracks' })).toBe(sel)
  })

  it('replaces them when another aircraft is selected', () => {
    const s = show(sel, '738001', [1, 2])
    expect(reducer(s, { type: 'select', icao: '738001' }).historyTracks).toEqual([1, 2])
    expect(reducer(s, { type: 'select', icao: '738002' }).historyTracks).toEqual([])
    expect(reducer(s, { type: 'showHistory', icao: 'AB12CD' }).historyTracks).toEqual([])
    const other = reducer(s, { type: 'select', icao: '738002' })
    expect(show(other, '738002', [9]).fitSeq).toBe(other.fitSeq + 1)
  })

  it('keeps them when the panel closes, until cleared or replaced', () => {
    const s = reducer(reducer(show(sel, '738001', [1, 2]), { type: 'historyProfile', icao: '738001', id: 1 }), {
      type: 'clearSelection',
    })
    expect([s.selected, s.historyFor, s.historyTracks, s.historyProfile]).toEqual([null, '738001', [1, 2], null])
    expect(reducer(s, { type: 'clearSelection' })).toBe(s)
    const back = reducer(s, { type: 'select', icao: '738001' })
    expect([back.selected, back.historyTracks, back.fitSeq]).toEqual(['738001', [1, 2], s.fitSeq])
    expect(reducer(s, { type: 'select', icao: '738002' }).historyTracks).toEqual([])
    expect(reducer(s, { type: 'clearTracks' })).toMatchObject({ historyTracks: [], historyFor: null, historyProfile: null })
    expect(reducer(sel, { type: 'clearTracks' })).toBe(sel)
  })

  it('keeps a gone aircraft with tracks open from its history', () => {
    const s = show(sel, '738001', [1])
    const gone = snap(s, ac('738002'))
    expect([gone.selected, gone.historyIcao, gone.historyTracks]).toEqual([null, '738001', [1]])
    expect(snap(reducer(base, { type: 'select', icao: '738001' }), ac('738002')).historyIcao).toBeNull()
  })

  it('keeps the tracks and the history panel while Play waits for the aircraft', () => {
    const s = reducer(show(sel, '738001', [1]), { type: 'selectAfterSeek', icao: '738001' })
    expect([s.selected, s.historyIcao, s.pendingSelect, s.historyTracks]).toEqual([null, '738001', '738001', [1]])
    const after = snap(reducer(s, { type: 'trails', windowMs: 1, trails: {} }), at('738001', T0, 25.1))
    expect([after.selected, after.historyIcao, after.historyTracks]).toEqual(['738001', null, [1]])
  })
})

describe('flight profile', () => {
  const sel = reducer(snap(initialState, at('738001', T0, 25.1)), { type: 'select', icao: '738001' })

  it('opens with its track shown and closes keeping the tracks', () => {
    const s = reducer(sel, { type: 'historyProfile', icao: '738001', id: 5 })
    expect([s.historyProfile, s.historyFor, s.historyTracks, s.fitSeq]).toEqual([5, '738001', [5], sel.fitSeq + 1])
    const closed = reducer(s, { type: 'historyProfile', icao: '738001', id: null })
    expect([closed.historyProfile, closed.historyTracks]).toEqual([null, [5]])
    const panelClosed = reducer(s, { type: 'clearSelection' })
    expect([panelClosed.historyProfile, panelClosed.historyFor, panelClosed.historyTracks]).toEqual([null, '738001', [5]])
    const cleared = reducer(reducer(s, { type: 'historyTracks', icao: '738001', ids: [] }), { type: 'clearSelection' })
    expect([cleared.historyProfile, cleared.historyFor]).toEqual([null, null])
  })

  it('does not add a track at the limit and goes with another selection', () => {
    const ids = Array.from({ length: 20 }, (_, i) => i + 1)
    const full = reducer(sel, { type: 'historyTracks', icao: '738001', ids })
    const s = reducer(full, { type: 'historyProfile', icao: '738001', id: 99 })
    expect([s.historyProfile, s.historyTracks]).toEqual([99, ids])
    expect(reducer(s, { type: 'showHistory', icao: 'AB12CD' }).historyProfile).toBeNull()
  })
})
