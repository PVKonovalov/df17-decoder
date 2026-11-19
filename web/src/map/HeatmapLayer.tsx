import L from 'leaflet'
import { useEffect, useMemo } from 'react'
import { useMap } from 'react-leaflet'
import type { CoverageGrid } from '../api/types'
import { cellAt, cellPolygon, type HeatBand, heatCells, heatColor, heatLabel, type HeatMetric } from './heatmap'

/**
 * HeatmapLayer draws the coverage grid as wedges on a canvas under the aircraft, colored by average signal or by
 * position count (log scale), for one altitude band or all of them. Hovering shows the cell's numbers.
 */
export function HeatmapLayer({ grid, metric, band }: { grid: CoverageGrid | null; metric: HeatMetric; band: HeatBand }) {
  const map = useMap()
  const cells = useMemo(() => heatCells(grid, band), [grid, band])

  useEffect(() => {
    if (grid === null || cells.length === 0) {
      return
    }
    const renderer = L.canvas({ padding: 0.5 })
    const max = Math.max(...cells.map((c) => c.n))
    const group = L.layerGroup(
      cells.map((c) =>
        L.polygon(cellPolygon(grid, c), {
          renderer,
          stroke: false,
          fillColor: heatColor(c, metric, max),
          fillOpacity: 0.5,
          interactive: false,
          className: 'heat-cell',
        }),
      ),
    ).addTo(map)

    const byKey = new Map(cells.map((c) => [`${c.bucket}/${c.ring}`, c]))
    const tooltip = L.tooltip({ direction: 'top', offset: [0, -8], className: 'heat-tooltip' })
    const onMove = (e: L.LeafletMouseEvent) => {
      const { bucket, ring } = cellAt(grid, e.latlng.lat, e.latlng.lng)
      const c = byKey.get(`${bucket}/${ring}`)
      if (c === undefined) {
        map.closeTooltip(tooltip)
        return
      }
      tooltip.setLatLng(e.latlng).setContent(heatLabel(grid, c))
      if (!map.hasLayer(tooltip)) {
        map.openTooltip(tooltip)
      }
    }
    const onOut = () => {
      map.closeTooltip(tooltip)
    }
    map.on('mousemove', onMove)
    map.on('mouseout', onOut)
    return () => {
      map.off('mousemove', onMove)
      map.off('mouseout', onOut)
      map.closeTooltip(tooltip)
      group.remove()
      renderer.remove()
    }
  }, [map, grid, cells, metric])

  return null
}
