import { PLAYBACK_SPEEDS, type Playback, type PlaybackControl, type Stats } from '../api/types'

const pad = (n: number) => String(n).padStart(2, '0')

/** localDate returns ms as local "YYYY-MM-DD". */
function localDate(ms: number): string {
  const d = new Date(ms)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** localTime returns ms as local "HH:MM:SS". */
function localTime(ms: number): string {
  const d = new Date(ms)
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** formatCounter returns the tape counter text for ms: local time, with the date when it isn't nowMs's day. */
export function formatCounter(ms: number, nowMs: number): string {
  const date = localDate(ms)
  return date === localDate(nowMs) ? localTime(ms) : `${date} ${localTime(ms)}`
}

/** toInputValue returns ms as a datetime-local input value with seconds. */
export function toInputValue(ms: number): string {
  return `${localDate(ms)}T${localTime(ms)}`
}

/** parseInputValue reads a datetime-local value ("YYYY-MM-DDTHH:MM[:SS]", local time); NaN when invalid. */
export function parseInputValue(v: string): number {
  return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/.test(v) ? new Date(v).getTime() : NaN
}

/** nextSpeed returns the speed after speed in the ⏩ cycle 1, 2, 4, 8, 16, 60, 1, … */
export function nextSpeed(speed: number): (typeof PLAYBACK_SPEEDS)[number] {
  const i = PLAYBACK_SPEEDS.findIndex((s) => s === speed)
  return PLAYBACK_SPEEDS[(i + 1) % PLAYBACK_SPEEDS.length] ?? 1
}

/** REC_FLUSH_STALE_MS is how old the last tape flush may be for the indicator to stay red. */
export const REC_FLUSH_STALE_MS = 30_000

export type RecState = 'off' | 'rec' | 'warn'

/**
 * recState returns the ● REC indicator: off (grey) when the tape is disabled, warn (amber, with the reasons) when
 * recording is paused or not flushing or frames were dropped or failed to write, else rec (red).
 */
export function recState(stats: Stats | null, nowMs: number): { state: RecState; title: string } {
  if (stats?.tapeRecording === undefined) {
    return { state: 'off', title: 'The tape is disabled (tape.enabled)' }
  }
  const reasons: string[] = []
  if (stats.tapePaused === true) {
    reasons.push('paused: free disk space is below tape.minFreeMB')
  } else if (!stats.tapeRecording) {
    reasons.push('not recording')
  }
  const flushed = stats.tapeLastFlush === undefined ? NaN : Date.parse(stats.tapeLastFlush)
  if (stats.tapeRecording && !(nowMs - flushed <= REC_FLUSH_STALE_MS)) {
    reasons.push('no recent flush to disk')
  }
  if ((stats.tapeDropped ?? 0) > 0) {
    reasons.push(`${stats.tapeDropped} frames dropped (queue full)`)
  }
  if ((stats.tapeWriteErrors ?? 0) > 0) {
    reasons.push(`${stats.tapeWriteErrors} write errors`)
  }
  return reasons.length > 0
    ? { state: 'warn', title: `Recording problem: ${reasons.join('; ')}` }
    : { state: 'rec', title: 'Recording every received frame to the tape' }
}

/** isTyping reports whether keyboard input goes to a form field, where the deck's shortcuts don't apply. */
export function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false
  }
  return target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)
}

/**
 * keyCommand maps a key to a playback command: Space play/pause (playback only), ←/→ ∓10 s (Shift: 1 min; ← also
 * enters playback from live), L live. Other keys give null.
 */
export function keyCommand(key: string, shift: boolean, pb: Playback | null): PlaybackControl | null {
  if (pb === null) {
    return null
  }
  const step = shift ? 60 : 10
  const playing = pb.mode === 'playback'
  switch (key) {
    case ' ':
      return playing ? { action: pb.playing ? 'pause' : 'play' } : null
    case 'ArrowLeft':
      return { action: 'step', seconds: -step }
    case 'ArrowRight':
      return playing ? { action: 'step', seconds: step } : null
    case 'l':
    case 'L':
      return playing ? { action: 'live' } : null
    default:
      return null
  }
}

export interface Span {
  from: number // ms
  to: number // ms
}

/** TapeExtent is GET /api/tape/extent. */
export interface TapeExtent {
  oldest?: string
  newest?: string
  spans: { from: string; to: string }[]
}

/** GAP_MS is the silence the server splits spans at (tape.GapThreshold). */
export const GAP_MS = 60_000

/** spansOf returns the extent's spans in ms, with the last one stretched to tapeEndMs when that is within a gap. */
export function spansOf(extent: TapeExtent | null, tapeEndMs: number): Span[] {
  const spans = (extent?.spans ?? []).map((s) => ({ from: Date.parse(s.from), to: Date.parse(s.to) }))
  const last = spans.at(-1)
  if (last !== undefined && tapeEndMs > last.to) {
    if (tapeEndMs - last.to <= GAP_MS) {
      last.to = tapeEndMs
    }
  }
  return spans
}

export const ZOOMS = [
  { label: '1h', ms: 3_600_000 },
  { label: '6h', ms: 6 * 3_600_000 },
  { label: '1d', ms: 24 * 3_600_000 },
  { label: 'All', ms: Infinity },
] as const

/**
 * timelineWindow returns the [from, to] ms the timeline shows: the whole tape for an infinite zoom, otherwise
 * zoomMs around the playhead tMs, shifted to stay within [startMs, endMs].
 */
export function timelineWindow(zoomMs: number, tMs: number, startMs: number, endMs: number): [number, number] {
  const total = Math.max(endMs - startMs, 1000)
  if (!(zoomMs < total)) {
    return [startMs, startMs + total]
  }
  let from = tMs - zoomMs / 2
  from = Math.min(Math.max(from, startMs), endMs - zoomMs)
  return [from, from + zoomMs]
}
