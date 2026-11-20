export const RANGES = [
  { key: '24h', label: '24 h', ms: 24 * 3600_000 },
  { key: '7d', label: '7 days', ms: 7 * 24 * 3600_000 },
  { key: '30d', label: '30 days', ms: 30 * 24 * 3600_000 },
  { key: '1y', label: '1 year', ms: 365 * 24 * 3600_000 },
] as const

export type RangeKey = (typeof RANGES)[number]['key'] | 'custom'

/** StatsRange is the shown span; custom ranges are whole local days (fromDay..toDay inclusive). live: it ends now. */
export interface StatsRange {
  key: RangeKey
  from: Date
  to: Date
  live: boolean
  fromDay?: string
  toDay?: string
}

const DAY_RE = /^(\d{4})-(\d{2})-(\d{2})$/

/** localDay returns the local midnight of a YYYY-MM-DD day, or null when it isn't one. */
export function localDay(day: string | null): Date | null {
  const m = DAY_RE.exec(day ?? '')
  if (m === null) {
    return null
  }
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]))
  return d.getDate() === Number(m[3]) ? d : null
}

/** dayString formats d as its local YYYY-MM-DD. */
export function dayString(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/**
 * parseRange reads ?range=24h|7d|30d|1y (default 24h) or ?from=YYYY-MM-DD&to=YYYY-MM-DD (local days, inclusive)
 * at the time now. Invalid values give the default.
 */
export function parseRange(search: string, now: Date): StatsRange {
  const p = new URLSearchParams(search)
  const from = localDay(p.get('from'))
  const toDay = localDay(p.get('to'))
  if (from !== null && toDay !== null && from <= toDay) {
    const to = new Date(toDay.getFullYear(), toDay.getMonth(), toDay.getDate() + 1)
    return { key: 'custom', from, to, live: to > now, fromDay: dayString(from), toDay: dayString(toDay) }
  }
  const r = RANGES.find((x) => x.key === p.get('range')) ?? RANGES[0]
  return { key: r.key, from: new Date(now.getTime() - r.ms), to: now, live: true }
}

/** rangeSearch writes a preset (24h is the default and left out) or a custom day span into search, keeping the rest. */
export function rangeSearch(search: string, sel: { key: RangeKey; fromDay?: string; toDay?: string }): string {
  const p = new URLSearchParams(search)
  p.delete('range')
  p.delete('from')
  p.delete('to')
  if (sel.key === 'custom') {
    if (sel.fromDay !== undefined && sel.toDay !== undefined) {
      p.set('from', sel.fromDay)
      p.set('to', sel.toDay)
    }
  } else if (sel.key !== '24h') {
    p.set('range', sel.key)
  }
  const s = p.toString()
  return s === '' ? '' : `?${s}`
}
