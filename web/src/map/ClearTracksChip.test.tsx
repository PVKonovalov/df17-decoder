import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { Dispatch } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Stats } from '../api/types'
import { App } from '../App'
import type { Action } from '../state/reducer'
import { ClearTracksChip } from './LiveMap'

let dispatch: Dispatch<Action> | undefined
vi.mock('../api/live', () => ({
  useLiveData: (d: Dispatch<Action>) => {
    dispatch = d
    return () => true
  },
}))
vi.mock('../deck/Deck', () => ({ Deck: () => null }))
vi.mock('./HistoryTrackLayer', () => ({ HistoryTrackLayer: () => null }))

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const act$ = (a: Action) => {
  act(() => {
    dispatch?.(a)
  })
}

describe('ClearTracksChip', () => {
  it('counts the tracks and clears them', () => {
    const d = vi.fn()
    render(<ClearTracksChip count={3} icao="AB12CD" dispatch={d} />)
    const chip = screen.getByRole('button', { name: 'Clear tracks (3)' })
    expect(chip.title).toContain('AB12CD')
    fireEvent.click(chip)
    expect(d).toHaveBeenCalledWith({ type: 'clearTracks' })
  })

  it('shows only while tracks are on the map without their panel', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => undefined)))
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        disconnect() {}
      },
    )
    render(<App />)
    act$({ type: 'stats', stats: { messagesPerSecond: 0, version: 'test', historyFlightsOpen: 0 } as unknown as Stats })
    act$({ type: 'receiver', receiver: { lat: 25.07, lon: 22.87, maxRangeNm: 300 } })
    act$({
      type: 'map',
      map: { tileUrl: 'http://x/{z}/{x}/{y}', attribution: '', maxZoom: 18, darkFilter: false, waypointLabelMinZoom: 9, waypoints: [] },
    })
    act$({ type: 'showHistory', icao: 'AB12CD' })
    act$({ type: 'historyTracks', icao: 'AB12CD', ids: [1, 2] })
    const chip = () => screen.queryByRole('button', { name: /Clear tracks \(\d+\)/ })
    expect(chip()).toBeNull() // the panel is open
    fireEvent.click(screen.getByLabelText('Close details'))
    expect(chip()?.textContent).toBe('Clear tracks (2)')
    expect(window.location.search).toContain('history=AB12CD')
    expect(window.location.search).toContain('flights=1%2C2')
    fireEvent.click(chip() as HTMLElement)
    expect(chip()).toBeNull()
    expect(window.location.search).not.toContain('flights')
  })
})
