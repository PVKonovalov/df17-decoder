import { describe, expect, it } from 'vitest'
import { dayString, localDay, parseRange, rangeSearch } from './range'

const now = new Date('2026-11-20T12:00:00Z')

describe('range', () => {
  it('defaults to the last 24 hours, ending now', () => {
    const r = parseRange('', now)
    expect(r.key).toBe('24h')
    expect(r.live).toBe(true)
    expect(now.getTime() - r.from.getTime()).toBe(24 * 3600_000)
    expect(parseRange('?range=bogus', now).key).toBe('24h')
  })

  it('round-trips presets and custom days through the URL, keeping other parameters', () => {
    for (const key of ['24h', '7d', '30d', '1y'] as const) {
      const s = rangeSearch('?icao=738ABC', { key })
      expect(parseRange(s, now).key).toBe(key)
      expect(new URLSearchParams(s).get('icao')).toBe('738ABC')
    }
    expect(rangeSearch('', { key: '24h' })).toBe('')
    expect(rangeSearch('?range=7d', { key: '24h' })).toBe('')

    const s = rangeSearch('?range=7d', { key: 'custom', fromDay: '2026-11-01', toDay: '2026-11-07' })
    expect(s).toBe('?from=2026-11-01&to=2026-11-07')
    const r = parseRange(s, now)
    expect(r).toMatchObject({ key: 'custom', fromDay: '2026-11-01', toDay: '2026-11-07', live: false })
    expect(r.from).toEqual(new Date(2026, 10, 1))
    expect(r.to).toEqual(new Date(2026, 10, 8)) // the last day is included
    expect(parseRange('?from=2026-11-15&to=2026-11-20', now).live).toBe(true)
  })

  it('ignores invalid custom days', () => {
    expect(parseRange('?from=2026-11-07&to=2026-11-01', now).key).toBe('24h')
    expect(parseRange('?from=2026-02-30&to=2026-03-01', now).key).toBe('24h')
    expect(localDay('20261101')).toBeNull()
    expect(dayString(new Date(2026, 0, 5))).toBe('2026-01-05')
  })
})
