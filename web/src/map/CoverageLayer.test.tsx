import { cleanup, render } from '@testing-library/react'
import L from 'leaflet'
import { MapContainer, useMap } from 'react-leaflet'
import { afterEach, describe, expect, it } from 'vitest'
import type { Coverage } from '../api/types'
import { CoverageLayer } from './CoverageLayer'

afterEach(cleanup)

const point = (bearing: number, lat: number, lon: number) => ({ bearing, distanceNm: 50, lat, lon, time: '2026-11-19T10:00:00Z' })
const cov: Coverage = {
  receiver: { lat: 25.07, lon: 22.87 },
  bucketDeg: 2,
  points: [point(0, 25.9, 22.87), point(120, 24.7, 23.6), point(240, 24.7, 22.1)],
}

// jsdom has no SVG renderer, so inspect the map's layers instead of the DOM.
function outlines(c: Coverage | null): L.Polygon[] {
  let map: L.Map | undefined
  function Grab() {
    map = useMap()
    return null
  }
  render(
    <MapContainer center={[25, 23]} zoom={7} style={{ width: 400, height: 300 }}>
      <CoverageLayer coverage={c} />
      <Grab />
    </MapContainer>,
  )
  const found: L.Polygon[] = []
  map?.eachLayer((l) => {
    if (l instanceof L.Polygon && l.options.className === 'coverage') {
      found.push(l)
    }
  })
  return found
}

describe('CoverageLayer', () => {
  it('draws the outline as one polygon through the points', () => {
    const [poly, ...rest] = outlines(cov)
    expect(rest).toHaveLength(0)
    const ring = (poly?.getLatLngs()[0] ?? []) as L.LatLng[]
    expect(ring.map((ll) => [ll.lat, ll.lng])).toEqual(cov.points.map((p) => [p.lat, p.lon]))
  })

  it('draws nothing without enough points', () => {
    expect(outlines(null)).toHaveLength(0)
    cleanup()
    expect(outlines({ ...cov, points: cov.points.slice(0, 2) })).toHaveLength(0)
  })
})
