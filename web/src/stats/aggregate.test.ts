import { describe, expect, it } from 'vitest'
import type { HourRow } from '../api/stats'
import { bestDay, crcShare, heatmap, localParts, momentSearch, topWithOther } from './aggregate'

const row = (hour: string, adsb: number, modes = 0, partial = false): HourRow => ({
  hour, aircraftAdsb: adsb, aircraftModes: modes, flightsStarted: 0, positions: 0, ...(partial ? { partial } : {}),
})

const TZ = 'Europe/Amsterdam'

describe('heatmap', () => {
  it('averages by local weekday and hour, skipping the partial hour', () => {
    // Friday 20 and 27 Nov 2026, 12:00 UTC = 13:00 CET
    const hm = heatmap([row('2026-11-20T12:00:00Z', 10, 2), row('2026-11-27T12:00:00Z', 4), row('2026-11-27T13:00:00Z', 50, 0, true)], TZ)
    expect(hm.cells).toEqual([[13, 4, 8]])
    expect(hm.max).toBe(8)
    expect(hm.latest.get('4-13')).toBe('2026-11-27T12:00:00Z')
  })

  it('follows DST: the skipped hour has no cell, the repeated one averages both', () => {
    // 29 Mar 2026 (Sunday): 00:00Z is 01:00 CET, 01:00Z is 03:00 CEST
    const spring = heatmap([row('2026-03-29T00:00:00Z', 1), row('2026-03-29T01:00:00Z', 3)], TZ)
    expect(spring.cells).toEqual([[1, 6, 1], [3, 6, 3]])
    // 25 Oct 2026 (Sunday): 00:00Z and 01:00Z are both 02:00 local
    const autumn = heatmap([row('2026-10-25T00:00:00Z', 2), row('2026-10-25T01:00:00Z', 4)], TZ)
    expect(autumn.cells).toEqual([[2, 6, 3]])
    expect(autumn.latest.get('6-2')).toBe('2026-10-25T01:00:00Z')
  })

  it('reads local parts in the given zone', () => {
    expect(localParts('2026-11-20T23:30:00Z', 'Pacific/Auckland')).toEqual({ day: 5, hour: 12 }) // Saturday
    expect(localParts('2026-11-20T23:30:00Z', 'UTC')).toEqual({ day: 4, hour: 23 })
  })
})

describe('countries, best day and moments', () => {
  it('sums the rest as Other', () => {
    const list = ['A', 'B', 'C', 'D'].map((c, i) => ({ name: c, aircraftHours: 10 - i }))
    expect(topWithOther(list, 2)).toEqual([list[0], list[1], { name: 'Other', aircraftHours: 15 }])
    expect(topWithOther(list, 4)).toEqual(list)
  })

  it('finds the best day, the first on ties', () => {
    const d = (day: string, nm: number) => ({ day, maxRangeNm: nm, hour: `${day}T00:00:00Z` })
    expect(bestDay([d('2026-11-18', 100), d('2026-11-19', 150), d('2026-11-20', 150)])).toBe(1)
    expect(bestDay([])).toBe(-1)
  })

  it('opens the map in playback at the hour', () => {
    expect(momentSearch('2026-11-20T12:00:00Z')).toBe('?t=2026-11-20T12%3A00%3A00Z')
    expect(momentSearch('2026-11-20T12:41:07.250Z', '4840D6')).toBe('?t=2026-11-20T12%3A41%3A07Z&icao=4840D6')
  })

  it('gives the CRC failure share of known hours', () => {
    const r = { hour: '2026-11-20T12:00:00Z', aircraftAdsb: 0, aircraftModes: 0, flightsStarted: 0, positions: 0 }
    expect(crcShare({ ...r, frames: 4000, crcFailures: 600 })).toBe(15)
    expect(crcShare({ ...r, frames: 0, crcFailures: 0 })).toBeNull()
    expect(crcShare(r)).toBeNull()
  })
})
