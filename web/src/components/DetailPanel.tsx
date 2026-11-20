import { useEffect, useState, type Dispatch, type ReactNode } from 'react'
import type { Aircraft } from '../api/types'
import type { Action } from '../state/reducer'
import { urlWith } from '../state/url'
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
    ['Mach', fixed(a.mach, 3)],
    ['Roll', a.roll === undefined ? undefined : `${Math.abs(a.roll).toFixed(1)}° ${a.roll < 0 ? 'left' : a.roll > 0 ? 'right' : ''}`.trimEnd()],
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

/**
 * copyText puts text on the clipboard: the Clipboard API where the page is a secure context, otherwise a
 * hidden textarea and execCommand (plain http on a LAN address). Resolves to false when both fail.
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (window.isSecureContext && 'clipboard' in navigator) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // fall through to the textarea
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.opacity = '0'
  document.body.append(area)
  area.select()
  try {
    // eslint-disable-next-line @typescript-eslint/no-deprecated -- the only copy that works outside a secure context
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    area.remove()
  }
}

const COPY_FEEDBACK_MS = 1500

/**
 * CopyLinkButton copies the page URL, which carries the selection and table filters, plus the playback moment when
 * one is given, and briefly confirms.
 */
function CopyLinkButton({ moment }: { moment: Moment | undefined }) {
  const [result, setResult] = useState<'copied' | 'failed' | null>(null)
  useEffect(() => {
    if (result === null) {
      return
    }
    const t = setTimeout(() => {
      setResult(null)
    }, COPY_FEEDBACK_MS)
    return () => {
      clearTimeout(t)
    }
  }, [result])
  return (
    <button
      type="button"
      className="detail-copy"
      title={`Copy a link that opens this aircraft${moment ? ' at this moment' : ''} with the current table filters`}
      onClick={() => {
        const { origin, pathname, search, hash } = window.location
        const href = moment ? `${origin}${pathname}${urlWith(search, moment)}${hash}` : window.location.href
        void copyText(href).then((ok) => {
          setResult(ok ? 'copied' : 'failed')
        })
      }}
    >
      {result === 'copied' ? 'Copied' : result === 'failed' ? 'Copy failed' : 'Copy link'}
    </button>
  )
}

/** Moment is a playback position for links. */
export interface Moment {
  t: string
  speed: number
}

/** DetailPanel shows all known fields of the selected aircraft; moment, in playback, goes into "Copy link". */
export function DetailPanel({
  aircraft,
  dispatch,
  moment,
}: {
  aircraft: Aircraft
  dispatch: Dispatch<Action>
  moment?: Moment | undefined
}) {
  return (
    <aside className={`detail${isEmergency(aircraft) ? ' emergency' : ''}`} aria-label="Aircraft details">
      <header className="detail-head">
        <h2>{aircraft.callsign ?? aircraft.icao}</h2>
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
