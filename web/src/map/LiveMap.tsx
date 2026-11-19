import L from 'leaflet'
import 'leaflet/dist/leaflet.css'
import { useEffect, useMemo, type Dispatch } from 'react'
import { Circle, CircleMarker, MapContainer, Marker, TileLayer, useMap, useMapEvents } from 'react-leaflet'
import type { Action, State } from '../state/reducer'
import { AircraftLayer } from './AircraftLayer'
import { ALT_MAX_FT, altitudeColor, destinationNorth, GROUND_COLOR, NO_ALT_COLOR } from './style'

const NM_TO_M = 1852
const RING_NM = [25, 50, 100] as const

interface Props {
  state: State
  dispatch: Dispatch<Action>
}

/**
 * LiveMap shows the receiver with range rings and all aircraft with a position, once the receiver and the
 * tile layer (the map section of the service config) are known.
 */
export function LiveMap({ state, dispatch }: Props) {
  const { receiver, map: tiles } = state
  if (receiver === null || tiles === null) {
    return <div className="map-wait">Waiting for receiver position…</div>
  }
  const center: [number, number] = [receiver.lat, receiver.lon]

  return (
    <div className="map-wrap">
      <MapContainer center={center} zoom={9} maxZoom={tiles.maxZoom} className="map" worldCopyJump>
        <TileLayer
          key={tiles.tileUrl}
          url={tiles.tileUrl}
          attribution={tiles.attribution}
          maxZoom={tiles.maxZoom}
          className={tiles.darkFilter ? 'tiles tiles-dark-filter' : 'tiles'}
        />
        <RangeRings lat={receiver.lat} lon={receiver.lon} />
        <CircleMarker
          center={center}
          radius={5}
          pathOptions={{ className: 'receiver', fillOpacity: 1, weight: 2 }}
          interactive={false}
        />
        <AircraftLayer
          aircraft={state.aircraft}
          trails={state.trails}
          selected={state.selected}
          dispatch={dispatch}
        />
        <ClearSelectionOnMapClick dispatch={dispatch} />
        <PanToSelection state={state} />
        <InvalidateOnResize />
      </MapContainer>
      <AltitudeLegend />
    </div>
  )
}

function RangeRings({ lat, lon }: { lat: number; lon: number }) {
  const labels = useMemo(
    () =>
      RING_NM.map((nm) =>
        L.divIcon({ className: 'ring-label', html: `${nm} nm`, iconSize: [48, 16], iconAnchor: [24, 8] }),
      ),
    [],
  )
  return (
    <>
      {RING_NM.map((nm) => (
        <Circle
          key={nm}
          center={[lat, lon]}
          radius={nm * NM_TO_M}
          pathOptions={{ className: 'ring', weight: 1, dashArray: '6 6', fill: false }}
          interactive={false}
        />
      ))}
      {RING_NM.map((nm, i) => (
        <Marker
          key={nm}
          position={destinationNorth(lat, lon, nm)}
          icon={labels[i] as L.DivIcon}
          interactive={false}
          keyboard={false}
        />
      ))}
    </>
  )
}

function ClearSelectionOnMapClick({ dispatch }: { dispatch: Dispatch<Action> }) {
  useMapEvents({
    click: () => {
      dispatch({ type: 'clearSelection' })
    },
  })
  return null
}

/** PanToSelection pans to the selected aircraft each time a selection asks for it (panSeq changes). */
function PanToSelection({ state }: { state: State }) {
  const map = useMap()
  const { panSeq } = state
  const a = state.selected === null ? undefined : state.aircraft.get(state.selected)
  const lat = a?.lat
  const lon = a?.lon
  useEffect(() => {
    if (panSeq > 0 && lat !== undefined && lon !== undefined) {
      map.panTo([lat, lon])
    }
    // deps omit lat/lon on purpose: pan once per request, don't follow the aircraft
  }, [map, panSeq])
  return null
}

/** InvalidateOnResize keeps Leaflet's size in sync when the layout (splitter, window) changes. */
function InvalidateOnResize() {
  const map = useMap()
  useEffect(() => {
    const ro = new ResizeObserver(() => {
      map.invalidateSize({ pan: false })
    })
    ro.observe(map.getContainer())
    return () => {
      ro.disconnect()
    }
  }, [map])
  return null
}

const LEGEND_TICKS = [0, 10_000, 20_000, 30_000, 40_000] as const

/** AltitudeLegend shows the continuous altitude color scale used by markers and trails. */
export function AltitudeLegend() {
  const gradient = useMemo(() => {
    const stops: string[] = []
    for (let ft = 0; ft <= ALT_MAX_FT; ft += 2000) {
      stops.push(`${altitudeColor(ft)} ${(ft / ALT_MAX_FT) * 100}%`)
    }
    return `linear-gradient(to right, ${stops.join(', ')})`
  }, [])
  return (
    <div className="legend" aria-label="Altitude color scale">
      <div className="legend-title">Altitude, ft</div>
      <div className="legend-bar" style={{ background: gradient }} />
      <div className="legend-ticks">
        {LEGEND_TICKS.map((ft) => (
          <span key={ft}>{ft === 0 ? '0' : `${ft / 1000}k`}</span>
        ))}
      </div>
      <div className="legend-extra">
        <span className="legend-swatch" style={{ background: GROUND_COLOR }} /> on ground
        <span className="legend-swatch" style={{ background: NO_ALT_COLOR }} /> unknown
      </div>
    </div>
  )
}
