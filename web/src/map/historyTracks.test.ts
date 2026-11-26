import { describe, expect, it } from 'vitest'
import type { HistoryTrack } from '../api/history'
import { nearestPoint, pointTooltip, trackBounds, trackSegments } from './historyTracks'
import { altitudeColor } from './style'

const tr: HistoryTrack = {
  id: 7,
  icao: 'AB12CD',
  t: [1000, 2000, 3000, 4000],
  lat: [25.1, 25.2, 25.3, 25.4],
  lon: [22.9, 23.0, 23.1, 23.2],
  alt: [10000, 10100, 20000, null],
  gs: [400, null, 410, 420],
  track: [45, 46, null, 47],
}

describe('stored track helpers', () => {
  it('splits by 500 ft color bucket with shared boundary points', () => {
    const segs = trackSegments(tr)
    expect(segs.map((s) => s.color)).toEqual([altitudeColor(10000), altitudeColor(20000), altitudeColor(undefined)])
    expect(segs[0]?.points).toHaveLength(2)
    expect(segs[1]?.points).toEqual([[25.2, 23.0], [25.3, 23.1]])
  })

  it('finds the nearest point within the hover radius', () => {
    const projected = [{ flight: 7, xs: new Float64Array([0, 10, 20]), ys: new Float64Array([0, 0, 0]) }]
    expect(nearestPoint(projected, 11, 2)).toEqual({ flight: 7, index: 1 })
    expect(nearestPoint(projected, 50, 50)).toBeNull()
  })

  it('bounds all points', () => {
    expect(trackBounds([tr])).toEqual([
      [25.1, 22.9],
      [25.4, 23.2],
    ])
    expect(trackBounds([{ ...tr, t: [], lat: [], lon: [], alt: [], gs: [], track: [] }])).toBeNull()
  })

  it('describes a point with its time, altitude, speed and track', () => {
    expect(pointTooltip(tr, 0)).toContain('<strong>Start</strong>')
    expect(pointTooltip(tr, 0)).toContain('10,000 ft · 400 kt · 45°')
    expect(pointTooltip(tr, 3)).toContain('End')
    expect(pointTooltip(tr, 1)).not.toContain('kt')
  })
})
