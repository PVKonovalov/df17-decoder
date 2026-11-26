import { lazy, Suspense, useEffect, useRef, useState, type Dispatch } from 'react'
import { MAX_TRACK_IDS, type AircraftHistory } from '../api/history'
import type { HistoryFlight } from '../api/types'
import { useAircraftHistory } from '../components/HistoryPanel'
import { useStored } from '../map/prefs'
import type { Action } from '../state/reducer'

const FlightProfile = lazy(() => import('./FlightProfile'))

export const FLIGHTS_PAGE = 20
export const SHOW_LAST = 10

/** flightsUrl is one page of icao's flights, newest first, before the cursor flight when given. */
export function flightsUrl(icao: string, before?: HistoryFlight): string {
  const cursor = before ? `&before=${String(Date.parse(before.firstSeen))}&beforeId=${String(before.id)}` : ''
  return `/api/history/flights?icao=${icao}&limit=${String(FLIGHTS_PAGE)}${cursor}`
}

/** formatDuration gives 1 h 05 min, 12 min or 40 s. */
export function formatDuration(s: number): string {
  if (s < 60) {
    return `${String(Math.max(0, Math.round(s)))} s`
  }
  const m = Math.round(s / 60)
  return m < 60 ? `${String(m)} min` : `${String(Math.floor(m / 60))} h ${String(m % 60).padStart(2, '0')} min`
}

const nm = (v: number | undefined) => (v === undefined ? '—' : v.toFixed(v < 10 ? 1 : 0))
const ft = (v: number | undefined) => (v === undefined ? '—' : `${v.toLocaleString('en-US')} ft`)

/** distanceText gives "closest–furthest nm", or "" when neither is known. */
export function distanceText(min: number | undefined, max: number | undefined): string {
  return min === undefined && max === undefined ? '' : `${nm(min)}–${nm(max)} nm`
}

/** usePages loads icao's flights page by page; more() loads the next one. */
export function useFlightPages(icao: string) {
  const [state, setState] = useState<{ icao: string; flights: HistoryFlight[]; done: boolean; busy: boolean }>({
    icao,
    flights: [],
    done: false,
    busy: true,
  })
  const ctl = useRef<AbortController | null>(null)
  const load = (before: HistoryFlight | undefined) => {
    ctl.current?.abort()
    const c = new AbortController()
    ctl.current = c
    setState((s) => ({ ...s, busy: true }))
    fetch(flightsUrl(icao, before), { signal: c.signal })
      .then((r) => (r.ok ? (r.json() as Promise<HistoryFlight[]>) : Promise.reject(new Error(String(r.status)))))
      .then((page) => {
        setState((s) => ({
          icao,
          flights: before === undefined ? page : [...s.flights, ...page],
          done: page.length < FLIGHTS_PAGE,
          busy: false,
        }))
      })
      .catch(() => {
        if (!c.signal.aborted) {
          setState((s) => ({ ...s, busy: false, done: true }))
        }
      })
  }
  useEffect(() => {
    load(undefined)
    return () => ctl.current?.abort()
  }, [icao])
  const current = state.icao === icao ? state : { icao, flights: [], done: false, busy: true }
  return { ...current, more: () => { load(current.flights.at(-1)) } }
}

/** HistorySummary lists the aircraft's history: all-time counts and the aggregates of the stored flights. */
export function HistorySummary({ h }: { h: AircraftHistory }) {
  const pruned = h.retainedFrom !== undefined && Date.parse(h.firstSeen) < Date.parse(h.retainedFrom)
  const rows: [string, string][] = [
    ['First seen', new Date(h.firstSeen).toLocaleString()],
    ['Last seen', new Date(h.lastSeen).toLocaleString()],
    ['Flights', h.flights.toLocaleString('en-US')],
    ['Days seen', h.daysSeen.toLocaleString('en-US')],
    ['Callsigns', h.callsigns.map((c) => `${c.callsign} (${String(c.flights)})`).join(', ')],
    ['Max altitude', h.maxAlt === undefined ? '' : ft(h.maxAlt)],
    ['Distance', distanceText(h.minDistanceNm, h.maxDistanceNm)],
    [
      'Position quality',
      h.quality === undefined
        ? ''
        : `${String(h.quality.ok)} ok · ${String(h.quality.degraded)} degraded · ${String(h.quality.lost)} lost`,
    ],
  ]
  return (
    <>
      <dl className="detail-list history-summary">
        {rows
          .filter(([, v]) => v !== '')
          .map(([label, v]) => (
            <div key={label}>
              <dt>{label}</dt>
              <dd>{v}</dd>
            </div>
          ))}
      </dl>
      {pruned && h.retainedFrom !== undefined && (
        <p className="history-note">
          Details cover {String(h.retainedFlights)} flights since {new Date(h.retainedFrom).toLocaleDateString()}.
        </p>
      )}
    </>
  )
}

export interface PlayControl {
  /** play seeks the tape to the flight; false when it can't */
  play: (f: HistoryFlight) => boolean
  available: boolean // the server has playback
}

/**
 * HistorySection is the detail panel's History part for icao: the summary, the flights newest first in pages,
 * per flight a track toggle, Play and its profile (a lazy ECharts chunk), and buttons for the last SHOW_LAST tracks, clearing and fitting them. With
 * showLatest, the newest flight's track is turned on once when none is shown.
 */
export function HistorySection({
  icao,
  shown,
  profile = null,
  dispatch,
  playback,
  showLatest = false,
}: {
  icao: string
  shown: readonly number[] // the flight ids on the map (for this aircraft)
  profile?: number | null // the flight whose profile is open
  dispatch: Dispatch<Action>
  playback?: PlayControl | undefined
  showLatest?: boolean
}) {
  const [open, setOpen] = useStored<boolean>('df17.history.open', (raw) => raw !== 'false')
  const summary = useAircraftHistory(icao)
  const pages = useFlightPages(icao)
  const autoShown = useRef<string | null>(null)
  const set = (ids: readonly number[]) => {
    dispatch({ type: 'historyTracks', icao, ids })
  }
  const newest = pages.flights[0]
  useEffect(() => {
    if (showLatest && newest !== undefined && autoShown.current !== icao) {
      autoShown.current = icao
      if (shown.length === 0) {
        dispatch({ type: 'historyTracks', icao, ids: [newest.id] })
      }
    }
  }, [showLatest, newest, icao, shown.length, dispatch])

  const full = shown.length >= MAX_TRACK_IDS
  return (
    <section className="history" aria-label="History">
      <button
        type="button"
        className="history-toggle"
        aria-expanded={open}
        onClick={() => {
          setOpen(!open)
        }}
      >
        History{' '}
        {summary ? (
          <span className="muted">
            · {summary.flights.toLocaleString('en-US')} flight{summary.flights === 1 ? '' : 's'}
          </span>
        ) : null}
      </button>
      {open && (
        <div className="history-body">
          {summary === null && <p className="history-note">No stored flights.</p>}
          {summary && <HistorySummary h={summary} />}
          <div className="history-actions">
            <button
              type="button"
              disabled={pages.flights.length === 0}
              onClick={() => {
                set([...shown, ...pages.flights.slice(0, SHOW_LAST).map((f) => f.id)])
              }}
            >
              Show last {SHOW_LAST} on map
            </button>
            <button
              type="button"
              disabled={shown.length === 0}
              onClick={() => {
                set([])
              }}
            >
              Clear tracks
            </button>
            <button
              type="button"
              disabled={shown.length === 0}
              onClick={() => {
                dispatch({ type: 'fitTracks' })
              }}
            >
              Fit
            </button>
          </div>
          <ul className="history-flights" aria-label="Flights">
            {pages.flights.map((f) => {
              const on = shown.includes(f.id)
              const isOpen = profile === f.id
              const playable = playback?.available === true && f.onTape
              return (
                <li key={f.id} className={[on ? 'shown' : '', isOpen ? 'open' : ''].join(' ').trim() || undefined}>
                  <button
                    type="button"
                    className="history-eye"
                    aria-pressed={on}
                    aria-label={on ? 'Hide the track' : 'Show the track'}
                    disabled={!on && full}
                    title={
                      !on && full
                        ? `At most ${String(MAX_TRACK_IDS)} tracks at a time: hide one first`
                        : on
                          ? 'Hide the track on the map'
                          : 'Show the track on the map'
                    }
                    onClick={() => {
                      set(on ? shown.filter((id) => id !== f.id) : [...shown, f.id])
                    }}
                  >
                    {on ? '◉' : '○'}
                  </button>
                  <span className="history-flight">
                    <span>
                      <strong>{new Date(f.firstSeen).toLocaleString([], { dateStyle: 'short', timeStyle: 'short' })}</strong>{' '}
                      {f.callsign ?? ''}{' '}
                      <span className={f.adsb ? 'tag tag-adsb' : 'tag tag-modes'}>{f.adsb ? 'ADS-B' : 'Mode-S'}</span>
                    </span>
                    <span className="muted">
                      {[formatDuration(f.durationS), f.maxAlt === undefined ? '' : ft(f.maxAlt), distanceText(f.minDistanceNm, f.maxDistanceNm)]
                        .filter((v) => v !== '')
                        .join(' · ')}
                    </span>
                  </span>
                  {playback !== undefined && (
                    <button
                      type="button"
                      className="history-play"
                      disabled={!playable}
                      title={
                        !playback.available
                          ? 'Playback is disabled on the server'
                          : f.onTape
                            ? 'Play this flight from the tape'
                            : 'This flight is no longer on the tape'
                      }
                      onClick={() => {
                        playback.play(f)
                      }}
                    >
                      ▶ Play
                    </button>
                  )}
                  <button
                    type="button"
                    className="history-play"
                    aria-pressed={isOpen}
                    title={isOpen ? 'Close the profile' : 'Altitude and speed over time'}
                    onClick={() => {
                      dispatch({ type: 'historyProfile', icao, id: isOpen ? null : f.id })
                    }}
                  >
                    Profile
                  </button>
                  {isOpen && (
                    <div className="history-profile-row">
                      <Suspense fallback={<p className="history-note">Loading the chart…</p>}>
                        <FlightProfile id={f.id} />
                      </Suspense>
                    </div>
                  )}
                </li>
              )
            })}
          </ul>
          {pages.busy && <p className="history-note">Loading…</p>}
          {!pages.busy && !pages.done && (
            <button type="button" className="history-more" onClick={pages.more}>
              Load more
            </button>
          )}
        </div>
      )}
    </section>
  )
}
