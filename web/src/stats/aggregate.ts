import type { HourRow, RangeDay } from '../api/stats'

export const WEEKDAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'] as const

const formatters = new Map<string, Intl.DateTimeFormat>()

/** localParts returns the weekday (Monday 0) and hour of an instant in the IANA zone tz. */
export function localParts(iso: string, tz: string): { day: number; hour: number } {
  let f = formatters.get(tz)
  if (f === undefined) {
    f = new Intl.DateTimeFormat('en-US', { timeZone: tz, weekday: 'short', hour: '2-digit', hourCycle: 'h23' })
    formatters.set(tz, f)
  }
  let day = 0
  let hour = 0
  for (const p of f.formatToParts(new Date(iso))) {
    if (p.type === 'weekday') {
      day = WEEKDAYS.indexOf(p.value as (typeof WEEKDAYS)[number])
    } else if (p.type === 'hour') {
      hour = Number(p.value) % 24
    }
  }
  return { day, hour }
}

/** Heatmap: cells are [hour, weekday, average aircraft]; latest maps "weekday-hour" to the newest hour start in it. */
export interface Heatmap {
  cells: [number, number, number][]
  max: number
  latest: Map<string, string>
}

/**
 * heatmap averages the aircraft per hour of the finished hours in rows by local weekday and hour in tz. An hour a
 * DST change skips has no cell; a repeated one averages both.
 */
export function heatmap(rows: HourRow[], tz: string): Heatmap {
  const sum = new Map<string, { n: number; total: number; latest: string }>()
  for (const r of rows) {
    if (r.partial === true) {
      continue
    }
    const { day, hour } = localParts(r.hour, tz)
    const key = `${day}-${hour}`
    const c = sum.get(key) ?? { n: 0, total: 0, latest: r.hour }
    c.n++
    c.total += r.aircraftAdsb + r.aircraftModes
    if (Date.parse(r.hour) > Date.parse(c.latest)) {
      c.latest = r.hour
    }
    sum.set(key, c)
  }
  const out: Heatmap = { cells: [], max: 0, latest: new Map() }
  for (const [key, c] of sum) {
    const [day, hour] = key.split('-').map(Number) as [number, number]
    const avg = Math.round((c.total / c.n) * 10) / 10
    out.cells.push([hour, day, avg])
    out.max = Math.max(out.max, avg)
    out.latest.set(key, c.latest)
  }
  out.cells.sort((a, b) => a[1] - b[1] || a[0] - b[0])
  return out
}

export const OTHER = 'Other'

/** Ranked is one bar of a ranking: a country or a callsign prefix and its aircraft-hours. */
export interface Ranked {
  name: string
  aircraftHours: number
}

/** topWithOther keeps the n largest entries (the list comes sorted) and sums the rest as "Other". */
export function topWithOther(list: Ranked[], n: number): Ranked[] {
  const rest = list.slice(n).reduce((s, c) => s + c.aircraftHours, 0)
  const top = list.slice(0, n)
  return rest > 0 ? [...top, { name: OTHER, aircraftHours: rest }] : top
}

/** crcShare returns an hour's CRC failures as a percentage of its frames, null when unknown or without frames. */
export function crcShare(r: HourRow): number | null {
  return r.frames === undefined || r.crcFailures === undefined || r.frames === 0
    ? null
    : Math.round((r.crcFailures / r.frames) * 1000) / 10
}

/** bestDay returns the index of the day with the longest range, the first one on ties; -1 when there are none. */
export function bestDay(days: RangeDay[]): number {
  let best = -1
  days.forEach((d, i) => {
    if (best < 0 || d.maxRangeNm > (days[best]?.maxRangeNm ?? 0)) {
      best = i
    }
  })
  return best
}

/** momentSearch is the map query that opens playback at a time (RFC 3339), selecting icao when given. */
export function momentSearch(t: string, icao?: string): string {
  const p = new URLSearchParams({ t: new Date(Math.floor(Date.parse(t) / 1000) * 1000).toISOString().replace('.000Z', 'Z') })
  if (icao !== undefined) {
    p.set('icao', icao)
  }
  return `?${p.toString()}`
}
