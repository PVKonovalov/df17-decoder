import type { Aircraft } from '../api/types'
import { aircraftAltitude } from '../map/style'

export const EMERGENCY_SQUAWKS = new Set(['7500', '7600', '7700'])

export type ColumnId =
  | 'icao'
  | 'callsign'
  | 'squawk'
  | 'altitude'
  | 'groundSpeed'
  | 'track'
  | 'verticalRate'
  | 'distance'
  | 'bearing'
  | 'country'
  | 'messages'
  | 'rssi'
  | 'lastSeen'

export interface Sort {
  id: ColumnId
  desc: boolean
}

export interface Filter {
  text: string
  withPosition: boolean
}

export const DEFAULT_SORT: Sort = { id: 'distance', desc: false }

/** isEmergency: an emergency squawk or an emergency state other than "none" from TC 28. */
export function isEmergency(a: Aircraft): boolean {
  return (a.squawk !== undefined && EMERGENCY_SQUAWKS.has(a.squawk)) || (a.emergency !== undefined && a.emergency !== 'none')
}

export const NEW_ROW_MS = 30_000

/** newAgeMs returns how long ago (ms, data time) the aircraft was first seen while that is under NEW_ROW_MS, else undefined. */
export function newAgeMs(a: Aircraft, now: number): number | undefined {
  const age = Math.max(0, now - Date.parse(a.firstSeen))
  return age < NEW_ROW_MS ? age : undefined
}

/** ageSeconds is how long ago the aircraft was last heard at data time now (ms). */
export function ageSeconds(a: Aircraft, now: number): number {
  return Math.max(0, Math.round((now - Date.parse(a.lastSeen)) / 1000))
}

/** sortValue returns the value a column sorts by; undefined means unknown and always sorts last. */
export function sortValue(a: Aircraft, id: ColumnId, now: number): string | number | undefined {
  switch (id) {
    case 'icao':
      return a.icao
    case 'callsign':
      return a.callsign
    case 'squawk':
      return a.squawk
    case 'altitude':
      return aircraftAltitude(a)
    case 'groundSpeed':
      return a.groundSpeed
    case 'track':
      return a.track
    case 'verticalRate':
      return a.verticalRate
    case 'distance':
      return a.distanceNm
    case 'bearing':
      return a.bearing
    case 'country':
      return a.country
    case 'messages':
      return a.messages
    case 'rssi':
      return a.rssi
    case 'lastSeen':
      return ageSeconds(a, now)
  }
}

function compareValues(x: string | number, y: string | number): number {
  if (typeof x === 'number' && typeof y === 'number') {
    return x - y
  }
  return String(x).localeCompare(String(y))
}

/** matchesFilter: free text over callsign, ICAO and squawk (case-insensitive), optionally positions only. */
export function matchesFilter(a: Aircraft, f: Filter): boolean {
  if (f.withPosition && !a.hasPosition) {
    return false
  }
  const q = f.text.trim().toUpperCase()
  if (q === '') {
    return true
  }
  return [a.callsign, a.icao, a.squawk].some((v) => v?.toUpperCase().includes(q) === true)
}

/**
 * visibleRows filters and sorts aircraft for the table: emergencies first regardless of sort, then by the
 * sort column with unknown values last in both directions, then by ICAO.
 */
export function visibleRows(aircraft: Iterable<Aircraft>, f: Filter, sort: Sort | null, now: number): Aircraft[] {
  const rows = [...aircraft].filter((a) => matchesFilter(a, f))
  const s = sort ?? DEFAULT_SORT
  const dir = s.desc ? -1 : 1
  const keyed = rows.map((a) => ({ a, v: sortValue(a, s.id, now), e: isEmergency(a) }))
  keyed.sort((x, y) => {
    if (x.e !== y.e) {
      return x.e ? -1 : 1
    }
    if (x.v === undefined || y.v === undefined) {
      if (x.v !== y.v) {
        return x.v === undefined ? 1 : -1
      }
    } else {
      const c = compareValues(x.v, y.v)
      if (c !== 0) {
        return c * dir
      }
    }
    return x.a.icao.localeCompare(y.a.icao)
  })
  return keyed.map((k) => k.a)
}

/** fmtAgo formats an age in seconds as "12 s ago" / "3 min ago". */
export function fmtAgo(seconds: number): string {
  return seconds < 120 ? `${seconds} s ago` : `${Math.floor(seconds / 60)} min ago`
}

/** fmtVerticalRate formats ft/min with an arrow: "↑ 1,280", "↓ 640", "0". */
export function fmtVerticalRate(fpm: number): string {
  if (fpm === 0) {
    return '0'
  }
  return `${fpm > 0 ? '↑' : '↓'} ${Math.abs(fpm).toLocaleString('en-US')}`
}
