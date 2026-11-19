import L from 'leaflet'
import 'leaflet/dist/leaflet.css'
import { useEffect, useMemo, type Dispatch } from 'react'
import { Circle, CircleMarker, MapContainer, Marker, TileLayer, useMap, useMapEvents } from 'react-leaflet'
import type { Action, State } from '../state/reducer'
import { AircraftLayer } from './AircraftLayer'
import { useColorMode } from './colorMode'
import {
  ALT_MAX_FT,
  altitudeColor,
  type ColorMode,
  destinationNorth,
  GROUND_COLOR,
  NO_ALT_COLOR,
  SIGNAL_MAX_DB,
  SIGNAL_MIN_DB,
  signalColor,
} from './style'

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
  const [chosenMode, setColorMode] = useColorMode()
  const hasSignal = useMemo(() => [...state.aircraft.values()].some((a) => a.rssi !== undefined), [state.aircraft])
  const colorMode = hasSignal ? chosenMode : 'altitude'
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
          colorMode={colorMode}
        />
        <ClearSelectionOnMapClick dispatch={dispatch} />
        <PanToSelection state={state} />
        <InvalidateOnResize />
      </MapContainer>
      <MapLegend mode={colorMode} onMode={hasSignal ? setColorMode : undefined} />
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

const ALT_TICKS = [0, 10_000, 20_000, 30_000, 40_000] as const
const SIGNAL_TICKS = [-36, -25, -14, -3] as const

const gradient = (stops: string[]) => `linear-gradient(to right, ${stops.join(', ')})`
const ALT_GRADIENT = gradient(
  Array.from({ length: ALT_MAX_FT / 2000 + 1 }, (_, i) => `${altitudeColor(i * 2000)} ${(i * 2000 * 100) / ALT_MAX_FT}%`),
)
const SIGNAL_GRADIENT = gradient(
  Array.from({ length: 12 }, (_, i) => {
    const db = SIGNAL_MIN_DB + (i * (SIGNAL_MAX_DB - SIGNAL_MIN_DB)) / 11
    return `${signalColor(db)} ${(i * 100) / 11}%`
  }),
)

/**
 * MapLegend shows the color scale of the markers: altitude (also used by trails) or signal level. onMode,
 * given only when some aircraft has a signal level (Beast input), adds the switch between the two.
 */
export function MapLegend({ mode, onMode }: { mode: ColorMode; onMode?: ((m: ColorMode) => void) | undefined }) {
  const signal = mode === 'signal'
  return (
    <div className="legend" aria-label={signal ? 'Signal color scale' : 'Altitude color scale'}>
      {onMode !== undefined && (
        <div className="legend-modes" role="group" aria-label="Color markers by">
          {(['altitude', 'signal'] as const).map((m) => (
            <button
              key={m}
              type="button"
              aria-pressed={mode === m}
              onClick={() => {
                onMode(m)
              }}
            >
              {m === 'altitude' ? 'Altitude' : 'Signal'}
            </button>
          ))}
        </div>
      )}
      <div className="legend-title">{signal ? 'Signal, dBFS (trails: altitude)' : 'Altitude, ft'}</div>
      <div className="legend-bar" style={{ background: signal ? SIGNAL_GRADIENT : ALT_GRADIENT }} />
      <div className="legend-ticks">
        {signal
          ? SIGNAL_TICKS.map((db) => <span key={db}>{db}</span>)
          : ALT_TICKS.map((ft) => <span key={ft}>{ft === 0 ? '0' : `${ft / 1000}k`}</span>)}
      </div>
      <div className="legend-extra">
        {!signal && (
          <>
            <span className="legend-swatch" style={{ background: GROUND_COLOR }} /> on ground
          </>
        )}
        <span className="legend-swatch" style={{ background: NO_ALT_COLOR }} /> {signal ? 'no signal level' : 'unknown'}
      </div>
    </div>
  )
}
