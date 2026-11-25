import L from 'leaflet'
import { useEffect, useRef } from 'react'
import { useMap } from 'react-leaflet'
import type { Receiver, Waypoint } from '../api/types'
import { labelsVisible, nearest, waypointHtml, waypointTooltip } from './waypoints'

/** PANE holds the waypoints between the tiles (200) and the overlays (400: coverage, heatmap, trails). */
export const PANE = 'waypoints'

/**
 * WaypointLayer draws the configured waypoints once (the list is static) in their own non-interactive pane under
 * all traffic. Names show from labelMinZoom up; hovering near a symbol shows its tooltip.
 */
export function WaypointLayer({
  waypoints,
  labelMinZoom,
  receiver,
}: {
  waypoints: readonly Waypoint[]
  labelMinZoom: number
  receiver: Receiver | null
}) {
  const map = useMap()
  const receiverRef = useRef(receiver)
  receiverRef.current = receiver

  useEffect(() => {
    if (waypoints.length === 0) {
      return
    }
    const pane = map.getPane(PANE) ?? map.createPane(PANE)
    pane.style.zIndex = '350'
    pane.style.pointerEvents = 'none'
    const group = L.layerGroup(
      waypoints.map((w) =>
        L.marker([w.lat, w.lon], {
          pane: PANE,
          interactive: false,
          keyboard: false,
          icon: L.divIcon({ className: 'wp-marker', html: waypointHtml(w), iconSize: [12, 12], iconAnchor: [6, 6] }),
        }),
      ),
    ).addTo(map)

    let points: L.Point[] = []
    const project = () => {
      points = waypoints.map((w) => map.latLngToLayerPoint([w.lat, w.lon]))
      pane.classList.toggle('wp-labels-off', !labelsVisible(map.getZoom(), labelMinZoom))
    }
    project()

    const tooltip = L.tooltip({ direction: 'right', offset: [8, 0], className: 'wp-tooltip' })
    let shown = -1
    const close = () => {
      shown = -1
      map.closeTooltip(tooltip)
    }
    const onMove = (e: L.LeafletMouseEvent) => {
      const i = nearest(points, e.layerPoint.x, e.layerPoint.y)
      const w = waypoints[i]
      if (w === undefined) {
        close()
        return
      }
      if (i !== shown) {
        shown = i
        tooltip.setLatLng([w.lat, w.lon]).setContent(waypointTooltip(w, receiverRef.current))
        map.openTooltip(tooltip)
      }
    }
    map.on('zoomend viewreset moveend', project)
    map.on('mousemove', onMove)
    map.on('mouseout', close)
    return () => {
      map.off('zoomend viewreset moveend', project)
      map.off('mousemove', onMove)
      map.off('mouseout', close)
      close()
      group.remove()
    }
  }, [map, waypoints, labelMinZoom])

  return null
}
