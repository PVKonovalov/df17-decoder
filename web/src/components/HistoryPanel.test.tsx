import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import historyFixture from '../api/history.fixture.json'
import { aircraftHistoryUrl, HistoryOnlyPanel, historyRows } from './HistoryPanel'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('HistoryOnlyPanel', () => {
  it('shows the history of an aircraft that is not received', async () => {
    const fetchMock = vi.fn((url: string) =>
      Promise.resolve(
        url.startsWith('/api/history/aircraft/')
          ? new Response(JSON.stringify(historyFixture.aircraft))
          : new Response('{"error":"none"}', { status: 404 }),
      ),
    )
    vi.stubGlobal('fetch', fetchMock)
    const dispatch = vi.fn()
    render(<HistoryOnlyPanel icao="AB12CD" dispatch={dispatch} />)
    expect(await screen.findByText(/Not received now · last seen/)).toBeDefined()
    expect(screen.getByRole('heading').textContent).toBe('FAKE777')
    expect(fetchMock.mock.calls.map(([u]) => u)).toContain(aircraftHistoryUrl('AB12CD'))
    expect(screen.queryByText('Altitude (baro)')).toBeNull()
    fireEvent.click(screen.getByLabelText('Close details'))
    expect(dispatch).toHaveBeenCalledWith({ type: 'clearSelection' })
  })

  it('says when the moment is in playback', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => undefined)))
    render(<HistoryOnlyPanel icao="AB12CD" dispatch={vi.fn()} moment={{ t: '2026-11-20T12:00:00Z', speed: 1 }} />)
    expect(screen.getByText('Not on the map at this moment')).toBeDefined()
  })

  it('lists the summary', () => {
    const rows = Object.fromEntries(historyRows(historyFixture.aircraft))
    expect([rows.ICAO, rows.Callsign, rows.Transponder]).toEqual(['AB12CD', 'FAKE777', 'ADS-B'])
  })
})
