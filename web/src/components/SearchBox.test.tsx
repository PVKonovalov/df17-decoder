import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AircraftMatch } from '../api/history'
import type { Aircraft } from '../api/types'
import { liveMatches, matchesQuery, SEARCH_DEBOUNCE_MS, SearchBox, searchUrl } from './SearchBox'

const ac = (icao: string, callsign?: string, lastSeen = '2026-11-20T12:00:00Z'): Aircraft => ({
  icao,
  ...(callsign !== undefined && { callsign }),
  firstSeen: lastSeen,
  lastSeen,
  messages: 1,
  messagesByDf: { '17': 1 },
  hasPosition: false,
  adsb: true,
})

const onMap = new Map([
  ['AB12CD', ac('AB12CD', 'FAKE101')],
  ['C0FFEE', ac('C0FFEE', undefined, '2026-11-20T12:00:05Z')],
])

const match = (icao: string, live: boolean, lastCallsign?: string): AircraftMatch => ({
  icao,
  ...(lastCallsign !== undefined && { lastCallsign }),
  callsigns: lastCallsign === undefined ? [] : [lastCallsign],
  lastSeen: '2026-11-19T08:00:00Z',
  flights: 3,
  adsb: true,
  live,
})

let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  vi.useFakeTimers()
  fetchMock = vi.fn(() =>
    Promise.resolve(new Response(JSON.stringify([match('AB12CD', true, 'FAKE101'), match('AB34EF', false, 'FAKE777')]))),
  )
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

const box = () => screen.getByRole('combobox')

async function typeAndWait(text: string) {
  fireEvent.focus(box())
  fireEvent.change(box(), { target: { value: text } })
  await act(async () => {
    await vi.advanceTimersByTimeAsync(SEARCH_DEBOUNCE_MS)
  })
}

describe('matchesQuery', () => {
  it('matches hex prefixes and callsign prefixes or substrings from 3 characters', () => {
    expect(matchesQuery('AB', 'AB12CD', undefined)).toBe(true)
    expect(matchesQuery('12', 'AB12CD', undefined)).toBe(false)
    expect(matchesQuery('FA', 'AB12CD', 'FAKE101')).toBe(true)
    expect(matchesQuery('KE1', 'AB12CD', 'FAKE101')).toBe(true)
    expect(matchesQuery('KE', 'AB12CD', 'FAKE101')).toBe(false)
  })

  it('searches the map newest first', () => {
    expect(liveMatches(onMap, 'c').map((m) => m.icao)).toEqual(['C0FFEE'])
    expect(liveMatches(onMap, 'fake')[0]).toMatchObject({ icao: 'AB12CD', live: true, lastCallsign: 'FAKE101' })
  })
})

describe('SearchBox', () => {
  it('searches the history once after the typing pauses', async () => {
    const dispatch = vi.fn()
    render(<SearchBox aircraft={onMap} history dispatch={dispatch} />)
    fireEvent.focus(box())
    for (const v of ['f', 'fa', 'fak']) {
      fireEvent.change(box(), { target: { value: v } })
      await act(async () => {
        await vi.advanceTimersByTimeAsync(SEARCH_DEBOUNCE_MS - 50)
      })
    }
    expect(fetchMock).not.toHaveBeenCalled()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(50)
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0]?.[0]).toBe(searchUrl('fak'))
    expect(screen.getAllByRole('option')).toHaveLength(2)
    expect(screen.getByText('live')).toBeDefined()
  })

  it('moves with the arrow keys, picks with Enter and closes with Esc', async () => {
    const dispatch = vi.fn()
    render(<SearchBox aircraft={onMap} history dispatch={dispatch} />)
    await typeAndWait('fake')
    const opts = () => screen.getAllByRole('option').map((o) => o.getAttribute('aria-selected'))
    expect(opts()).toEqual(['true', 'false'])
    fireEvent.keyDown(box(), { key: 'ArrowDown' })
    expect(opts()).toEqual(['false', 'true'])
    fireEvent.keyDown(box(), { key: 'ArrowDown' })
    expect(opts()).toEqual(['true', 'false'])
    fireEvent.keyDown(box(), { key: 'ArrowUp' })
    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(dispatch).toHaveBeenCalledWith({ type: 'showHistory', icao: 'AB34EF' })
    expect(screen.queryByRole('listbox')).toBeNull()

    fireEvent.focus(box())
    expect(screen.getByRole('listbox')).toBeDefined()
    fireEvent.keyDown(box(), { key: 'Escape' })
    expect(screen.queryByRole('listbox')).toBeNull()
  })

  it('picks a live result with a click', async () => {
    const dispatch = vi.fn()
    render(<SearchBox aircraft={onMap} history dispatch={dispatch} />)
    await typeAndWait('ab')
    fireEvent.click(screen.getAllByRole('option')[0] as HTMLElement)
    expect(dispatch).toHaveBeenCalledWith({ type: 'showHistory', icao: 'AB12CD' })
  })

  it('focuses on "/" unless typing in a field', () => {
    render(
      <>
        <input aria-label="other" />
        <SearchBox aircraft={onMap} history dispatch={vi.fn()} />
      </>,
    )
    const other = screen.getByLabelText('other')
    other.focus()
    fireEvent.keyDown(other, { key: '/' })
    expect(document.activeElement).toBe(other)
    other.blur()
    fireEvent.keyDown(document.body, { key: '/' })
    expect(document.activeElement).toBe(box())
  })

  it('searches only the map without history', async () => {
    const dispatch = vi.fn()
    render(<SearchBox aircraft={onMap} history={false} dispatch={dispatch} />)
    await typeAndWait('ab')
    expect(fetchMock).not.toHaveBeenCalled()
    const opts = screen.getAllByRole('option')
    expect(opts).toHaveLength(1)
    expect(opts[0]?.textContent).not.toContain('flight')
    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(dispatch).toHaveBeenCalledWith({ type: 'showHistory', icao: 'AB12CD' })
  })
})
