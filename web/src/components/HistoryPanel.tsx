import { useEffect, useState, type Dispatch, type ReactNode } from 'react'
import type { AircraftHistory } from '../api/history'
import type { Action } from '../state/reducer'
import { CopyLinkButton, routeText, trackerUrl, typeText, useReference, type Moment } from './DetailPanel'

/** localZone is the browser's IANA time zone for the server's day counts; UTC when unknown. */
export function localZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

export function aircraftHistoryUrl(icao: string): string {
  return `/api/history/aircraft/${icao}?tz=${encodeURIComponent(localZone())}`
}

const SUMMARY_TTL_MS = 10_000
const summaries = new Map<string, { at: number; p: Promise<AircraftHistory | null> }>()

/** loadSummary fetches icao's summary, sharing a request made in the last SUMMARY_TTL_MS (panel and section). */
function loadSummary(icao: string): Promise<AircraftHistory | null> {
  const hit = summaries.get(icao)
  if (hit !== undefined && Date.now() - hit.at < SUMMARY_TTL_MS) {
    return hit.p
  }
  const p = fetch(aircraftHistoryUrl(icao)).then((r) => (r.ok ? (r.json() as Promise<AircraftHistory>) : null))
  p.catch(() => {
    summaries.delete(icao)
  })
  summaries.set(icao, { at: Date.now(), p })
  return p
}

/** useAircraftHistory loads the history summary of icao: undefined while loading, null when it has none. */
export function useAircraftHistory(icao: string): AircraftHistory | null | undefined {
  const [data, setData] = useState<{ icao: string; v: AircraftHistory | null } | null>(null)
  useEffect(() => {
    let live = true
    loadSummary(icao)
      .then((v) => {
        if (live) {
          setData({ icao, v })
        }
      })
      .catch(() => undefined)
    return () => {
      live = false
    }
  }, [icao])
  return data?.icao === icao ? data.v : undefined
}

const when = (iso: string) => new Date(iso).toLocaleString()

/** historyRows lists what the summary knows about an aircraft that isn't on the map, as [label, value]. */
export function historyRows(h: AircraftHistory): [string, ReactNode][] {
  const rows: [string, ReactNode | undefined][] = [
    ['ICAO', h.icao],
    ['Callsign', h.callsigns[0]?.callsign],
    ['Country', h.country],
    ['Transponder', h.adsb ? 'ADS-B' : 'Mode S only'],
  ]
  return rows.filter((r): r is [string, ReactNode] => r[1] !== undefined)
}

/**
 * HistoryOnlyPanel is the detail panel of an aircraft that isn't on the map (picked in the search or from a
 * ?history= link): no live fields, only what the history and the user's reference data know, then children (the
 * History section).
 */
export function HistoryOnlyPanel({
  icao,
  dispatch,
  moment,
  children,
}: {
  icao: string
  dispatch: Dispatch<Action>
  moment?: Moment | undefined
  children?: ReactNode
}) {
  const h = useAircraftHistory(icao)
  const callsign = h?.callsigns[0]?.callsign
  const [ref] = useReference(icao, callsign, true)
  const extra: [string, ReactNode | undefined][] = [
    ['Route', routeText(ref.route)],
    ['Registration', ref.aircraft?.registration],
    ['Aircraft type', typeText(ref.aircraft)],
    ['Operator', ref.aircraft?.operator],
  ]
  const rows = h ? historyRows(h) : []
  rows.splice(2, 0, ...extra.filter((r): r is [string, ReactNode] => r[1] !== undefined))
  return (
    <aside className="detail history-only" aria-label="Aircraft history">
      <header className="detail-head">
        <h2>{callsign ?? icao}</h2>
        <CopyLinkButton moment={moment} />
        <button
          type="button"
          className="detail-close"
          aria-label="Close details"
          onClick={() => {
            dispatch({ type: 'clearSelection' })
          }}
        >
          ×
        </button>
      </header>
      <p className="detail-gone history-badge">
        {moment ? 'Not on the map at this moment' : 'Not received now'}
        {h && ` · last seen ${when(h.lastSeen)}`}
      </p>
      {h === null && <p className="detail-gone">No history for {icao}.</p>}
      <dl className="detail-list">
        {rows.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
      {children}
      <a className="detail-link" href={trackerUrl(icao)} target="_blank" rel="noreferrer noopener">
        View on ADS-B Exchange ↗
      </a>
    </aside>
  )
}
