import { describe, expect, it } from 'vitest'
import historyFixture from '../api/history.fixture.json'
import type { FlightDetail } from '../api/history'
import { readTheme } from '../stats/theme'
import { getHover, nearestIndex, setHover, subscribeHover, clearHover } from './hover'
import { profileData, profileOption } from './profile'

const base: FlightDetail = { ...historyFixture.flight, positions: [], samples: [] }

describe('profileData', () => {
  it('takes altitude and ground speed from ADS-B positions', () => {
    const d = profileData({
      ...base,
      positions: [
        { t: 1, lat: 25.1, lon: 22.9, alt: 1000, gs: 200 },
        { t: 2, lat: 25.2, lon: 22.9 },
      ],
    })
    expect(d).toEqual({ modeS: false, t: [1, 2], alt: [1000, null], gs: [200, null], squawks: [] })
  })

  it('marks the squawk changes of Mode-S samples', () => {
    const d = profileData({
      ...base,
      adsb: false,
      samples: [
        { t: 1, alt: 3000, squawk: '7000' },
        { t: 2, alt: 3100, squawk: '7000' },
        { t: 3, squawk: '7700' },
      ],
    })
    expect(d.modeS).toBe(true)
    expect(d.alt).toEqual([3000, 3100, null])
    expect(d.squawks).toEqual([
      { t: 1, alt: 3000, squawk: '7000' },
      { t: 3, alt: 3100, squawk: '7700' },
    ])
  })
})

describe('profileOption', () => {
  it('has a speed axis only for ADS-B and squawk marks only for Mode-S', () => {
    const adsb = profileOption({ modeS: false, t: [1], alt: [1], gs: [1], squawks: [] }, readTheme())
    expect(adsb.yAxis).toHaveLength(2)
    expect(adsb.series).toHaveLength(2)
    const modeS = profileOption({ modeS: true, t: [1], alt: [1], gs: [], squawks: [{ t: 1, alt: 1, squawk: '7700' }] }, readTheme())
    expect(modeS.yAxis).toHaveLength(1)
    expect(JSON.stringify(modeS.series)).toContain('7700')
  })
})

describe('shared hover', () => {
  it('finds the closest time', () => {
    expect([nearestIndex([10, 20, 30], 14), nearestIndex([10, 20, 30], 16), nearestIndex([10, 20, 30], 99)]).toEqual([0, 1, 2])
    expect(nearestIndex([], 5)).toBe(-1)
  })

  it('notifies on change only and clears by source', () => {
    let n = 0
    const stop = subscribeHover(() => {
      n++
    })
    setHover({ flight: 1, t: 5, source: 'map' })
    setHover({ flight: 1, t: 5, source: 'map' })
    clearHover('chart')
    expect([n, getHover()?.t]).toEqual([1, 5])
    clearHover('map')
    expect([n, getHover()]).toEqual([2, null])
    stop()
  })
})
