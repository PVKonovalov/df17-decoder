import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { navigate } from './route'
import { Root } from './Root'

vi.mock('./App', () => ({ App: () => <p>map page</p> }))
vi.mock('./stats/StatsPage', () => ({ default: () => <p>stats page</p> }))

afterEach(() => {
  window.history.replaceState(null, '', '/')
})

describe('routing', () => {
  it('switches between the map and /stats, keeping the query, and follows back', async () => {
    window.history.replaceState(null, '', '/?icao=738ABC')
    render(<Root />)
    expect(screen.getByText('map page')).toBeTruthy()

    act(() => {
      navigate('/stats')
    })
    expect(await screen.findByText('stats page')).toBeTruthy()
    expect(window.location.pathname + window.location.search).toBe('/stats?icao=738ABC')

    act(() => {
      window.history.replaceState(null, '', '/?icao=738ABC')
      window.dispatchEvent(new PopStateEvent('popstate'))
    })
    expect(await screen.findByText('map page')).toBeTruthy()
  })

  it('opens /stats directly', async () => {
    window.history.replaceState(null, '', '/stats/')
    render(<Root />)
    expect(await screen.findByText('stats page')).toBeTruthy()
  })
})
