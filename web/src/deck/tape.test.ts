import { describe, expect, it } from 'vitest'
import type { Playback, Stats } from '../api/types'
import {
  formatCounter,
  isTyping,
  keyCommand,
  nextSpeed,
  parseInputValue,
  recState,
  spansOf,
  timelineWindow,
  toInputValue,
} from './tape'

const local = (y: number, mo: number, d: number, h: number, mi: number, s: number) =>
  new Date(y, mo - 1, d, h, mi, s).getTime()

describe('formatCounter', () => {
  it('shows the date only when it is not today', () => {
    const now = local(2026, 11, 20, 15, 0, 0)
    expect(formatCounter(local(2026, 11, 20, 9, 5, 7), now)).toBe('09:05:07')
    expect(formatCounter(local(2026, 11, 19, 23, 59, 59), now)).toBe('2026-11-19 23:59:59')
  })

  it('round-trips the datetime-local value', () => {
    const t = local(2026, 11, 19, 8, 7, 6)
    expect(toInputValue(t)).toBe('2026-11-19T08:07:06')
    expect(parseInputValue(toInputValue(t))).toBe(t)
    expect(parseInputValue('2026-11-19T08:07')).toBe(local(2026, 11, 19, 8, 7, 0))
    expect(parseInputValue('tomorrow')).toBeNaN()
  })
})

it('cycles the speeds', () => {
  expect([1, 2, 4, 8, 16, 60].map(nextSpeed)).toEqual([2, 4, 8, 16, 60, 1])
  expect(nextSpeed(3)).toBe(1)
})

describe('recState', () => {
  const now = Date.parse('2026-11-20T10:00:00Z')
  const source = { url: 'beast://pi:30005', connected: true, reconnects: 0, malformedLines: 0, lastFrame: '2026-11-20T09:59:59Z' }
  const base = {
    serverTime: '2026-11-20T10:00:00Z', source,
    tapeRecording: true, tapePaused: false, tapeDropped: 0, tapeWriteErrors: 0, tapeLastFlush: '2026-11-20T09:59:58Z',
  }
  const st = (extra: Partial<Stats>) => ({ ...base, ...extra }) as Stats

  it('is grey without a tape, red while recording and flushing', () => {
    expect(recState(null, now).state).toBe('off')
    expect(recState({} as Stats, now).state).toBe('off')
    expect(recState(st({}), now).state).toBe('rec')
  })

  it('is amber with the reasons on problems', () => {
    expect(recState(st({ tapePaused: true }), now)).toMatchObject({ state: 'warn', title: expect.stringContaining('disk') as string })
    expect(recState(st({ tapeDropped: 3 }), now).title).toContain('3 frames dropped')
    expect(recState(st({ tapeWriteErrors: 1 }), now).title).toContain('1 write errors')
    expect(recState(st({ tapeLastFlush: '2026-11-20T09:58:00Z' }), now).title).toContain('no recent flush')
    expect(recState(st({ tapeRecording: false }), now).title).toContain('not recording')
    expect(recState(st({ source: { ...source, connected: false } }), now).title).toContain('source is disconnected')
  })

  it('stays red in a quiet sky, and judges ages by the server clock', () => {
    const quiet = recState(st({ source: { ...source, lastFrame: '2026-11-20T09:48:00Z' } }), now)
    expect(quiet).toEqual({ state: 'rec', title: 'Recording; nothing heard for 12 min (a quiet sky?)' })
    expect(recState(st({ source: { url: 'x', connected: true, reconnects: 0, malformedLines: 0 } }), now).title).toContain('first frame')
    // the browser runs 5 minutes ahead of the server: the server's clock decides
    expect(recState(st({}), now + 300_000).state).toBe('rec')
    expect(recState(st({ serverTime: 'bogus' }), now + 300_000).title).toContain('no recent flush')
  })
})

describe('keyCommand', () => {
  const live: Playback = { mode: 'live', t: '2026-11-20T10:00:00Z', speed: 1, playing: true }
  const pb: Playback = { ...live, mode: 'playback' }

  it('maps the shortcuts in playback', () => {
    expect(keyCommand(' ', false, pb)).toEqual({ action: 'pause' })
    expect(keyCommand(' ', false, { ...pb, playing: false })).toEqual({ action: 'play' })
    expect(keyCommand('ArrowLeft', false, pb)).toEqual({ action: 'step', seconds: -10 })
    expect(keyCommand('ArrowRight', true, pb)).toEqual({ action: 'step', seconds: 60 })
    expect(keyCommand('L', false, pb)).toEqual({ action: 'live' })
    expect(keyCommand('x', false, pb)).toBeNull()
  })

  it('only rewinds from live, and does nothing without playback', () => {
    expect(keyCommand('ArrowLeft', true, live)).toEqual({ action: 'step', seconds: -60 })
    for (const k of [' ', 'ArrowRight', 'l']) {
      expect(keyCommand(k, false, live)).toBeNull()
    }
    expect(keyCommand('ArrowLeft', false, null)).toBeNull()
  })

  it('is ignored while typing', () => {
    const input = document.createElement('input')
    const select = document.createElement('select')
    const div = document.createElement('div')
    expect([input, select, div, null].map(isTyping)).toEqual([true, true, false, false])
  })
})

describe('timeline', () => {
  const h = 3_600_000
  it('stretches the last span to the recording end only across a short silence', () => {
    const extent = { spans: [{ from: '2026-11-20T08:00:00Z', to: '2026-11-20T09:00:00Z' }] }
    const end = Date.parse('2026-11-20T09:00:00Z')
    expect(spansOf(extent, end + 30_000).at(-1)?.to).toBe(end + 30_000)
    expect(spansOf(extent, end + 5 * 60_000).at(-1)?.to).toBe(end)
    expect(spansOf(null, end)).toEqual([])
  })

  it('keeps the playhead in a zoomed window within the tape', () => {
    expect(timelineWindow(h, 10 * h, 0, 24 * h)).toEqual([9.5 * h, 10.5 * h])
    expect(timelineWindow(h, 0.1 * h, 0, 24 * h)).toEqual([0, h])
    expect(timelineWindow(h, 23.9 * h, 0, 24 * h)).toEqual([23 * h, 24 * h])
    expect(timelineWindow(Infinity, 5 * h, 0, 24 * h)).toEqual([0, 24 * h])
    expect(timelineWindow(6 * h, 1 * h, 0, 2 * h)).toEqual([0, 2 * h])
  })
})
