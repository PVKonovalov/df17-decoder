import { describe, expect, it } from 'vitest'
import type { Aircraft } from '../api/types'
import { ageSeconds, fmtAgo, fmtVerticalRate, isEmergency, newAgeMs, NEW_ROW_MS, visibleRows, type Filter } from './rows'

const NOW = Date.parse('2026-11-19T10:00:30Z')
const ac = (icao: string, extra: Partial<Aircraft> = {}): Aircraft => ({
  icao,
  firstSeen: '2026-11-19T10:00:00Z',
  lastSeen: '2026-11-19T10:00:20Z',
  messages: 1,
  messagesByDf: {},
  hasPosition: extra.distanceNm !== undefined,
  adsb: false,
  ...extra,
})
const all: Filter = { text: '', withPosition: false }
const ids = (rows: Aircraft[]) => rows.map((a) => a.icao)

describe('visibleRows sort', () => {
  const list = [
    ac('000001', { distanceNm: 40, altBaro: 0 }),
    ac('000002'),
    ac('000003', { distanceNm: 5, altBaro: 12000 }),
    ac('000004', { distanceNm: 20 }),
  ]

  it('defaults to distance ascending with unknown last', () => {
    expect(ids(visibleRows(list, all, null, NOW))).toEqual(['000003', '000004', '000001', '000002'])
  })

  it('keeps unknown values last when descending', () => {
    expect(ids(visibleRows(list, all, { id: 'distance', desc: true }, NOW))).toEqual([
      '000001',
      '000004',
      '000003',
      '000002',
    ])
    expect(ids(visibleRows(list, all, { id: 'altitude', desc: true }, NOW))).toEqual([
      '000003',
      '000001', // 0 ft is a real value, not unknown
      '000002',
      '000004',
    ])
  })

  it('sorts strings and breaks ties by ICAO', () => {
    const named = [ac('B', { callsign: 'ELY1' }), ac('A', { callsign: 'ELY1' }), ac('C', { callsign: 'AAL2' })]
    expect(ids(visibleRows(named, all, { id: 'callsign', desc: false }, NOW))).toEqual(['C', 'A', 'B'])
  })

  it('pins emergencies to the top regardless of sort', () => {
    const withEmerg = [
      ...list,
      ac('00000E', { squawk: '7700' }),
      ac('00000F', { emergency: 'lifeguard', distanceNm: 99 }),
      ac('000010', { emergency: 'none', distanceNm: 1 }),
    ]
    for (const desc of [false, true]) {
      // both pinned; among themselves the known distance sorts before the unknown one
      expect(ids(visibleRows(withEmerg, all, { id: 'distance', desc }, NOW)).slice(0, 2)).toEqual(['00000F', '00000E'])
    }
  })

  it('sorts last seen by age', () => {
    const seen = [ac('A', { lastSeen: '2026-11-19T10:00:10Z' }), ac('B', { lastSeen: '2026-11-19T10:00:29Z' })]
    expect(ids(visibleRows(seen, all, { id: 'lastSeen', desc: false }, NOW))).toEqual(['B', 'A'])
  })
})

describe('visibleRows filter', () => {
  const list = [
    ac('738ABC', { callsign: 'ELY001', distanceNm: 3 }),
    ac('4CA123', { squawk: '1200' }),
    ac('738DEF', { callsign: 'ISR22' }),
  ]

  it('matches callsign, ICAO and squawk case-insensitively', () => {
    expect(ids(visibleRows(list, { text: 'ely', withPosition: false }, null, NOW))).toEqual(['738ABC'])
    expect(ids(visibleRows(list, { text: ' 738 ', withPosition: false }, null, NOW)).sort()).toEqual([
      '738ABC',
      '738DEF',
    ])
    expect(ids(visibleRows(list, { text: '1200', withPosition: false }, null, NOW))).toEqual(['4CA123'])
  })

  it('filters to aircraft with position', () => {
    expect(ids(visibleRows(list, { text: '', withPosition: true }, null, NOW))).toEqual(['738ABC'])
  })
})

describe('helpers', () => {
  it('detects emergencies', () => {
    expect(isEmergency(ac('A', { squawk: '7500' }))).toBe(true)
    expect(isEmergency(ac('A', { squawk: '7000' }))).toBe(false)
    expect(isEmergency(ac('A', { emergency: 'none' }))).toBe(false)
    expect(isEmergency(ac('A', { emergency: 'general' }))).toBe(true)
  })

  it('marks aircraft first seen less than NEW_ROW_MS ago as new', () => {
    const first = (ms: number) => ac('A', { firstSeen: new Date(NOW - ms).toISOString() })
    expect(newAgeMs(first(0), NOW)).toBe(0)
    expect(newAgeMs(first(4000), NOW)).toBe(4000)
    expect(newAgeMs(first(NEW_ROW_MS - 1), NOW)).toBe(NEW_ROW_MS - 1)
    expect(newAgeMs(first(NEW_ROW_MS), NOW)).toBeUndefined()
    expect(newAgeMs(first(-2000), NOW)).toBe(0) // first seen "after" now: clamp
  })

  it('formats ages and vertical rates', () => {
    expect(ageSeconds(ac('A'), NOW)).toBe(10)
    expect(fmtAgo(10)).toBe('10 s ago')
    expect(fmtAgo(185)).toBe('3 min ago')
    expect(fmtVerticalRate(1280)).toBe('↑ 1,280')
    expect(fmtVerticalRate(-640)).toBe('↓ 640')
    expect(fmtVerticalRate(0)).toBe('0')
  })
})
