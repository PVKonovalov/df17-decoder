import { afterEach, describe, expect, it } from 'vitest'
import { DEFAULT_SORT } from '../table/rows'
import { normalizeIcao, normalizeTime, parseIds, parseUrl, replaceUrl, urlWith } from './url'

afterEach(() => {
  window.history.replaceState(null, '', '/')
})

describe('parseUrl', () => {
  it('gives the defaults for an empty query', () => {
    expect(parseUrl('')).toEqual({
      icao: null,
      history: null,
      flights: [],
      profile: null,
      filter: { text: '', withPosition: false },
      sort: DEFAULT_SORT,
      t: null,
      speed: null,
    })
  })

  it('reads the selection, filter and sort', () => {
    expect(parseUrl('?icao=738abc&q=ELY&pos=1&sort=-altitude')).toEqual({
      icao: '738ABC',
      history: null,
      flights: [],
      profile: null,
      filter: { text: 'ELY', withPosition: true },
      sort: { id: 'altitude', desc: true },
      t: null,
      speed: null,
    })
    expect(parseUrl('?sort=callsign').sort).toEqual({ id: 'callsign', desc: false })
  })

  it('ignores invalid values', () => {
    const s = parseUrl('?icao=73Z000&pos=yes&sort=-nope')
    expect(s.icao).toBeNull()
    expect(s.filter.withPosition).toBe(false)
    expect(s.sort).toEqual(DEFAULT_SORT)
    expect(normalizeIcao(' 4ca123 ')).toBe('4CA123')
    expect(normalizeIcao('4CA12')).toBeNull()
    expect(normalizeIcao(null)).toBeNull()
    expect(parseUrl('?t=yesterday&speed=3')).toMatchObject({ t: null, speed: null })
    expect(parseUrl('?speed=1').speed).toBeNull()
  })

  it('reads a moment on the tape', () => {
    expect(parseUrl('?icao=738ABC&t=2026-11-20T10:00:05.250Z&speed=16')).toMatchObject({
      icao: '738ABC',
      t: '2026-11-20T10:00:05Z',
      speed: 16,
    })
    expect(normalizeTime('2026-11-20T12:00:00+02:00')).toBe('2026-11-20T10:00:00Z')
    expect(normalizeTime('')).toBeNull()
  })
})

describe('urlWith', () => {
  it('writes non-default values, drops defaults and keeps other parameters', () => {
    const full = urlWith('?debug=1', {
      icao: '738ABC',
      filter: { text: 'EL Y', withPosition: true },
      sort: { id: 'altitude', desc: true },
    })
    expect(full).toBe('?debug=1&icao=738ABC&q=EL+Y&pos=1&sort=-altitude')
    expect(parseUrl(full).filter.text).toBe('EL Y')
    expect(urlWith(full, { icao: null, filter: { text: '', withPosition: false }, sort: DEFAULT_SORT })).toBe('?debug=1')
    expect(urlWith('?icao=738ABC', { icao: null })).toBe('')
    expect(urlWith('?q=x', { icao: '738ABC' })).toBe('?q=x&icao=738ABC')
  })

  it('round-trips a moment with the selection', () => {
    const link = urlWith('?icao=738ABC', { t: '2026-11-20T10:00:00Z', speed: 4 })
    expect(link).toBe('?icao=738ABC&t=2026-11-20T10%3A00%3A00Z&speed=4')
    expect(parseUrl(link)).toMatchObject({ icao: '738ABC', t: '2026-11-20T10:00:00Z', speed: 4 })
    expect(urlWith(link, { t: null, speed: 1 })).toBe('?icao=738ABC')
  })
})

describe('replaceUrl', () => {
  it('replaces the query without adding history entries', () => {
    const before = window.history.length
    replaceUrl({ icao: '738ABC' })
    expect(window.location.search).toBe('?icao=738ABC')
    replaceUrl({ icao: null })
    expect(window.location.search).toBe('')
    expect(window.history.length).toBe(before)
  })
})

describe('history link', () => {
  it('round-trips history= with a playback moment', () => {
    const search = urlWith('?icao=738001', { history: 'ab12cd', t: '2026-11-20T12:00:00Z', speed: 4 })
    const u = parseUrl(search)
    expect([u.icao, u.history, u.t, u.speed]).toEqual(['738001', 'AB12CD', '2026-11-20T12:00:00Z', 4])
    expect(urlWith(search, { history: null })).toBe('?icao=738001&t=2026-11-20T12%3A00%3A00Z&speed=4')
  })

  it('round-trips the shown tracks with the history aircraft and a moment', () => {
    const search = urlWith('', { history: 'AB12CD', flights: [12, 7], t: '2026-11-20T12:00:00Z', speed: 2 })
    expect(search).toBe('?history=AB12CD&flights=12%2C7&t=2026-11-20T12%3A00%3A00Z&speed=2')
    expect(parseUrl(search)).toMatchObject({ history: 'AB12CD', flights: [12, 7], t: '2026-11-20T12:00:00Z', speed: 2 })
    expect(urlWith(search, { flights: [] })).not.toContain('flights')
    const withProfile = urlWith(search, { profile: 12 })
    expect(parseUrl(withProfile).profile).toBe(12)
    expect(urlWith(withProfile, { profile: null })).toBe(search)
  })

  it('reads at most 20 valid flight ids', () => {
    expect(parseIds('3,x,-1,3,1.5,4')).toEqual([3, 4])
    expect(parseIds(Array.from({ length: 30 }, (_, i) => i + 1).join(','))).toHaveLength(20)
  })

  it('ignores an invalid address', () => {
    expect(parseUrl('?history=XYZ').history).toBeNull()
  })
})
