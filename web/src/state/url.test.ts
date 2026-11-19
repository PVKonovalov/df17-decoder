import { afterEach, describe, expect, it } from 'vitest'
import { DEFAULT_SORT } from '../table/rows'
import { normalizeIcao, parseUrl, replaceUrl, urlWith } from './url'

afterEach(() => {
  window.history.replaceState(null, '', '/')
})

describe('parseUrl', () => {
  it('gives the defaults for an empty query', () => {
    expect(parseUrl('')).toEqual({ icao: null, filter: { text: '', withPosition: false }, sort: DEFAULT_SORT })
  })

  it('reads the selection, filter and sort', () => {
    expect(parseUrl('?icao=738abc&q=ELY&pos=1&sort=-altitude')).toEqual({
      icao: '738ABC',
      filter: { text: 'ELY', withPosition: true },
      sort: { id: 'altitude', desc: true },
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
