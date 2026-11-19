import { describe, expect, it } from 'vitest'
import type { Coverage, CoveragePoint } from '../api/types'
import { coverageAt, coverageLabel, coveragePositions, initialBearing } from './coverage'

const north: CoveragePoint = { bearing: 1.2, distanceNm: 120, lat: 27.07, lon: 22.92, alt: 35000, time: '2026-11-19T10:00:00Z' }
const east: CoveragePoint = { bearing: 91, distanceNm: 80.4, lat: 25.05, lon: 24.34, time: '2026-11-19T10:00:00Z' }
const southWest: CoveragePoint = { bearing: 225.5, distanceNm: 40, lat: 24.6, lon: 22.35, alt: 4500, time: '2026-11-19T10:00:00Z' }
const cov: Coverage = { receiver: { lat: 25.07, lon: 22.87 }, bucketDeg: 2, points: [north, east, southWest] }

describe('coverage helpers', () => {
  it('builds polygon vertices only from 3 or more points', () => {
    expect(coveragePositions(cov)).toEqual([
      [27.07, 22.92],
      [25.05, 24.34],
      [24.6, 22.35],
    ])
    expect(coveragePositions({ ...cov, points: cov.points.slice(0, 2) })).toEqual([])
    expect(coveragePositions(null)).toEqual([])
  })

  it('computes bearings', () => {
    expect(initialBearing(0, 0, 1, 0)).toBeCloseTo(0, 6)
    expect(initialBearing(0, 0, 0, 1)).toBeCloseTo(90, 6)
    expect(initialBearing(0, 0, -1, 0)).toBeCloseTo(180, 6)
    expect(initialBearing(0, 0, 0, -1)).toBeCloseTo(270, 6)
  })

  it('finds the point in the bucket under the cursor', () => {
    expect(coverageAt(cov, 26, 22.89)?.distanceNm).toBe(120) // ~1° from the receiver
    expect(coverageAt(cov, 25.04, 24)?.distanceNm).toBe(80.4) // ~91.4°, bucket 90–92
    expect(coverageAt(cov, 25.07, 24)).toBeUndefined() // same latitude is 89.8° on a great circle: bucket 88–90
    expect(coverageAt(cov, 24, 22.87)).toBeUndefined() // 180°: empty bucket
  })

  it('labels points with range, bearing and altitude', () => {
    expect(coverageLabel(north)).toBe('120 nm at 001° · FL350')
    expect(coverageLabel(east)).toBe('80 nm at 091°')
    expect(coverageLabel(southWest)).toBe('40 nm at 226° · 4,500 ft')
  })
})
