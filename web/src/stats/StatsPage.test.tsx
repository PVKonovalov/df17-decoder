import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import statsFixture from '../api/stats.fixture.json'
import StatsPage from './StatsPage'

const fake = vi.hoisted(() => {
  const handlers: ((p: unknown) => void)[] = []
  return {
    handlers,
    init: vi.fn(() => ({
      on: (_: string, h: (p: unknown) => void) => handlers.push(h),
      setOption: vi.fn(),
      resize: vi.fn(),
      dispose: vi.fn(),
    })),
  }
})
vi.mock('./echarts', () => ({ initChart: fake.init }))

const hours = [
  { hour: '2026-11-20T10:00:00Z', aircraftAdsb: 5, aircraftModes: 1, flightsStarted: 4, positions: 100 },
  { hour: '2026-11-20T11:00:00Z', aircraftAdsb: 3, aircraftModes: 0, flightsStarted: 1, positions: 20, partial: true },
]

function serve(routes: Record<string, unknown>) {
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string) => {
      const path = url.split('?')[0] ?? ''
      const body = routes[path]
      return Promise.resolve(body === 503 ? new Response('{"error":"off"}', { status: 503 }) : new Response(JSON.stringify(body ?? [])))
    }),
  )
}

beforeEach(() => {
  fake.handlers.length = 0
  window.history.replaceState(null, '', '/stats?range=7d')
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('StatsPage', () => {
  it('shows storage and charts, queries the URL range, and opens a clicked hour in playback', async () => {
    serve({
      '/api/storage': statsFixture.storage,
      '/api/stats/hourly': hours,
      '/api/stats/countries': [{ country: 'Netherlands', aircraftHours: 3 }],
      '/api/stats/range-daily': [statsFixture.rangeDay],
      '/api/stats/notable': statsFixture.notable,
    })
    render(<StatsPage />)
    expect(await screen.findByText(/tape grows about/)).toBeTruthy()
    expect(await screen.findByText('KLM1023')).toBeTruthy() // the notable list
    await waitFor(() => {
      expect(fake.init).toHaveBeenCalled()
    })
    const urls = vi.mocked(fetch).mock.calls.map((c) => c[0] as string)
    const hourly = new URL(urls.find((u) => u.startsWith('/api/stats/hourly')) ?? '', 'http://x')
    const span = Date.parse(hourly.searchParams.get('to') ?? '') - Date.parse(hourly.searchParams.get('from') ?? '')
    expect(span).toBe(7 * 24 * 3600_000)
    expect(urls.some((u) => u.startsWith('/api/stats/range-daily') && u.includes('tz='))).toBe(true)
    expect(screen.getByRole('button', { name: '7 days', pressed: true })).toBeTruthy()

    act(() => {
      fake.handlers[0]?.({ dataIndex: 0 }) // the first chart is aircraft per hour
    })
    expect(window.location.pathname + window.location.search).toBe('/?t=2026-11-20T10%3A00%3A00Z')
  })

  it('explains a disabled history and missing data instead of empty frames', async () => {
    serve({
      '/api/storage': statsFixture.storage,
      ...Object.fromEntries(['hourly', 'countries', 'operators', 'range-daily', 'altitude', 'notable'].map((p) => [`/api/stats/${p}`, 503])),
    })
    render(<StatsPage />)
    expect((await screen.findAllByText(/set history.enabled: true/)).length).toBeGreaterThan(1)

    cleanup()
    serve({ '/api/storage': statsFixture.storage, '/api/stats/notable': { emergencies: 0, firstSeen: 0, events: [] } })
    render(<StatsPage />)
    expect(await screen.findByText(/No data yet: rollups start after the first full hour/)).toBeTruthy()
    expect(screen.getByText('No aircraft in this range.')).toBeTruthy()
    expect(screen.getAllByText('No positions in this range.').length).toBe(2) // range and altitude
    expect(screen.getByText('No airline callsigns in this range.')).toBeTruthy()
    expect(screen.getByText(/No receiver counters yet/)).toBeTruthy()
    expect(screen.getByText('No emergencies or new aircraft in this range.')).toBeTruthy()
  })

  it('keeps the picked range in the URL', async () => {
    serve({ '/api/storage': statsFixture.storage, '/api/stats/notable': statsFixture.notable })
    render(<StatsPage />)
    act(() => {
      screen.getByRole('button', { name: '30 days' }).click()
    })
    expect(window.location.search).toBe('?range=30d')
    await screen.findByText(/tape grows/)
  })
})
