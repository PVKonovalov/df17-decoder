import { describe, expect, it } from 'vitest'
import fixture from './aircraft.fixture.json'
import pointFixture from './trackpoint.fixture.json'
import statsFixture from './stats.fixture.json'
import {
  altitudeKeys,
  countryKeys,
  hourRowKeys,
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
import { aircraftKeys, trackPointKeys, type Aircraft, type TrackPoint } from './types'

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
