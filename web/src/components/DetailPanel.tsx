import { useEffect, useState, type Dispatch, type ReactNode, type SyntheticEvent } from 'react'
import { categoryText } from '../map/icons'
import {
  aircraftInfoUrl,
  getReference,
  putReference,
  routeUrl,
  type AircraftInfo,
  type RouteInfo,
} from '../api/reference'
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

/** Reference is the user's data for the aircraft and its callsign: undefined while unknown, null when none. */
export interface Reference {
  aircraft?: AircraftInfo | null | undefined
  route?: RouteInfo | null | undefined
}

const typeText = (i: AircraftInfo | null | undefined) =>
  [i?.typeCode, i?.typeName].filter((v) => v !== undefined).join(' · ') || undefined
const routeText = (r: RouteInfo | null | undefined) =>
  r ? `${r.origin ?? '?'} → ${r.destination ?? '?'}` : undefined

/**
 * detailRows lists every known field of the aircraft, with the user's reference data ref, as [label, value];
 * unknown fields are left out.
 */
export function detailRows(a: Aircraft, ref: Reference = {}): [string, ReactNode][] {
  const rows: [string, ReactNode | undefined][] = [
    ['ICAO', a.icao],
    ['Callsign', a.callsign],
    ['Route', routeText(ref.route)],
    ['Registration', ref.aircraft?.registration],
    ['Aircraft type', typeText(ref.aircraft)],
    ['Operator', ref.aircraft?.operator],
    ['Squawk', a.squawk],
    ['Emergency', a.emergency !== undefined && a.emergency !== 'none' ? a.emergency : undefined],
    ['Country', a.country],
    ['Category', categoryText(a.category)],
    ['Transponder', a.adsb ? `ADS-B${a.adsbVersion === undefined ? '' : ` v${a.adsbVersion}`}` : 'Mode S only'],
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

/**
 * useReference loads the user's entries for icao and callsign when enabled, again only when either changes, and
 * returns them with setters for saved values.
 */
function useReference(icao: string, callsign: string | undefined, enabled: boolean) {
  const [info, setInfo] = useState<{ key: string; v: AircraftInfo | null } | null>(null)
  const [route, setRoute] = useState<{ key: string; v: RouteInfo | null } | null>(null)
  useEffect(() => {
    if (!enabled) {
      return
    }
    const ctl = new AbortController()
    getReference<AircraftInfo>(aircraftInfoUrl(icao), ctl.signal)
      .then((v) => {
        setInfo({ key: icao, v })
      })
      .catch(() => undefined)
    return () => {
      ctl.abort()
    }
  }, [icao, enabled])
  useEffect(() => {
    if (!enabled || callsign === undefined) {
      return
    }
    const ctl = new AbortController()
    getReference<RouteInfo>(routeUrl(callsign), ctl.signal)
      .then((v) => {
        setRoute({ key: callsign, v })
      })
      .catch(() => undefined)
    return () => {
      ctl.abort()
    }
  }, [callsign, enabled])
  const current: Reference = {
    aircraft: info?.key === icao ? info.v : undefined,
    route: callsign !== undefined && route?.key === callsign ? route.v : undefined,
  }
  const saved = (a: AircraftInfo | null, r: RouteInfo | null) => {
    setInfo({ key: icao, v: a })
    if (callsign !== undefined) {
      setRoute({ key: callsign, v: r })
    }
  }
  return [current, saved] as const
}

const AIRCRAFT_FIELDS = [
  ['registration', 'Registration', 'PH-BXA'],
  ['typeCode', 'Type code', 'B738'],
  ['typeName', 'Type name', 'Boeing 737-800'],
  ['operator', 'Operator', 'KLM'],
] as const
const ROUTE_FIELDS = [
  ['origin', 'From', 'EHAM Amsterdam'],
  ['destination', 'To', 'LEMD Madrid'],
] as const

/**
 * ReferenceEditor edits the user's entries: the airframe by ICAO address and the route by callsign (only when the
 * aircraft has one). Emptying every field of an entry deletes it.
 */
function ReferenceEditor({
  icao,
  callsign,
  current,
  onSaved,
  onClose,
}: {
  icao: string
  callsign: string | undefined
  current: Reference
  onSaved: (a: AircraftInfo | null, r: RouteInfo | null) => void
  onClose: () => void
}) {
  const [aircraft, setAircraft] = useState<Record<string, string>>(() =>
    Object.fromEntries(AIRCRAFT_FIELDS.map(([k]) => [k, current.aircraft?.[k] ?? ''])),
  )
  const [route, setRoute] = useState<Record<string, string>>(() =>
    Object.fromEntries(ROUTE_FIELDS.map(([k]) => [k, current.route?.[k] ?? ''])),
  )
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const submit = (e: SyntheticEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    void Promise.all([
      putReference<AircraftInfo>(aircraftInfoUrl(icao), aircraft),
      callsign === undefined ? Promise.resolve(null) : putReference<RouteInfo>(routeUrl(callsign), route),
    ])
      .then(([a, r]) => {
        onSaved(a, r)
        onClose()
      })
      .catch((err: unknown) => {
        setError(err instanceof Error ? err.message : 'save failed')
        setBusy(false)
      })
  }
  const input = (
    values: Record<string, string>,
    set: (v: Record<string, string>) => void,
    [key, label, placeholder]: readonly [string, string, string],
  ) => (
    <label key={key}>
      <span>{label}</span>
      <input
        value={values[key] ?? ''}
        placeholder={placeholder}
        maxLength={64}
        onChange={(e) => {
          set({ ...values, [key]: e.target.value })
        }}
      />
    </label>
  )
  return (
    <form className="detail-form" aria-label="Edit reference data" onSubmit={submit}>
      <fieldset disabled={busy}>
        <legend>Aircraft {icao}</legend>
        {AIRCRAFT_FIELDS.map((f) => input(aircraft, setAircraft, f))}
      </fieldset>
      {callsign === undefined ? (
        <p className="detail-form-note">The route is stored by callsign, which this aircraft hasn't sent yet.</p>
      ) : (
        <fieldset disabled={busy}>
          <legend>Route {callsign}</legend>
          {ROUTE_FIELDS.map((f) => input(route, setRoute, f))}
        </fieldset>
      )}
      {error !== null && <p className="detail-form-error">{error}</p>}
      <div className="detail-form-buttons">
        <button type="submit" disabled={busy}>
          Save
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </form>
  )
}

/** Moment is a playback position for links. */
export interface Moment {
  t: string
  speed: number
}

/**
 * DetailPanel shows all known fields of the selected aircraft; moment, in playback, goes into "Copy link". With
 * reference (the server keeps history), it also shows and edits the user's registration, type and route.
 */
export function DetailPanel({
  aircraft,
  dispatch,
  moment,
  reference = false,
}: {
  aircraft: Aircraft
  dispatch: Dispatch<Action>
  moment?: Moment | undefined
  reference?: boolean
}) {
  const [current, saved] = useReference(aircraft.icao, aircraft.callsign, reference)
  const [editing, setEditing] = useState<string | null>(null)
  const editKey = `${aircraft.icao}/${aircraft.callsign ?? ''}`
  return (
    <aside className={`detail${isEmergency(aircraft) ? ' emergency' : ''}`} aria-label="Aircraft details">
      <header className="detail-head">
        <h2>{aircraft.callsign ?? aircraft.icao}</h2>
        <CopyLinkButton moment={moment} />
        {reference && (
          <button
            type="button"
            className="detail-copy detail-edit"
            title="Enter the registration, type and route"
            aria-pressed={editing === editKey}
            onClick={() => {
              setEditing(editing === editKey ? null : editKey)
            }}
          >
            Edit
          </button>
        )}
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
      {editing === editKey && (
        <ReferenceEditor
          key={editKey}
          icao={aircraft.icao}
          callsign={aircraft.callsign}
          current={current}
          onSaved={saved}
          onClose={() => {
            setEditing(null)
          }}
        />
      )}
      <dl className="detail-list">
        {detailRows(aircraft, current).map(([label, value]) => (
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
