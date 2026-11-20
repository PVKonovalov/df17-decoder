import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Playback, PlaybackControl, Stats } from '../api/types'
import { initialState, type State } from '../state/reducer'
import { Deck } from './Deck'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const now = new Date()
const tape = { tapeRecording: true, tapePaused: false, tapeDropped: 0, tapeWriteErrors: 0, tapeLastFlush: now.toISOString() }
const live: Playback = {
  mode: 'live',
  t: now.toISOString(),
  speed: 1,
  playing: true,
  tapeStart: new Date(now.getTime() - 3_600_000).toISOString(),
  tapeEnd: now.toISOString(),
}
const playback: Playback = { ...live, mode: 'playback', t: new Date(now.getTime() - 600_000).toISOString(), speed: 4 }

const eventT = new Date(now.getTime() - 600_000)
const flightFirst = new Date(now.getTime() - 1_200_000)

function respond(url: string): unknown {
  if (url.startsWith('/api/history/timeline')) {
    const from = Number(new URL(url, 'http://x').searchParams.get('from'))
    return {
      from: new Date(from).toISOString(),
      bucket: '1m0s',
      buckets: Array.from({ length: 200 }, (_, i) => [i % 7, i % 3]),
      events: [{ t: eventT.toISOString(), flightId: 7, icao: '40621D', squawk: '7700' }],
    }
  }
  if (url.startsWith('/api/history/flights')) {
    return [
      { id: 7, icao: '4840D6', callsign: 'KLM1023', adsb: true, firstSeen: flightFirst.toISOString(), lastSeen: now.toISOString(), closed: true },
      { id: 1, icao: '4840D6', callsign: 'KLM1023', adsb: true, firstSeen: '2020-01-01T00:00:00Z', lastSeen: '2020-01-01T01:00:00Z', closed: true },
    ]
  }
  return { spans: [] }
}

function setup(extra: Partial<State>) {
  vi.stubGlobal('fetch', vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(respond(url))))))
  const sent: PlaybackControl[] = []
  const dispatch = vi.fn()
  const state: State = { ...initialState, conn: 'connected', stats: tape as unknown as Stats, ...extra }
  const send = (c: PlaybackControl) => {
    sent.push(c)
    return true
  }
  render(<Deck state={state} send={send} dispatch={dispatch} />)
  return { sent, dispatch }
}

describe('Deck', () => {
  it('shows a grey REC and nothing else without a tape', () => {
    setup({ stats: null })
    expect(screen.getByRole('status').className).toContain('rec-off')
    expect(screen.queryByRole('group', { name: 'Transport' })).toBeNull()
  })

  it('in live mode shows REC, rewind and LIVE, and enters playback with ⏪', () => {
    const { sent } = setup({ playback: live })
    expect(screen.getByRole('status').className).toContain('rec-rec')
    expect(screen.getByRole('button', { name: /LIVE/ }).textContent).toBe('LIVE')
    expect(screen.queryByRole('button', { name: 'Pause' })).toBeNull()
    expect(screen.queryByRole('slider')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Back 10 seconds' }), { shiftKey: true })
    fireEvent.click(screen.getByRole('button', { name: 'Oldest recording' }))
    expect(sent).toEqual([
      { action: 'step', seconds: -60 },
      { action: 'seek', t: live.tapeStart },
    ])
  })

  it('in playback shows the badge, timeline and transport', () => {
    const { sent } = setup({ playback })
    expect(screen.getByText(/PLAYBACK 4×/)).toBeTruthy()
    expect(screen.getByRole('slider', { name: 'Playback position' })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Pause' }))
    fireEvent.click(screen.getByRole('button', { name: /Speed 4×/ }))
    fireEvent.click(screen.getByRole('button', { name: /LIVE/ }))
    expect(sent).toEqual([{ action: 'pause' }, { action: 'speed', speed: 8 }, { action: 'live' }])
  })

  it('handles the keyboard, except while typing', () => {
    const { sent } = setup({ playback })
    fireEvent.keyDown(window, { key: ' ' })
    fireEvent.keyDown(window, { key: 'ArrowRight', shiftKey: true })
    fireEvent.click(screen.getByTitle('Click to go to a date and time'))
    const input = screen.getByLabelText('Go to date and time')
    fireEvent.keyDown(input, { key: 'l' })
    fireEvent.change(input, { target: { value: '2026-11-19T08:00:00' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(sent).toEqual([
      { action: 'pause' },
      { action: 'step', seconds: 60 },
      { action: 'seek', t: new Date(2026, 10, 19, 8, 0, 0).toISOString() },
    ])
  })

  it('shows an amber REC with the reason and server errors', () => {
    const { dispatch } = setup({
      playback: live,
      stats: { ...tape, tapeDropped: 2 } as unknown as Stats,
      playbackError: 'too many playback sessions',
    })
    const rec = screen.getByRole('status')
    expect(rec.className).toContain('rec-warn')
    expect(rec.title).toContain('2 frames dropped')
    expect(screen.getByRole('alert').textContent).toContain('too many playback sessions')
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(dispatch).toHaveBeenCalledWith({ type: 'clearPlaybackError' })
  })
})

describe('Deck with history', () => {
  const withHistory = { ...tape, historyFlightsOpen: 3 } as unknown as Stats

  it('hides the strip and search without history', () => {
    setup({ playback })
    expect(screen.queryByLabelText(/Find a flight/)).toBeNull()
    expect(document.querySelector('.timeline-density')).toBeNull()
  })

  it('draws the density strip with event markers that seek before the event', async () => {
    const { sent } = setup({ playback, stats: withHistory })
    const marker = await screen.findByRole('button', { name: 'Emergency 7700 40621D' })
    expect(document.querySelector('.density-all')?.getAttribute('d')).toMatch(/^M/)
    fireEvent.click(marker)
    expect(sent).toEqual([{ action: 'seek', t: new Date(eventT.getTime() - 30_000).toISOString() }])
  })

  it('finds a flight, seeks to it and selects it after the seek', async () => {
    const { sent, dispatch } = setup({ playback: live, stats: withHistory })
    fireEvent.change(screen.getByLabelText(/Find a flight/), { target: { value: 'klm' } })
    const options = await screen.findAllByRole('option', {}, { timeout: 2000 })
    expect(options).toHaveLength(2)
    const old = options[1]?.querySelector('button')
    expect(old?.disabled).toBe(true) // before the tape starts
    const first = options[0]?.querySelector('button')
    if (!first) {
      throw new Error('no result button')
    }
    fireEvent.click(first)
    await waitFor(() => {
      expect(sent).toEqual([{ action: 'seek', t: new Date(flightFirst.getTime() - 30_000).toISOString() }])
    })
    expect(dispatch).toHaveBeenCalledWith({ type: 'selectAfterSeek', icao: '4840D6' })
  })
})
