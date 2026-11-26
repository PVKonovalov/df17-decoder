import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import historyFixture from '../api/history.fixture.json'
import FlightProfile, { flightUrl } from './FlightProfile'
import { getHover, setHover } from './hover'

const fake = vi.hoisted(() => {
  const handlers = new Map<string, (e: unknown) => void>()
  const chart = {
    on: vi.fn((name: string, h: (e: unknown) => void) => {
      handlers.set(name, h)
    }),
    off: vi.fn(),
    setOption: vi.fn(),
    dispatchAction: vi.fn(),
    resize: vi.fn(),
    dispose: vi.fn(),
  }
  return { chart, handlers, init: vi.fn(() => chart) }
})
vi.mock('../stats/echarts', () => ({ initChart: fake.init }))

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setHover(null)
})

const detail = {
  ...historyFixture.flight,
  positions: [
    { t: 1000, lat: 25.1, lon: 22.9, alt: 10000, gs: 400 },
    { t: 2000, lat: 25.2, lon: 23.0, alt: 11000, gs: 410 },
    { t: 3000, lat: 25.3, lon: 23.1, alt: 12000, gs: 420 },
  ],
  samples: [],
}

describe('FlightProfile', () => {
  it('shares its hover with the map track both ways', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify(detail))))
    vi.stubGlobal('fetch', fetchMock)
    render(<FlightProfile id={3} />)
    await screen.findByLabelText('Flight profile')
    await act(async () => {
      await Promise.resolve()
    })
    expect(fetchMock).toHaveBeenCalledWith(flightUrl(3), expect.anything())
    expect(fake.chart.setOption).toHaveBeenCalled()

    act(() => {
      fake.handlers.get('updateAxisPointer')?.({ axesInfo: [{ axisDim: 'x', value: 2100 }] })
    })
    expect(getHover()).toEqual({ flight: 3, t: 2000, source: 'chart' })
    act(() => {
      fake.handlers.get('globalout')?.({})
    })
    expect(getHover()).toBeNull()

    act(() => {
      setHover({ flight: 3, t: 2900, source: 'map' })
    })
    expect(fake.chart.dispatchAction).toHaveBeenLastCalledWith({ type: 'showTip', seriesIndex: 0, dataIndex: 2 })
    act(() => {
      fake.handlers.get('updateAxisPointer')?.({ axesInfo: [{ axisDim: 'x', value: 3000 }] })
    })
    expect(getHover()?.source).toBe('map') // the tip it was told to show doesn't take the hover back
    act(() => {
      setHover({ flight: 9, t: 2900, source: 'map' })
    })
    expect(fake.chart.dispatchAction).toHaveBeenLastCalledWith({ type: 'hideTip' })
  })

  it('says when the flight is gone', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('{}', { status: 404 }))))
    render(<FlightProfile id={3} />)
    expect(await screen.findByText('This flight is no longer stored.')).toBeDefined()
  })
})
