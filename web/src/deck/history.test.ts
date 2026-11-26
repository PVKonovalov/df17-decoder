import { describe, expect, it } from 'vitest'
import { densityPaths, flightSeek, timelineQuery } from './history'

const H = 3_600_000

describe('timelineQuery', () => {
  it('pads, snaps and keeps the query while the window moves less than a quarter', () => {
    const a = timelineQuery(10 * H, 11 * H)
    expect(a).toEqual({ from: 9.75 * H, to: 11.25 * H, bucketMs: 60_000 })
    expect(timelineQuery(10 * H + 60_000, 11 * H + 60_000)).toEqual({ from: 9.75 * H, to: 11.5 * H, bucketMs: 60_000 })
    expect(timelineQuery(10 * H + 1, 11 * H - 1)).toEqual(a)
    expect(timelineQuery(0, 14 * 24 * H).bucketMs % 60_000).toBe(0)
    expect((timelineQuery(0, 14 * 24 * H).to - timelineQuery(0, 14 * 24 * H).from) / timelineQuery(0, 14 * 24 * H).bucketMs).toBeLessThanOrEqual(400)
  })
})

describe('densityPaths', () => {
  it('draws steps scaled to the busiest bucket in the window', () => {
    const tl = { from: new Date(0).toISOString(), bucket: '1m0s', buckets: [[1, 1], [4, 0], [9, 9]] as [number, number][], events: [] }
    const p = densityPaths(tl, 60_000, 0, 120_000, 10)
    expect(p.peak).toBe(4)
    expect(p.all).toBe('M0.0,10L0.0,5.0L500.0,5.0L500.0,0.0L1000.0,0.0L1000.0,10Z')
    expect(p.adsb).toContain('L0.0,7.5')
    expect(densityPaths({ ...tl, buckets: [] }, 60_000, 0, 1, 10).all).toBe('')
  })
})

describe('flightSeek', () => {
  it('rounds a tape start with milliseconds up, since seeks are in whole seconds', () => {
    expect(flightSeek('2026-11-20T10:00:10Z', '2026-11-20T10:30:00Z', '2026-11-20T10:00:00.884Z')).toBe('2026-11-20T10:00:01.000Z')
  })

  it('seeks 30 s before the flight, within the tape', () => {
    expect(flightSeek('2026-11-20T10:00:00Z', '2026-11-20T10:30:00Z', '2026-11-19T00:00:00Z')).toBe('2026-11-20T09:59:30.000Z')
    expect(flightSeek('2026-11-20T10:00:10Z', '2026-11-20T10:30:00Z', '2026-11-20T10:00:00Z')).toBe('2026-11-20T10:00:00.000Z')
    expect(flightSeek('2026-11-01T10:00:00Z', '2026-11-01T11:00:00Z', '2026-11-06T00:00:00Z')).toBeNull()
  })
})
