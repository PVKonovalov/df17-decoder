import L from 'leaflet'
import { useEffect, useRef, type Dispatch } from 'react'
import { useMap } from 'react-leaflet'
import type { Aircraft } from '../api/types'
import type { Action, TrailPoint } from '../state/reducer'
import { newAgeMs } from '../table/rows'
import { aircraftAltitude, altitudeColor, dataNow, isStale, markerLabel, rotation, trailSegments } from './style'

const PLANE_SVG =
  '<svg viewBox="0 0 24 24" width="24" height="24" aria-hidden="true"><path d="M12 1.5c.8 0 1.3.7 1.3 1.6V9l8 4.8v2.1l-8-2.4v5l2.3 1.7v1.8L12 21.1 8.4 22v-1.8l2.3-1.7v-5l-8 2.4v-2.1l8-4.8V3.1c0-.9.5-1.6 1.3-1.6z"/></svg>'

interface Entry {
  marker: L.Marker
  root: HTMLElement
  plane: HTMLElement
  label: HTMLElement
  trail: L.LayerGroup
  trailData: readonly TrailPoint[] | undefined
  trailStyle: string // stale/selected flags the trail was drawn with
  color: string
  rotation: number
}

interface Props {
  aircraft: ReadonlyMap<string, Aircraft>
  trails: ReadonlyMap<string, readonly TrailPoint[]>
  selected: string | null
  dispatch: Dispatch<Action>
}

/**
 * AircraftLayer draws one rotated, altitude-colored marker with a label and a trail per aircraft with a
 * position. Markers are created once and updated in place on every snapshot; trails are redrawn only when
 * the aircraft's trail or its stale/selected state changes.
 */
export function AircraftLayer({ aircraft, trails, selected, dispatch }: Props) {
  const map = useMap()
  const entries = useRef(new Map<string, Entry>())
  const renderer = useRef<L.Canvas | null>(null)

  useEffect(() => {
    const all = entries.current
    return () => {
      for (const e of all.values()) {
        e.marker.remove()
        e.trail.remove()
      }
      all.clear()
    }
  }, [map])

  useEffect(() => {
    renderer.current ??= L.canvas({ padding: 0.5 })
    const now = dataNow(aircraft.values())
    const all = entries.current

    for (const [icao, e] of all) {
      const a = aircraft.get(icao)
      if (a?.lat === undefined || a.lon === undefined) {
        e.marker.remove()
        e.trail.remove()
        all.delete(icao)
      }
    }

    for (const a of aircraft.values()) {
      if (a.lat === undefined || a.lon === undefined) {
        continue
      }
      let e = all.get(a.icao)
      if (e === undefined) {
        e = createEntry(map, a, (icao) => {
          dispatch({ type: 'select', icao })
        })
        all.set(a.icao, e)
        if (newAgeMs(a, now) !== undefined) {
          e.root.classList.add('pulse') // same "new" rule as the table row highlight
        }
      }

      const stale = isStale(a, now)
      const isSelected = a.icao === selected
      const latlng = e.marker.getLatLng()
      if (latlng.lat !== a.lat || latlng.lng !== a.lon) {
        e.marker.setLatLng([a.lat, a.lon])
      }
      const color = altitudeColor(aircraftAltitude(a), a.onGround === true)
      if (e.color !== color) {
        e.plane.style.color = color
        e.color = color
      }
      const deg = rotation(a)
      if (e.rotation !== deg) {
        e.plane.style.transform = `rotate(${deg}deg)`
        e.rotation = deg
      }
      const text = markerLabel(a)
      if (e.label.textContent !== text) {
        e.label.textContent = text
      }
      e.root.classList.toggle('stale', stale)
      if (e.root.classList.contains('selected') !== isSelected) {
        e.root.classList.toggle('selected', isSelected)
        e.marker.setZIndexOffset(isSelected ? 1000 : 0)
      }

      const trail = trails.get(a.icao)
      const trailStyle = `${String(stale)}/${String(isSelected)}`
      if (trail !== e.trailData || trailStyle !== e.trailStyle) {
        drawTrail(e.trail, trail ?? [], stale, isSelected, renderer.current)
        e.trailData = trail
        e.trailStyle = trailStyle
      }
    }
  }, [map, aircraft, trails, selected, dispatch])

  return null
}

function createEntry(map: L.Map, a: Aircraft, onSelect: (icao: string) => void): Entry {
  const icon = L.divIcon({
    className: 'ac',
    html: `<div class="ac-plane">${PLANE_SVG}</div><div class="ac-label"></div>`,
    iconSize: [24, 24],
    iconAnchor: [12, 12],
  })
  const marker = L.marker([a.lat ?? 0, a.lon ?? 0], { icon, keyboard: false, title: a.icao })
  marker.on('click', (ev) => {
    L.DomEvent.stopPropagation(ev)
    onSelect(a.icao)
  })
  marker.addTo(map)
  const root = marker.getElement()
  const plane = root?.querySelector<HTMLElement>('.ac-plane')
  const label = root?.querySelector<HTMLElement>('.ac-label')
  if (!root || !plane || !label) {
    throw new Error('aircraft marker element missing')
  }
  const trail = L.layerGroup().addTo(map)
  return { marker, root, plane, label, trail, trailData: undefined, trailStyle: '', color: '', rotation: NaN }
}

function drawTrail(
  group: L.LayerGroup,
  trail: readonly TrailPoint[],
  stale: boolean,
  selected: boolean,
  renderer: L.Canvas,
) {
  group.clearLayers()
  for (const seg of trailSegments(trail)) {
    L.polyline(seg.points, {
      color: seg.color,
      weight: selected ? 4 : 2,
      opacity: stale ? 0.35 : selected ? 1 : 0.75,
      interactive: false,
      renderer,
    }).addTo(group)
  }
}
