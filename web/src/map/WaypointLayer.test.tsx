import { cleanup, render } from '@testing-library/react'
import type L from 'leaflet'
import { MapContainer, useMap } from 'react-leaflet'
import { afterEach, describe, expect, it } from 'vitest'
import type { Receiver, Waypoint } from '../api/types'
import { PANE, WaypointLayer } from './WaypointLayer'

afterEach(cleanup)

const receiver: Receiver = { lat: 25.07, lon: 22.87, maxRangeNm: 300 }
const waypoints: Waypoint[] = [
  { name: 'ALPHA', lat: 25.07, lon: 22.87, latDms: '250412.00N', lonDms: '0225212.00E', icon: 'star' },
  { name: 'BRAVO', lat: 25.2, lon: 23, latDms: '251200.00N', lonDms: '0230000.00E', icon: 'flag', color: '#0a0' },
]

function setup(zoom: number, wps: Waypoint[] = waypoints) {
  let map: L.Map | undefined
  function Grab() {
    map = useMap()
    return null
  }
  const ui = (other: number) => (
    <MapContainer center={[25.07, 22.87]} zoom={zoom} style={{ width: 400, height: 300 }}>
      <WaypointLayer waypoints={wps} labelMinZoom={9} receiver={{ ...receiver, maxRangeNm: other }} />
      <Grab />
    </MapContainer>
  )
  const r = render(ui(300))
  if (map === undefined) {
    throw new Error('no map')
  }
  const rerender = (other: number) => {
    r.rerender(ui(other))
  }
  return { map, rerender }
}

describe('WaypointLayer', () => {
  it('draws one non-interactive marker per waypoint in its pane under the overlays', () => {
    const { map } = setup(10)
    const pane = map.getPane(PANE)
    expect(pane?.style.zIndex).toBe('350')
    expect(pane?.style.pointerEvents).toBe('none')
    const markers = pane?.querySelectorAll('.wp-marker') ?? []
    expect(markers).toHaveLength(2)
    expect(pane?.textContent).toBe('ALPHABRAVO')
    expect(pane?.querySelector('.leaflet-interactive')).toBeNull()
    expect(Number(pane?.style.zIndex)).toBeLessThan(400) // leaflet.css: .leaflet-overlay-pane, under markers (600)
  })

  it('hides the labels below the label zoom', () => {
    expect(setup(8).map.getPane(PANE)?.classList.contains('wp-labels-off')).toBe(true)
    cleanup()
    const { map } = setup(9)
    expect(map.getPane(PANE)?.classList.contains('wp-labels-off')).toBe(false)
    map.setZoom(7, { animate: false })
    expect(map.getPane(PANE)?.classList.contains('wp-labels-off')).toBe(true)
  })

  it('keeps its markers on rerenders', () => {
    const { map, rerender } = setup(10)
    const before = map.getPane(PANE)?.querySelector('.wp-marker')
    rerender(250)
    expect(map.getPane(PANE)?.querySelector('.wp-marker')).toBe(before)
  })

  it('draws nothing without waypoints', () => {
    expect(setup(10, []).map.getPane(PANE)).toBeUndefined()
  })
})
