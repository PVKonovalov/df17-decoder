import { describe, expect, it } from 'vitest'
import type { Aircraft } from '../api/types'
import type { TrailPoint } from '../state/reducer'
import {
  altitudeColor,
  altitudeHue,
  dataNow,
  GROUND_COLOR,
  isStale,
  markerLabel,
  NO_ALT_COLOR,
  rotation,
  trailSegments,
} from './style'

const ac = (extra: Partial<Aircraft> = {}): Aircraft => ({
  icao: '738ABC',
  firstSeen: '2026-11-19T10:00:00Z',
  lastSeen: '2026-11-19T10:00:00Z',
  messages: 1,
  messagesByDf: {},
  hasPosition: false,
  adsb: false,
  ...extra,
})

describe('altitude color', () => {
  it('hits the stops and clamps', () => {
    expect(altitudeHue(0)).toBe(20)
    expect(altitudeHue(10_000)).toBe(120)
    expect(altitudeHue(40_000)).toBe(300)
    expect(altitudeHue(-500)).toBe(20)
    expect(altitudeHue(60_000)).toBe(300)
    expect(altitudeHue(7_000)).toBeCloseTo(85)
  })

  it('increases monotonically', () => {
    let prev = -1
    for (let ft = 0; ft <= 40_000; ft += 250) {
      const h = altitudeHue(ft)
      expect(h).toBeGreaterThanOrEqual(prev)
      prev = h
    }
  })

  it('handles 0 ft, missing altitude and ground', () => {
    expect(altitudeColor(0)).toBe('hsl(20 85% 45%)')
    expect(altitudeColor(undefined)).toBe(NO_ALT_COLOR)
    expect(altitudeColor(5000, true)).toBe(GROUND_COLOR)
  })
})

describe('markerLabel', () => {
  it('formats name and altitude in hundreds of feet', () => {
    expect(markerLabel(ac({ callsign: 'WZZ314', altBaro: 4000 }))).toBe('WZZ314 040')
    expect(markerLabel(ac({ altBaro: 38000 }))).toBe('738ABC 380')
    expect(markerLabel(ac({ altBaro: 0 }))).toBe('738ABC 000')
    expect(markerLabel(ac({ altGnss: 1250 }))).toBe('738ABC 013')
    expect(markerLabel(ac({ altBaro: -300 }))).toBe('738ABC -3')
    expect(markerLabel(ac({ callsign: 'ELY1', onGround: true, altBaro: 0 }))).toBe('ELY1 GND')
    expect(markerLabel(ac())).toBe('738ABC')
  })
})

describe('staleness and rotation', () => {
  it('uses data time', () => {
    const list = [ac({ lastSeen: '2026-11-19T10:00:40Z' }), ac({ lastSeen: '2026-11-19T10:00:10Z' })]
    const now = dataNow(list)
    expect(now).toBe(Date.parse('2026-11-19T10:00:40Z'))
    expect(isStale(ac({ posTime: '2026-11-19T10:00:10Z' }), now)).toBe(false)
    expect(isStale(ac({ posTime: '2026-11-19T10:00:09Z' }), now)).toBe(true)
    expect(isStale(ac(), now)).toBe(true)
  })

  it('prefers track, then heading', () => {
    expect(rotation(ac({ track: 90, heading: 80 }))).toBe(90)
    expect(rotation(ac({ heading: 80 }))).toBe(80)
    expect(rotation(ac())).toBe(0)
  })
})

describe('trailSegments', () => {
  const pt = (lat: number, alt?: number): TrailPoint => ({ lat, lon: 23, alt, t: lat })

  it('groups by altitude bucket and joins segments', () => {
    const segs = trailSegments([pt(1, 1000), pt(2, 1100), pt(3, 5000), pt(4, 5100), pt(5)])
    expect(segs.map((s) => s.points.map((p) => p[0]))).toEqual([
      [1, 2],
      [2, 3, 4],
      [4, 5],
    ])
    expect(segs.map((s) => s.color)).toEqual([altitudeColor(1000), altitudeColor(5000), NO_ALT_COLOR])
  })

  it('returns nothing for fewer than two points', () => {
    expect(trailSegments([])).toEqual([])
    expect(trailSegments([pt(1, 1000)])).toEqual([])
  })
})
