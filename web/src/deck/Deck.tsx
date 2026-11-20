import { useEffect, useMemo, useRef, useState, type Dispatch, type PointerEvent } from 'react'
import { parseDuration, type SendControl } from '../api/live'
import type { HistoryFlight, Playback, Timeline as TimelineData } from '../api/types'
import { useStored } from '../map/prefs'
import { densityPaths, flightSeek, timelineQuery } from './history'
import type { Action, State } from '../state/reducer'
import {
  formatCounter,
  formatUtc,
  isTyping,
  keyCommand,
  nextSpeed,
  parseInputValue,
  recState,
  spansOf,
  timelineWindow,
  toInputValue,
  ZOOMS,
  type Span,
  type TapeExtent,
} from './tape'

const ERROR_SHOW_MS = 6000

/**
 * Deck is the tape deck under the header: the ● REC indicator and, when the server offers playback, the transport
 * buttons, the tape counter and (in playback) the PLAYBACK badge and the timeline. It also handles the keyboard
 * shortcuts.
 */
export function Deck({ state, send, dispatch }: { state: State; send: SendControl; dispatch: Dispatch<Action> }) {
  const { playback: pb, stats, playbackError } = state
  const nowMs = useMemo(() => (stats === null ? 0 : Date.now()), [stats])
  const rec = recState(stats, nowMs)
  const history = stats?.historyFlightsOpen !== undefined

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey || e.metaKey || e.altKey || isTyping(e.target)) {
        return
      }
      const ctl = keyCommand(e.key, e.shiftKey, pb)
      if (ctl !== null) {
        e.preventDefault()
        send(ctl)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
    }
  }, [pb, send])

  useEffect(() => {
    if (playbackError === null) {
      return
    }
    const t = setTimeout(() => {
      dispatch({ type: 'clearPlaybackError' })
    }, ERROR_SHOW_MS)
    return () => {
      clearTimeout(t)
    }
  }, [playbackError, dispatch])

  const inPlayback = pb?.mode === 'playback'
  return (
    <div className={`deck${inPlayback ? ' deck-playback' : ''}`} role="toolbar" aria-label="Tape deck">
      <span className={`rec rec-${rec.state}`} title={rec.title} role="status">
        ● REC
      </span>
      {pb !== null && <Transport pb={pb} send={send} />}
      {pb !== null && <Counter pb={pb} nowMs={serverNowMs(stats, nowMs)} send={send} />}
      {inPlayback && (
        <span className="deck-badge" aria-live="polite">
          PLAYBACK {pb.speed}×{pb.playing ? '' : ' ‖'}
        </span>
      )}
      {inPlayback && <Timeline pb={pb} send={send} history={history} />}
      {pb !== null && history && <FlightSearch pb={pb} send={send} dispatch={dispatch} nowMs={nowMs} />}
      {playbackError !== null && (
        <span className="deck-error" role="alert">
          {playbackError}
          <button
            type="button"
            aria-label="Dismiss"
            onClick={() => {
              dispatch({ type: 'clearPlaybackError' })
            }}
          >
            ×
          </button>
        </span>
      )}
    </div>
  )
}

function Transport({ pb, send }: { pb: Playback; send: SendControl }) {
  const inPlayback = pb.mode === 'playback'
  return (
    <span className="deck-transport" role="group" aria-label="Transport">
      <button
        type="button"
        title="Oldest recording"
        aria-label="Oldest recording"
        disabled={pb.tapeStart === undefined}
        onClick={() => {
          if (pb.tapeStart !== undefined) {
            send({ action: 'seek', t: pb.tapeStart })
          }
        }}
      >
        ⏮
      </button>
      <button
        type="button"
        title="Back 10 s (Shift: 1 min) · ←"
        aria-label="Back 10 seconds"
        onClick={(e) => {
          send({ action: 'step', seconds: e.shiftKey ? -60 : -10 })
        }}
      >
        ⏪
      </button>
      {inPlayback && (
        <>
          <button
            type="button"
            className={pb.playing ? 'deck-pause' : 'deck-play'}
            title={`${pb.playing ? 'Pause' : 'Play'} · Space`}
            aria-label={pb.playing ? 'Pause' : 'Play'}
            onClick={() => {
              send({ action: pb.playing ? 'pause' : 'play' })
            }}
          >
            {pb.playing ? '⏸' : '▶'}
          </button>
          <button
            type="button"
            title="Faster (1×, 2×, 4×, 8×, 16×, 60×)"
            aria-label={`Speed ${pb.speed}×, next ${nextSpeed(pb.speed)}×`}
            onClick={() => {
              send({ action: 'speed', speed: nextSpeed(pb.speed) })
            }}
          >
            ⏩ <span className="deck-speed">{pb.speed}×</span>
          </button>
          <button
            type="button"
            className="deck-live"
            title="Back to live · L"
            onClick={() => {
              send({ action: 'live' })
            }}
          >
            ⏭ LIVE
          </button>
        </>
      )}
    </span>
  )
}

/** serverNowMs is the server's clock from the last stats (serverTime), else fallback. */
function serverNowMs(stats: State['stats'], fallback: number): number {
  const t = stats === null ? NaN : Date.parse(stats.serverTime)
  return Number.isNaN(t) ? fallback : t
}

/**
 * Counter shows the playback time, or "LIVE" and the server's time, in local time with UTC beneath; a click opens
 * a local date and time field that seeks.
 */
function Counter({ pb, nowMs, send }: { pb: Playback; nowMs: number; send: SendControl }) {
  const [editing, setEditing] = useState<string | null>(null)
  const tMs = Date.parse(pb.t)
  const gap = pb.gapSkipped
  if (editing !== null) {
    const submit = () => {
      const ms = parseInputValue(editing)
      setEditing(null)
      if (!Number.isNaN(ms)) {
        send({ action: 'seek', t: new Date(ms).toISOString() })
      }
    }
    return (
      <input
        className="deck-counter-input"
        type="datetime-local"
        step={1}
        aria-label="Go to date and time"
        value={editing}
        autoFocus
        onChange={(e) => {
          setEditing(e.target.value)
        }}
        onBlur={() => {
          setEditing(null)
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            submit()
          } else if (e.key === 'Escape') {
            setEditing(null)
          }
        }}
      />
    )
  }
  return (
    <button
      type="button"
      className={`deck-counter${gap ? ' gap' : ''}`}
      title={
        gap
          ? `Skipped ${Math.round((Date.parse(gap.to) - Date.parse(gap.from)) / 60_000)} min without recording`
          : 'Click to go to a date and time'
      }
      onClick={() => {
        setEditing(toInputValue(pb.mode === 'live' ? nowMs : tMs))
      }}
    >
      <span className="deck-counter-local">
        {pb.mode === 'live' ? `LIVE ${formatCounter(nowMs, nowMs)}` : formatCounter(tMs, nowMs)}
      </span>
      <span className="deck-counter-utc">{formatUtc(pb.mode === 'live' ? nowMs : tMs, nowMs)}</span>
    </button>
  )
}

/**
 * useExtent fetches GET /api/tape/extent once when the deck enters playback and again when the recording has grown
 * past what it knows by more than a minute (no polling: it follows the pushed tapeEnd).
 */
function useExtent(tapeEndMs: number): TapeExtent | null {
  const [extent, setExtent] = useState<TapeExtent | null>(null)
  const known = extent?.newest === undefined ? -Infinity : Date.parse(extent.newest)
  const stale = tapeEndMs - known > 60_000
  useEffect(() => {
    if (!stale) {
      return
    }
    const ctl = new AbortController()
    fetch('/api/tape/extent', { signal: ctl.signal })
      .then((r) => (r.ok ? (r.json() as Promise<TapeExtent>) : null))
      .then((e) => {
        if (e !== null) {
          setExtent(e)
        }
      })
      .catch(() => {
        // aborted or offline: the next tapeEnd change retries
      })
    return () => {
      ctl.abort()
    }
  }, [stale])
  return extent
}

function Timeline({ pb, send, history }: { pb: Playback; send: SendControl; history: boolean }) {
  const tapeEnd = pb.tapeEnd === undefined ? Date.parse(pb.t) : Date.parse(pb.tapeEnd)
  const extent = useExtent(tapeEnd)
  const [zoom, setZoom] = useStored<string>('df17.tapeZoom', (raw) => ZOOMS.find((z) => z.label === raw)?.label ?? '1h')
  const zoomMs = ZOOMS.find((z) => z.label === zoom)?.ms ?? Infinity
  const tMs = Date.parse(pb.t)
  const start = pb.tapeStart === undefined ? tMs : Date.parse(pb.tapeStart)
  const end = Math.max(tapeEnd, tMs)
  const [drag, setDrag] = useState<number | null>(null)
  const [from, to] = timelineWindow(zoomMs, drag ?? tMs, start, end)
  const spans = spansOf(extent, tapeEnd)
  const track = useRef<HTMLDivElement>(null)

  const at = (e: PointerEvent) => {
    const r = track.current?.getBoundingClientRect()
    if (r === undefined || r.width === 0) {
      return tMs
    }
    const x = Math.min(Math.max((e.clientX - r.left) / r.width, 0), 1)
    return Math.min(Math.max(from + x * (to - from), start), end)
  }
  const pct = (ms: number) => `${(((ms - from) / (to - from)) * 100).toFixed(3)}%`
  const head = drag ?? tMs

  return (
    <span className="deck-timeline">
      <span className="deck-time-label">{formatCounter(from, tMs)}</span>
      <span className="timeline-stack">
        {history && <Density from={from} to={to} send={send} tapeStart={pb.tapeStart} />}
        <div
        ref={track}
        className="timeline-track"
        role="slider"
        aria-label="Playback position"
        aria-valuemin={from}
        aria-valuemax={to}
        aria-valuenow={head}
        aria-valuetext={formatCounter(head, tMs)}
        tabIndex={-1}
        onPointerDown={(e) => {
          e.currentTarget.setPointerCapture(e.pointerId)
          setDrag(at(e))
        }}
        onPointerMove={(e) => {
          if (drag !== null) {
            setDrag(at(e))
          }
        }}
        onPointerUp={(e) => {
          if (drag !== null) {
            send({ action: 'seek', t: new Date(at(e)).toISOString() })
            setDrag(null)
          }
        }}
        onPointerCancel={() => {
          setDrag(null)
        }}
      >
        {visible(spans, from, to).map((s) => (
          <span key={s.from} className="timeline-span" style={{ left: pct(s.from), width: pct(from + s.to - s.from) }} />
        ))}
        <span className={`timeline-head${drag === null ? '' : ' dragging'}`} style={{ left: pct(head) }} />
        </div>
      </span>
      <span className="deck-time-label">{formatCounter(to, tMs)}</span>
      <span className="deck-zoom" role="group" aria-label="Timeline zoom">
        {ZOOMS.map((z) => (
          <button
            key={z.label}
            type="button"
            aria-pressed={z.label === zoom}
            onClick={() => {
              setZoom(z.label)
            }}
          >
            {z.label}
          </button>
        ))}
      </span>
    </span>
  )
}

/** visible clips spans to [from, to], dropping those outside. */
function visible(spans: Span[], from: number, to: number): Span[] {
  return spans
    .filter((s) => s.to >= from && s.from <= to)
    .map((s) => ({ from: Math.max(s.from, from), to: Math.min(s.to, to) }))
}

const STRIP_H = 16

/** useJSON fetches url (null: nothing) and keeps the last good response; failures keep the previous one. */
function useJSON(url: string | null): unknown {
  const [data, setData] = useState<{ url: string; value: unknown } | null>(null)
  useEffect(() => {
    if (url === null) {
      return
    }
    const ctl = new AbortController()
    fetch(url, { signal: ctl.signal })
      .then((r) => (r.ok ? (r.json() as Promise<unknown>) : null))
      .then((value) => {
        if (value !== null) {
          setData({ url, value })
        }
      })
      .catch(() => {
        // aborted or offline: keep what is shown
      })
    return () => {
      ctl.abort()
    }
  }, [url])
  return data?.value ?? null
}

/**
 * Density is the traffic strip over the timeline (flights per bucket from the history database, ADS-B darker) with
 * the emergency events as markers that seek to just before them.
 */
function Density({
  from,
  to,
  send,
  tapeStart,
}: {
  from: number
  to: number
  send: SendControl
  tapeStart: string | undefined
}) {
  const q = timelineQuery(from, to)
  const tl = useJSON(`/api/history/timeline?from=${q.from}&to=${q.to}&bucket=${q.bucketMs / 1000}s`) as TimelineData | null
  const bucketMs = tl === null ? q.bucketMs : parseDuration(tl.bucket)
  const paths = tl === null ? null : densityPaths(tl, bucketMs, from, to, STRIP_H)
  const events = (tl?.events ?? []).filter((e) => {
    const t = Date.parse(e.t)
    return t >= from && t <= to
  })
  return (
    <span className="timeline-density" title={paths ? `Flights per ${tl?.bucket ?? ''}, up to ${paths.peak}` : undefined}>
      <svg viewBox={`0 0 1000 ${STRIP_H}`} preserveAspectRatio="none" aria-hidden="true">
        {paths && <path className="density-all" d={paths.all} />}
        {paths && <path className="density-adsb" d={paths.adsb} />}
      </svg>
      {events.map((e) => (
        <button
          key={`${e.flightId}-${e.squawk}`}
          type="button"
          className="timeline-event"
          style={{ left: `${(((Date.parse(e.t) - from) / (to - from)) * 100).toFixed(3)}%` }}
          title={`${e.squawk} ${e.callsign ?? e.icao} at ${formatCounter(Date.parse(e.t), to)}`}
          aria-label={`Emergency ${e.squawk} ${e.callsign ?? e.icao}`}
          onClick={() => {
            const t = flightSeek(e.t, e.t, tapeStart)
            if (t !== null) {
              send({ action: 'seek', t })
            }
          }}
        />
      ))}
    </span>
  )
}

const SEARCH_DEBOUNCE_MS = 300

/** FlightSearch finds stored flights by callsign or ICAO; picking one seeks to just before it and selects it. */
function FlightSearch({
  pb,
  send,
  dispatch,
  nowMs,
}: {
  pb: Playback
  send: SendControl
  dispatch: Dispatch<Action>
  nowMs: number
}) {
  const [text, setText] = useState('')
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  useEffect(() => {
    const t = setTimeout(() => {
      setQuery(text.trim())
    }, SEARCH_DEBOUNCE_MS)
    return () => {
      clearTimeout(t)
    }
  }, [text])
  const results = useJSON(
    query.length >= 2 ? `/api/history/flights?q=${encodeURIComponent(query)}&limit=8` : null,
  ) as HistoryFlight[] | null
  const pick = (f: HistoryFlight) => {
    const t = flightSeek(f.firstSeen, f.lastSeen, pb.tapeStart)
    if (t !== null && send({ action: 'seek', t })) {
      dispatch({ type: 'selectAfterSeek', icao: f.icao })
      setOpen(false)
    }
  }
  return (
    <span className="deck-search">
      <input
        type="search"
        placeholder="Find a flight"
        aria-label="Find a flight by callsign or ICAO"
        maxLength={16}
        value={text}
        onChange={(e) => {
          setText(e.target.value)
          setOpen(true)
        }}
        onFocus={() => {
          setOpen(true)
        }}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            setOpen(false)
          }
        }}
      />
      {open && query.length >= 2 && results !== null && (
        <ul className="deck-results" role="listbox" aria-label="Flights">
          {results.length === 0 && <li className="muted">No flights</li>}
          {results.map((f) => {
            const onTape = flightSeek(f.firstSeen, f.lastSeen, pb.tapeStart) !== null
            return (
              <li key={f.id} role="option" aria-selected={false}>
                <button
                  type="button"
                  disabled={!onTape}
                  title={onTape ? 'Wind the tape to this flight' : 'This flight is no longer on the tape'}
                  onClick={() => {
                    pick(f)
                  }}
                >
                  <strong>{f.callsign ?? f.icao}</strong> <span className="muted">{f.icao}</span>{' '}
                  {formatCounter(Date.parse(f.firstSeen), nowMs)}
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </span>
  )
}
