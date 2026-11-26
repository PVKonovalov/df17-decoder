import { MAX_TRACK_IDS } from '../api/history'
import { PLAYBACK_SPEEDS } from '../api/types'
import { COLUMN_IDS, DEFAULT_SORT, type ColumnId, type Filter, type Sort } from '../table/rows'

/**
 * UrlState is what a shareable link carries: the selected aircraft, the table's filter and sort, and a moment on
 * the tape (playback time and speed).
 */
export interface UrlState {
  icao: string | null
  history: string | null // an aircraft shown from its history (not on the map)
  flights: number[] // flight ids whose stored tracks are shown
  profile: number | null // the flight whose profile is open
  filter: Filter
  sort: Sort
  t: string | null // RFC 3339 UTC
  speed: number | null // a playback speed other than 1
}

/** normalizeTime returns raw as an RFC 3339 UTC time (whole seconds), or null when it isn't a time. */
export function normalizeTime(raw: string | null): string | null {
  const ms = raw === null || raw.trim() === '' ? NaN : Date.parse(raw)
  return Number.isNaN(ms) ? null : new Date(Math.floor(ms / 1000) * 1000).toISOString().replace('.000Z', 'Z')
}

function parseSpeed(raw: string | null): number | null {
  const v = Number(raw)
  return v !== 1 && PLAYBACK_SPEEDS.some((s) => s === v) ? v : null
}

/** parseIds reads a comma-separated list of positive flight ids, keeping the first MAX_TRACK_IDS distinct ones. */
export function parseIds(raw: string | null): number[] {
  const ids = (raw ?? '')
    .split(',')
    .map((v) => Number(v))
    .filter((n) => Number.isSafeInteger(n) && n > 0)
  return [...new Set(ids)].slice(0, MAX_TRACK_IDS)
}

const ICAO_RE = /^[0-9A-F]{6}$/

/** normalizeIcao returns raw as an upper-case 6-digit hex ICAO address, or null when it isn't one. */
export function normalizeIcao(raw: string | null): string | null {
  const v = raw?.trim().toUpperCase() ?? ''
  return ICAO_RE.test(v) ? v : null
}

/**
 * parseUrl reads a query string: icao=738ABC, history=738ABC, flights=12,13, profile=12, q=<filter text>, pos=1 (with position only) and
 * sort=<column> or sort=-<column> (descending), t=<RFC 3339> and speed=<x>. Missing or invalid values give the
 * defaults.
 */
export function parseUrl(search: string): UrlState {
  const p = new URLSearchParams(search)
  const rawSort = p.get('sort') ?? ''
  const desc = rawSort.startsWith('-')
  const id = COLUMN_IDS.find((c) => c === (desc ? rawSort.slice(1) : rawSort))
  return {
    icao: normalizeIcao(p.get('icao')),
    history: normalizeIcao(p.get('history')),
    flights: parseIds(p.get('flights')),
    profile: parseIds(p.get('profile'))[0] ?? null,
    filter: { text: p.get('q') ?? '', withPosition: p.get('pos') === '1' },
    sort: id === undefined ? DEFAULT_SORT : { id: id satisfies ColumnId, desc },
    t: normalizeTime(p.get('t')),
    speed: parseSpeed(p.get('speed')),
  }
}

/**
 * urlWith returns search ("" or "?…") with the fields of patch written into it; defaults are left out and
 * other parameters are kept.
 */
export function urlWith(search: string, patch: Partial<UrlState>): string {
  const p = new URLSearchParams(search)
  const put = (key: string, value: string | null) => {
    if (value === null || value === '') {
      p.delete(key)
    } else {
      p.set(key, value)
    }
  }
  if (patch.icao !== undefined) {
    put('icao', patch.icao)
  }
  if (patch.history !== undefined) {
    put('history', patch.history)
  }
  if (patch.flights !== undefined) {
    put('flights', patch.flights.join(','))
  }
  if (patch.profile !== undefined) {
    put('profile', patch.profile === null ? null : String(patch.profile))
  }
  if (patch.filter !== undefined) {
    put('q', patch.filter.text)
    put('pos', patch.filter.withPosition ? '1' : null)
  }
  if (patch.sort !== undefined) {
    const { id, desc } = patch.sort
    put('sort', id === DEFAULT_SORT.id && desc === DEFAULT_SORT.desc ? null : `${desc ? '-' : ''}${id}`)
  }
  if (patch.t !== undefined) {
    put('t', patch.t)
  }
  if (patch.speed !== undefined) {
    put('speed', patch.speed === null || patch.speed === 1 ? null : String(patch.speed))
  }
  const out = p.toString()
  return out === '' ? '' : `?${out}`
}

/** replaceUrl writes patch into the page's query string without adding a history entry. */
export function replaceUrl(patch: Partial<UrlState>): void {
  const { pathname, search, hash } = window.location
  const next = urlWith(search, patch)
  if (next !== search) {
    window.history.replaceState(window.history.state, '', `${pathname}${next}${hash}`)
  }
}
