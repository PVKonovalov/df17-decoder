import { describe, expect, it } from 'vitest'
import fixture from './aircraft.fixture.json'
import mapFixture from './map.fixture.json'
import pointFixture from './trackpoint.fixture.json'
import statsFixture from './stats.fixture.json'
import historyFixture from './history.fixture.json'
import {
  aircraftHistoryKeys,
  aircraftMatchKeys,
  historyFlightKeys,
  historyTrackKeys,
  samplePointKeys,
  type AircraftHistory,
  type AircraftMatch,
  type HistoryTrack,
  type TracksResponse,
} from './history'
import {
  altitudeKeys,
  countryKeys,
  hourRowKeys,
  dailyRowKeys,
  dailyTotalsKeys,
  newAircraftKeys,
  notableEventKeys,
  notableKeys,
  operatorKeys,
  rangeDayKeys,
  storageKeys,
  type AltitudeBand,
  type CountryCount,
  type HourRow,
  type Notable,
  type OperatorCount,
  type RangeDay,
  type StorageReport,
} from './stats'
import {
  aircraftKeys,
  mapConfigKeys,
  trackPointKeys,
  waypointKeys,
  type Aircraft,
  type HistoryFlight,
  type MapConfig,
  type TrackPoint,
} from './types'

// The same fixture is checked against tracker.Aircraft by internal/api TestAircraftContract,
// so any drift between Go and TypeScript breaks one of the two tests.
describe('Aircraft contract', () => {
  it('has exactly the fixture keys', () => {
    expect(Object.keys(aircraftKeys).sort()).toEqual(Object.keys(fixture).sort())
  })

  it('accepts the fixture as an Aircraft', () => {
    const a: Aircraft = fixture
    expect(a.icao).toBe('738ABC')
    expect(a.messagesByDf['17']).toBe(100)
  })
})

// Checked against tracker.TrackPoint by internal/api TestTrackPointContract.
describe('TrackPoint contract', () => {
  it('has exactly the fixture keys', () => {
    expect(Object.keys(trackPointKeys).sort()).toEqual(Object.keys(pointFixture).sort())
  })

  it('accepts the fixture as a TrackPoint', () => {
    const p: TrackPoint = pointFixture
    expect(p.alt).toBe(12000)
  })
})

// Checked against api.MapConfig and api.Waypoint by internal/api TestMapContract.
describe('MapConfig contract', () => {
  it('has exactly the fixture keys', () => {
    expect(Object.keys(mapConfigKeys).sort()).toEqual(Object.keys(mapFixture).sort())
    expect(Object.keys(waypointKeys).sort()).toEqual(Object.keys(mapFixture.waypoints[0] ?? {}).sort())
  })

  it('accepts the fixture as a MapConfig', () => {
    const m: MapConfig = mapFixture
    expect([m.waypointLabelMinZoom, m.waypoints[0]?.latDms]).toEqual([9, '250412.00N'])
  })
})

// Checked against storage.Report and the history stats rows by internal/api TestStatsContract.
describe('Stats contract', () => {
  const nested = (o: object): string[] =>
    Object.entries(o).flatMap(([k, v]) =>
      v !== null && typeof v === 'object' ? [k, ...nested(v as object).map((n) => `${k}.${n}`)] : [k],
    )

  it('has exactly the fixture keys', () => {
    const pairs: [object, object][] = [
      [storageKeys, statsFixture.storage],
      [hourRowKeys, statsFixture.hourly],
      [countryKeys, statsFixture.country],
      [operatorKeys, statsFixture.operator],
      [rangeDayKeys, statsFixture.rangeDay],
      [altitudeKeys, statsFixture.altitude],
      [notableKeys, { ...statsFixture.notable, events: true }],
      [notableEventKeys, statsFixture.notable.events[0] ?? {}],
      [dailyTotalsKeys, { ...statsFixture.daily, days: true }],
      [dailyRowKeys, statsFixture.daily.days[0] ?? {}],
      [newAircraftKeys, statsFixture.newAircraft],
    ]
    for (const [keys, fx] of pairs) {
      expect(nested(keys).sort()).toEqual(nested(fx).sort())
    }
  })

  it('accepts the fixture as the stats types', () => {
    const s: StorageReport = statsFixture.storage
    const h: HourRow = statsFixture.hourly
    const c: CountryCount = statsFixture.country
    const o: OperatorCount = statsFixture.operator
    const d: RangeDay = statsFixture.rangeDay
    const a: AltitudeBand = statsFixture.altitude
    const n = statsFixture.notable as Notable
    expect([a.minFt, n.events[0]?.kind]).toEqual([5000, 'emergency'])
    expect([s.flags.minFreeBytes, h.positions, c.aircraftHours, o.prefix, d.day]).toEqual([
      1073741824,
      9120,
      120,
      'KLM',
      '2026-11-20',
    ])
  })
})

// Checked against the history types by internal/api TestAircraftHistoryContract.
describe('Aircraft history contract', () => {
  const nested = (o: object): string[] =>
    Object.entries(o).flatMap(([k, v]) =>
      v !== null && typeof v === 'object' && !Array.isArray(v)
        ? [k, ...nested(v as object).map((n) => `${k}.${n}`)]
        : [k],
    )

  it('has exactly the fixture keys', () => {
    const pairs: [object, object][] = [
      [aircraftMatchKeys, historyFixture.match],
      [aircraftHistoryKeys, historyFixture.aircraft],
      [historyFlightKeys, historyFixture.flight],
      [historyTrackKeys, historyFixture.track],
      [samplePointKeys, historyFixture.sample],
    ]
    for (const [keys, fx] of pairs) {
      expect(nested(keys).sort()).toEqual(nested(fx).sort())
    }
    expect(Object.keys(historyFixture.tracks)).toEqual(['tracks'])
  })

  it('accepts the fixture as the history types', () => {
    const m: AircraftMatch = historyFixture.match
    const a: AircraftHistory = historyFixture.aircraft
    const f: HistoryFlight = historyFixture.flight
    const tr: HistoryTrack = historyFixture.track
    const r: TracksResponse = { tracks: [tr] }
    expect([m.flights, a.quality?.lost, f.durationS, r.tracks[0]?.alt[1]]).toEqual([2, 1, 600, null])
  })
})
