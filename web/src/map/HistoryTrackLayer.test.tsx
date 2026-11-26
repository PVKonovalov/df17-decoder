import { act, cleanup, render } from '@testing-library/react'
import type L from 'leaflet'
import { MapContainer, useMap } from 'react-leaflet'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { HistoryTrack } from '../api/history'
import { clearTrackCache, tracksUrl } from '../history/trackCache'
import { PANE, HistoryTrackLayer } from './HistoryTrackLayer'

const track = (id: number): HistoryTrack => ({
  id,
  icao: 'AB12CD',
  t: [1000, 2000],
  lat: [25.1, 25.2 + id / 100],
  lon: [22.9, 23.0],
  alt: [10000, 12000],
  gs: [400, 410],
  track: [10, 12],
})

let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  clearTrackCache()
  fetchMock = vi.fn((url: string) => {
    const ids = (new URL(url, 'http://x').searchParams.get('ids') ?? '').split(',').map(Number)
    return Promise.resolve(new Response(JSON.stringify({ tracks: ids.filter((id) => id !== 99).map(track) })))
  })
  vi.stubGlobal('fetch', fetchMock)
  // jsdom has no canvas: give Leaflet's canvas renderer a context whose methods do nothing
  const ctx = new Proxy({}, { get: () => () => undefined })
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(ctx as CanvasRenderingContext2D)
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function setup(ids: number[], fitSeq: number) {
  let map: L.Map | undefined
  function Grab() {
    map = useMap()
    return null
  }
  const ui = (i: number[], f: number) => (
    <MapContainer center={[25.07, 22.87]} zoom={9} style={{ width: 400, height: 300 }}>
      <HistoryTrackLayer ids={i} fitSeq={f} />
      <Grab />
    </MapContainer>
  )
  const r = render(ui(ids, fitSeq))
  if (map === undefined) {
    throw new Error('no map')
  }
  const m = map
  const fit = vi.spyOn(m, 'fitBounds')
  return {
    map: m,
    fit,
    rerender: (i: number[], f: number) => {
      r.rerender(ui(i, f))
    },
  }
}

const flush = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 0))
  })

describe('HistoryTrackLayer', () => {
  it('fetches missing tracks in one batch and draws them between coverage and live trails', async () => {
    const { map } = setup([1, 2, 99], 0)
    await flush()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0]?.[0]).toBe(tracksUrl([1, 2, 99]))
    const z = Number(map.getPane(PANE)?.style.zIndex)
    expect(z).toBeGreaterThan(400)
    expect(z).toBeLessThan(420)
    expect(map.getPane(PANE)?.style.pointerEvents).toBe('none')
  })

  it('reuses cached tracks and fits once per request', async () => {
    const { fit, rerender } = setup([1], 1)
    await flush()
    expect(fit).toHaveBeenCalledTimes(1)
    rerender([1, 2], 1)
    await flush()
    rerender([1], 1)
    await flush()
    expect(fit).toHaveBeenCalledTimes(1)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(fetchMock.mock.calls[1]?.[0]).toBe(tracksUrl([2]))
    rerender([1, 2], 2)
    await flush()
    expect(fit).toHaveBeenCalledTimes(2)
  })
})
