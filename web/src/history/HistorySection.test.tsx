import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import historyFixture from '../api/history.fixture.json'
import type { HistoryFlight } from '../api/types'
import { flightsUrl, formatDuration, HistorySection } from './HistorySection'

const T0 = Date.parse('2026-11-20T12:00:00Z')
const flight = (id: number, extra: Partial<HistoryFlight> = {}): HistoryFlight => ({
  ...historyFixture.flight,
  id,
  firstSeen: new Date(T0 - id * 3_600_000).toISOString(),
  lastSeen: new Date(T0 - id * 3_600_000 + 600_000).toISOString(),
  onTape: id % 2 === 1,
  ...extra,
})
const page = (from: number, n: number) => Array.from({ length: n }, (_, i) => flight(from + i))

let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  fetchMock = vi.fn((url: string) => {
    if (url.startsWith('/api/history/aircraft/')) {
      return Promise.resolve(new Response(JSON.stringify(historyFixture.aircraft)))
    }
    const before = new URL(url, 'http://x').searchParams.get('beforeId')
    return Promise.resolve(new Response(JSON.stringify(before === null ? page(1, 20) : page(21, 5))))
  })
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  localStorage.clear()
})

const rows = () => screen.getAllByRole('listitem').filter((li) => li.closest('.history-flights') !== null)

describe('HistorySection', () => {
  it('formats durations', () => {
    expect([formatDuration(40), formatDuration(720), formatDuration(3900)]).toEqual(['40 s', '12 min', '1 h 05 min'])
  })

  it('shows the summary and pages the flights with the cursor of the last one', async () => {
    render(<HistorySection icao="AB12CD" shown={[]} dispatch={vi.fn()} />)
    expect(await screen.findByText(/Details cover 12 flights since/)).toBeDefined()
    expect(await screen.findAllByLabelText('Show the track')).toHaveLength(20)
    fireEvent.click(screen.getByText('Load more'))
    await waitFor(() => {
      expect(rows()).toHaveLength(25)
    })
    const last = flight(20)
    expect(fetchMock.mock.calls.map(([u]) => String(u))).toContain(flightsUrl('AB12CD', last))
    expect(flightsUrl('AB12CD', last)).toContain(`before=${String(Date.parse(last.firstSeen))}&beforeId=20`)
    expect(screen.queryByText('Load more')).toBeNull()
  })

  it('toggles tracks, shows the last 10 and stops at 20', async () => {
    const dispatch = vi.fn()
    const { rerender } = render(<HistorySection icao="AB12CD" shown={[2]} dispatch={dispatch} />)
    await screen.findAllByLabelText('Show the track')
    fireEvent.click(screen.getAllByLabelText('Show the track')[0] as HTMLElement)
    expect(dispatch).toHaveBeenLastCalledWith({ type: 'historyTracks', icao: 'AB12CD', ids: [2, 1] })
    fireEvent.click(screen.getByLabelText('Hide the track'))
    expect(dispatch).toHaveBeenLastCalledWith({ type: 'historyTracks', icao: 'AB12CD', ids: [] })
    fireEvent.click(screen.getByText('Show last 10 on map'))
    expect(dispatch).toHaveBeenLastCalledWith({ type: 'historyTracks', icao: 'AB12CD', ids: [2, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10] })

    const full = Array.from({ length: 20 }, (_, i) => i + 101)
    rerender(<HistorySection icao="AB12CD" shown={full} dispatch={dispatch} />)
    const off = screen.getAllByLabelText('Show the track')[0] as HTMLButtonElement
    expect(off.disabled).toBe(true)
    expect(off.title).toContain('At most 20 tracks')
    fireEvent.click(screen.getByText('Fit'))
    expect(dispatch).toHaveBeenLastCalledWith({ type: 'fitTracks' })
  })

  it('plays flights still on the tape', async () => {
    const play = vi.fn(() => true)
    render(<HistorySection icao="AB12CD" shown={[]} dispatch={vi.fn()} playback={{ available: true, play }} />)
    const buttons = await screen.findAllByText<HTMLButtonElement>('▶ Play')
    expect([buttons[0]?.disabled, buttons[1]?.disabled]).toEqual([false, true])
    expect(buttons[1]?.title).toBe('This flight is no longer on the tape')
    fireEvent.click(buttons[0] as HTMLElement)
    expect(play).toHaveBeenCalledWith(expect.objectContaining({ id: 1 }))
  })

  it('shows the newest track once for a history-only aircraft', async () => {
    const dispatch = vi.fn()
    const { rerender } = render(<HistorySection icao="AB12CD" shown={[]} dispatch={dispatch} showLatest />)
    await screen.findAllByLabelText('Show the track')
    expect(dispatch).toHaveBeenCalledWith({ type: 'historyTracks', icao: 'AB12CD', ids: [1] })
    rerender(<HistorySection icao="AB12CD" shown={[]} dispatch={dispatch} showLatest />)
    await act(async () => {
      await Promise.resolve()
    })
    expect(dispatch.mock.calls.filter(([a]) => (a as { type: string }).type === 'historyTracks')).toHaveLength(1)
  })

  it('collapses and remembers it', async () => {
    render(<HistorySection icao="AB12CD" shown={[]} dispatch={vi.fn()} />)
    await screen.findAllByLabelText('Show the track')
    fireEvent.click(screen.getByRole('button', { name: /History/ }))
    expect(screen.queryByLabelText('Flights')).toBeNull()
    expect(localStorage.getItem('df17.history.open')).toBe('false')
  })
})
