import L from 'leaflet'
import { useEffect, useRef } from 'react'
import { useMap } from 'react-leaflet'
import type { HistoryTrack } from '../api/history'
import { clearHover, getHover, nearestIndex, setHover, subscribeHover } from '../history/hover'
import { useTracks } from '../history/trackCache'
import { nearestPoint, pointTooltip, trackBounds, trackSegments } from './historyTracks'
import { altitudeColor } from './style'

/** PANE holds the stored tracks above waypoints (350) and coverage (overlay pane, 400), under the live trails (420). */
export const PANE = 'historyTracks'

/**
 * HistoryTrackLayer draws the stored tracks of the flights ids as thin dashed altitude-colored lines with a start
 * and an end dot, in a non-interactive pane; hovering near a point shows its time, altitude, speed and track. The
 * map fits the tracks each time fitSeq changes, once they are loaded.
 */
export function HistoryTrackLayer({ ids, fitSeq }: { ids: readonly number[]; fitSeq: number }) {
  const map = useMap()
  const { tracks, complete } = useTracks(ids)
  const fitted = useRef(0)

  useEffect(() => {
    if (fitSeq === fitted.current || !complete) {
      return
    }
    fitted.current = fitSeq
    const b = trackBounds(tracks)
    if (b !== null) {
      // keep the tracks clear of the detail panel, which covers the top right of the map
      const panel = map.getContainer().parentElement?.querySelector('aside.detail')
      const right = panel instanceof HTMLElement ? Math.min(panel.offsetWidth, map.getSize().x / 2) : 0
      map.fitBounds(b, { paddingTopLeft: [24, 24], paddingBottomRight: [right + 24, 24], maxZoom: 12 })
    }
  }, [map, fitSeq, tracks, complete])

  useEffect(() => {
    if (tracks.length === 0) {
      return
    }
    const pane = map.getPane(PANE) ?? map.createPane(PANE)
    pane.style.zIndex = '410'
    pane.style.pointerEvents = 'none'
    const renderer = L.canvas({ padding: 0.5, pane: PANE })
    const group = L.layerGroup().addTo(map)
    for (const tr of tracks) {
      draw(group, tr, renderer)
    }

    let projected: { flight: number; xs: Float64Array; ys: Float64Array }[] = []
    const project = () => {
      projected = tracks.map((tr) => {
        const xs = new Float64Array(tr.t.length)
        const ys = new Float64Array(tr.t.length)
        for (let i = 0; i < tr.t.length; i++) {
          const p = map.latLngToLayerPoint([tr.lat[i] ?? 0, tr.lon[i] ?? 0])
          xs[i] = p.x
          ys[i] = p.y
        }
        return { flight: tr.id, xs, ys }
      })
    }
    project()

    const tooltip = L.tooltip({ direction: 'top', offset: [0, -6], className: 'track-tooltip' })
    const marker = L.circleMarker([0, 0], {
      radius: 5,
      color: cssVar('--sel', '#2a7de1'),
      weight: 2,
      fill: false,
      interactive: false,
      renderer,
    })
    let shown = ''
    const close = () => {
      shown = ''
      map.closeTooltip(tooltip)
      marker.remove()
      clearHover('map')
    }
    const showAt = (tr: HistoryTrack, i: number) => {
      const key = `${String(tr.id)}/${String(i)}`
      if (key === shown) {
        return
      }
      shown = key
      const ll: [number, number] = [tr.lat[i] ?? 0, tr.lon[i] ?? 0]
      tooltip.setLatLng(ll).setContent(pointTooltip(tr, i))
      map.openTooltip(tooltip)
      marker.setLatLng(ll).addTo(group)
    }
    // the profile chart's hover marks the point of its flight closest in time
    const onShared = () => {
      const h = getHover()
      if (h?.source === 'map') {
        return
      }
      const tr = h === null ? undefined : tracks.find((t) => t.id === h.flight)
      const i = tr === undefined || h === null ? -1 : nearestIndex(tr.t, h.t)
      if (tr === undefined || i < 0) {
        if (shown !== '') {
          shown = ''
          map.closeTooltip(tooltip)
          marker.remove()
        }
        return
      }
      showAt(tr, i)
    }
    const unsubscribe = subscribeHover(onShared)
    const onMove = (e: L.LeafletMouseEvent) => {
      const hit = nearestPoint(projected, e.layerPoint.x, e.layerPoint.y)
      const tr = hit === null ? undefined : tracks.find((t) => t.id === hit.flight)
      if (hit === null || tr === undefined) {
        close()
        return
      }
      showAt(tr, hit.index)
      setHover({ flight: tr.id, t: tr.t[hit.index] ?? 0, source: 'map' })
    }
    map.on('zoomend viewreset', project)
    map.on('mousemove', onMove)
    map.on('mouseout', close)
    return () => {
      unsubscribe()
      map.off('zoomend viewreset', project)
      map.off('mousemove', onMove)
      map.off('mouseout', close)
      close()
      group.remove()
      renderer.remove()
    }
  }, [map, tracks])
  return null
}

function draw(group: L.LayerGroup, tr: HistoryTrack, renderer: L.Canvas) {
  for (const seg of trackSegments(tr)) {
    L.polyline(seg.points, {
      color: seg.color,
      weight: 1.5,
      opacity: 0.9,
      dashArray: '6 4',
      interactive: false,
      renderer,
    }).addTo(group)
  }
  const n = tr.t.length
  const halo = cssVar('--surface', '#ffffff')
  for (const i of n > 1 ? [0, n - 1] : [0]) {
    if (n === 0) {
      break
    }
    const color = altitudeColor(tr.alt[i] ?? undefined)
    // start: a hollow dot, end: a filled one
    L.circleMarker([tr.lat[i] ?? 0, tr.lon[i] ?? 0], {
      radius: 3.5,
      color,
      fillColor: i === 0 ? halo : color,
      weight: 1.5,
      fillOpacity: 1,
      interactive: false,
      renderer,
    }).addTo(group)
  }
}

/** cssVar reads a theme token for canvas drawing, which can't use var(). */
function cssVar(name: string, fallback: string): string {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return v === '' ? fallback : v
}
