import { describe, expect, it } from 'vitest'
import fixture from './aircraft.fixture.json'
import { aircraftKeys, type Aircraft } from './types'

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
