import { cleanup, render } from '@testing-library/react'
import L from 'leaflet'
import { MapContainer, useMap } from 'react-leaflet'
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import type { CoverageGrid } from '../api/types'
import type { HeatBand, HeatMetric } from './heatmap'
import { HeatmapLayer } from './HeatmapLayer'
import { signalColor } from './style'

afterEach(cleanup)

// jsdom has no canvas: give Leaflet's canvas renderer a context whose methods do nothing.
beforeAll(() => {
  const ctx = new Proxy({}, { get: () => () => undefined })
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(ctx as CanvasRenderingContext2D)
})
afterAll(() => {
  vi.restoreAllMocks()
})

const grid: CoverageGrid = {
  receiver: { lat: 25, lon: 23 },
  bucketDeg: 2,
  ringNm: 5,
  bands: [
    { name: 'low', cells: [[0, 1, 2, 2, -30, -31, -29], [10, 2, 1, 0, 0, 0, 0]] },
    { name: 'mid', cells: [] },
    { name: 'high', cells: [[90, 30, 5, 5, -12, -14, -10]] },
    { name: 'unknown', cells: [] },
  ],
}

// Inspect the map's layers instead of the drawing.
function cells(g: CoverageGrid | null, metric: HeatMetric, band: HeatBand): L.Polygon[] {
  let map: L.Map | undefined
  function Grab() {
    map = useMap()
    return null
  }
  render(
    <MapContainer center={[25, 23]} zoom={7} style={{ width: 400, height: 300 }}>
      <HeatmapLayer grid={g} metric={metric} band={band} />
      <Grab />
    </MapContainer>,
  )
  const found: L.Polygon[] = []
  map?.eachLayer((l) => {
    if (l instanceof L.Polygon && l.options.className === 'heat-cell') {
      found.push(l)
    }
  })
  return found
}

describe('HeatmapLayer', () => {
  it('draws one wedge per cell of the band, colored by signal', () => {
    const all = cells(grid, 'signal', 'all')
    expect(all).toHaveLength(3)
    expect(all.map((p) => p.options.fillColor).sort()).toEqual(
      [signalColor(-30), signalColor(undefined), signalColor(-12)].sort(),
    )
    cleanup()
    expect(cells(grid, 'count', 'high')).toHaveLength(1)
  })

  it('draws nothing without a grid', () => {
    expect(cells(null, 'signal', 'all')).toHaveLength(0)
  })
})
