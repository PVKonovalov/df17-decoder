import { describe, expect, it } from 'vitest'
import fixture from './aircraft.fixture.json'
import pointFixture from './trackpoint.fixture.json'
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
