import type { Dispatch, ReactNode } from 'react'
import type { Aircraft } from '../api/types'
import type { Action } from '../state/reducer'
import { fmtVerticalRate, isEmergency } from '../table/rows'

/** trackerUrl links the aircraft on an external tracker by ICAO hex. */
export function trackerUrl(icao: string): string {
  return `https://globe.adsbexchange.com/?icao=${icao.toLowerCase()}`
}

const fixed = (v: number | undefined, digits: number, unit = '') =>
  v === undefined ? undefined : `${v.toFixed(digits)}${unit}`
const ft = (v: number | undefined) => (v === undefined ? undefined : `${v.toLocaleString('en-US')} ft`)
const MODE_LABELS: Record<string, string> = {
  autopilot: 'AP',
  vnav: 'VNAV',
  althold: 'ALT HOLD',
  approach: 'APPR',
  lnav: 'LNAV',
  tcas: 'TCAS',
}
const selectedAlt = (a: Aircraft) =>
  a.selectedAlt === undefined
    ? undefined
    : `${a.selectedAlt.toLocaleString('en-US')} ft${a.selectedAltSource === undefined ? '' : ` (${a.selectedAltSource.toUpperCase()})`}`
const autopilot = (modes: string[] | undefined) =>
  modes === undefined ? undefined : modes.map((m) => MODE_LABELS[m] ?? m).join(' · ') || 'none engaged'
const time = (iso: string | undefined) => (iso === undefined ? undefined : new Date(iso).toLocaleTimeString())

/** detailRows lists every known field of the aircraft as [label, value]; unknown fields are left out. */
export function detailRows(a: Aircraft): [string, ReactNode][] {
  const rows: [string, ReactNode | undefined][] = [
    ['ICAO', a.icao],
    ['Callsign', a.callsign],
    ['Squawk', a.squawk],
    ['Emergency', a.emergency !== undefined && a.emergency !== 'none' ? a.emergency : undefined],
    ['Country', a.country],
    ['Category', a.category],
    ['Type', a.adsb ? `ADS-B${a.adsbVersion === undefined ? '' : ` v${a.adsbVersion}`}` : 'Mode S only'],
    ['Altitude (baro)', ft(a.altBaro)],
    ['Altitude (GNSS)', ft(a.altGnss)],
    ['On ground', a.onGround === undefined ? undefined : a.onGround ? 'yes' : 'no'],
    ['Ground speed', fixed(a.groundSpeed, 0, ' kt')],
    ['Track', fixed(a.track, 1, '°')],
    ['Heading', fixed(a.heading, 1, '°')],
    ['IAS', a.ias === undefined ? undefined : `${a.ias} kt`],
    ['TAS', a.tas === undefined ? undefined : `${a.tas} kt`],
    ['Vertical rate', a.verticalRate === undefined ? undefined : `${fmtVerticalRate(a.verticalRate)} ft/min`],
    ['Selected altitude', selectedAlt(a)],
    ['Selected heading', fixed(a.selectedHeading, 1, '°')],
    ['Altimeter', fixed(a.baroSetting, 1, ' hPa')],
    ['Autopilot', autopilot(a.autopilotModes)],
    ['Position', a.lat === undefined || a.lon === undefined ? undefined : `${a.lat.toFixed(4)}, ${a.lon.toFixed(4)}`],
    ['Position time', time(a.posTime)],
    ['Distance', fixed(a.distanceNm, 1, ' nm')],
    ['Bearing', fixed(a.bearing, 0, '°')],
    ['RSSI', fixed(a.rssi, 1, ' dBFS')],
    ['Messages', a.messages.toLocaleString('en-US')],
    [
      'By DF',
      Object.entries(a.messagesByDf)
        .map(([df, n]) => `DF${df}: ${n}`)
        .join(', ') || undefined,
    ],
    ['First seen', time(a.firstSeen)],
    ['Last seen', time(a.lastSeen)],
  ]
  return rows.filter((r): r is [string, ReactNode] => r[1] !== undefined)
}

/** DetailPanel shows all known fields of the selected aircraft. */
export function DetailPanel({ aircraft, dispatch }: { aircraft: Aircraft; dispatch: Dispatch<Action> }) {
  return (
    <aside className={`detail${isEmergency(aircraft) ? ' emergency' : ''}`} aria-label="Aircraft details">
      <header className="detail-head">
        <h2>{aircraft.callsign ?? aircraft.icao}</h2>
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
      <dl className="detail-list">
        {detailRows(aircraft).map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
      <a className="detail-link" href={trackerUrl(aircraft.icao)} target="_blank" rel="noreferrer noopener">
        View on ADS-B Exchange ↗
      </a>
    </aside>
  )
}
