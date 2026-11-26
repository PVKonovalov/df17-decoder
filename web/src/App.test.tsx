import { act, cleanup, render, screen } from '@testing-library/react'
import type { Dispatch } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Stats } from './api/types'
import { App } from './App'
import type { Action } from './state/reducer'

let dispatch: Dispatch<Action> | undefined
vi.mock('./api/live', () => ({
  useLiveData: (d: Dispatch<Action>) => {
    dispatch = d
    return () => true
  },
}))
vi.mock('./map/LiveMap', () => ({ LiveMap: () => null }))
vi.mock('./deck/Deck', () => ({ Deck: () => null }))

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  window.history.replaceState(null, '', '/')
})

const stats = (history: boolean) =>
  ({ messagesPerSecond: 0, version: 'test', ...(history && { historyFlightsOpen: 0 }) }) as unknown as Stats

describe('App history link', () => {
  it('opens ?history= only when the server keeps history, and keeps it in the URL', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => undefined)))
    window.history.replaceState(null, '', '/?history=ab12cd&t=2026-11-20T12:00:00Z')
    render(<App />)
    act(() => {
      dispatch?.({ type: 'stats', stats: stats(false) })
    })
    expect(screen.queryByLabelText('Aircraft history')).toBeNull()
    expect(screen.getByRole('combobox').getAttribute('aria-label')).toContain('on the map')
    act(() => {
      dispatch?.({ type: 'stats', stats: stats(true) })
    })
    expect(screen.getByLabelText('Aircraft history')).toBeDefined()
    expect(window.location.search).toContain('history=AB12CD')
    expect(window.location.search).toContain('t=2026-11-20')
  })
})
