import type { Receiver, Waypoint } from '../api/types'
import { initialBearing } from './coverage'
import { distanceNm } from './heatmap'
import { waypointIcon, waypointSvg } from './icons'

/** HOVER_PX is how close (in screen pixels) the pointer must be to a waypoint to show its tooltip. */
export const HOVER_PX = 8

const HEX_COLOR = /^#(?:[0-9a-f]{3}|[0-9a-f]{6})$/i

export function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => `&#${String(c.charCodeAt(0))};`)
}

/** waypointColor is the waypoint's own color when it is a valid #rgb/#rrggbb, else undefined (the theme token). */
export function waypointColor(w: Waypoint): string | undefined {
  return w.color !== undefined && HEX_COLOR.test(w.color) ? w.color : undefined
}

/** waypointHtml is the marker content: the symbol and the name label (hidden below the label zoom by CSS). */
export function waypointHtml(w: Waypoint): string {
  const color = waypointColor(w)
  const style = color === undefined ? '' : ` style="--wp-color:${color}"`
  return `<div class="wp"${style}>${waypointSvg(waypointIcon(w.icon))}<span class="wp-label">${escapeHtml(w.name)}</span></div>`
}

/** labelsVisible reports whether names are shown at zoom (from map.waypointLabelMinZoom up). */
export function labelsVisible(zoom: number, minZoom: number): boolean {
  return zoom >= minZoom
}

/** waypointTooltip is the hover text: name, DMS and decimal degrees, distance and bearing from the receiver, note. */
export function waypointTooltip(w: Waypoint, receiver: Receiver | null): string {
  const lines = [
    `<b>${escapeHtml(w.name)}</b>`,
    `${escapeHtml(w.latDms)} ${escapeHtml(w.lonDms)}`,
    `${w.lat.toFixed(5)}, ${w.lon.toFixed(5)}`,
  ]
  if (receiver !== null) {
    const d = distanceNm(receiver.lat, receiver.lon, w.lat, w.lon)
    const brg = String(Math.round(initialBearing(receiver.lat, receiver.lon, w.lat, w.lon)) % 360).padStart(3, '0')
    lines.push(`${d.toFixed(1)} nm at ${brg}° from the receiver`)
  }
  if (w.note !== undefined && w.note !== '') {
    lines.push(escapeHtml(w.note))
  }
  return lines.join('<br>')
}

/** nearest returns the index of the point closest to (x, y) within radius px, or -1. */
export function nearest(points: readonly { x: number; y: number }[], x: number, y: number, radius = HOVER_PX): number {
  let best = -1
  let bestD = radius * radius
  points.forEach((p, i) => {
    const d = (p.x - x) ** 2 + (p.y - y) ** 2
    if (d <= bestD) {
      best = i
      bestD = d
    }
  })
  return best
}
