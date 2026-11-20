import L from 'leaflet'
import 'leaflet/dist/leaflet.css'
import { useEffect, useMemo, type Dispatch } from 'react'
import { Circle, CircleMarker, MapContainer, Marker, TileLayer, useMap, useMapEvents } from 'react-leaflet'
import type { Action, State } from '../state/reducer'
import { AircraftLayer } from './AircraftLayer'
import { ICON_ORDER, ICONS, iconFor, iconSvg, type IconKind } from './icons'
import { type CoverageView, useColorMode, useCoverageView, useHeatBand } from './prefs'
import { CoverageLayer } from './CoverageLayer'
import { HEAT_BANDS, type HeatBand, hasSignal as gridHasSignal } from './heatmap'
import { HeatmapLayer } from './HeatmapLayer'
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
  showLegend?: boolean
  colorMode?: ColorMode // chosen marker colors, when the caller shares them (with the table); else remembered here
  onColorMode?: (m: ColorMode) => void
}

/**
 * LiveMap shows the receiver with range rings and all aircraft with a position, once the receiver and the
 * tile layer (the map section of the service config) are known.
 */
export function LiveMap(props: Props) {
  const { state, dispatch, showLegend = true } = props
  const { receiver, map: tiles } = state
  const [storedMode, setStoredMode] = useColorMode()
  const chosenMode = props.colorMode ?? storedMode
  const setColorMode = props.onColorMode ?? setStoredMode
  const [chosenView, setCoverageView] = useCoverageView()
  const [band, setBand] = useHeatBand()
  const heatSignal = useMemo(() => gridHasSignal(state.coverageGrid), [state.coverageGrid])
  const hasSignal = useMemo(() => [...state.aircraft.values()].some((a) => a.rssi !== undefined), [state.aircraft])
  const colorMode = hasSignal ? chosenMode : 'altitude'
  const kinds = useMemo(() => {
    const present = new Set<IconKind>()
    for (const a of state.aircraft.values()) {
      if (a.hasPosition) {
        present.add(iconFor(a.category))
      }
    }
    return ICON_ORDER.filter((k) => present.has(k))
  }, [state.aircraft])
  const coverageView = chosenView === 'signal' && !heatSignal ? 'count' : chosenView
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
        {(coverageView === 'signal' || coverageView === 'count') && (
          <HeatmapLayer grid={state.coverageGrid} metric={coverageView} band={band} />
        )}
        {coverageView === 'outline' && <CoverageLayer coverage={state.coverage} />}
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
      {showLegend && (
        <MapLegend
          mode={colorMode}
          onMode={hasSignal ? setColorMode : undefined}
          kinds={kinds}
          coverage={coverageView}
          onCoverage={setCoverageView}
          heatSignal={heatSignal}
          band={band}
          onBand={setBand}
        />
      )}
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

const COVERAGE_VIEWS: readonly { view: CoverageView; label: string }[] = [
  { view: 'off', label: 'Off' },
  { view: 'outline', label: 'Outline' },
  { view: 'signal', label: 'Signal' },
  { view: 'count', label: 'Count' },
]

/**
 * MapLegend shows the color scale of the markers: altitude (also used by trails) or signal level. onMode,
 * given only when some aircraft has a signal level (Beast input), adds the switch between the two. onCoverage
 * adds the coverage view switch (the Signal heatmap only when heatSignal), and onBand the heatmap's altitude band.
 */
export function MapLegend({
  mode,
  onMode,
  coverage = 'off',
  onCoverage,
  heatSignal = false,
  band = 'all',
  onBand,
  kinds = [],
}: {
  mode: ColorMode
  onMode?: ((m: ColorMode) => void) | undefined
  coverage?: CoverageView
  onCoverage?: (v: CoverageView) => void
  heatSignal?: boolean
  band?: HeatBand
  onBand?: (b: HeatBand) => void
  kinds?: readonly IconKind[]
}) {
  const heat = coverage === 'signal' || coverage === 'count'
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
      {kinds.length > 0 && (
        <ul className="legend-icons" aria-label="Aircraft icons">
          {kinds.map((k) => (
            <li key={k}>
              <span className="ac-icon" dangerouslySetInnerHTML={{ __html: iconSvg(k, 0.7) }} />
              {ICONS[k].label}
            </li>
          ))}
        </ul>
      )}
      {onCoverage !== undefined && (
        <div className="legend-coverage">
          <div className="legend-title">Coverage</div>
          <div className="legend-modes" role="group" aria-label="Coverage view">
            {COVERAGE_VIEWS.filter((v) => v.view !== 'signal' || heatSignal).map((v) => (
              <button
                key={v.view}
                type="button"
                aria-pressed={coverage === v.view}
                onClick={() => {
                  onCoverage(v.view)
                }}
              >
                {v.label}
              </button>
            ))}
          </div>
          {heat && (
            <>
              {onBand !== undefined && (
                <select
                  className="legend-band"
                  aria-label="Heatmap altitude band"
                  value={band}
                  onChange={(e) => {
                    onBand(e.target.value as HeatBand)
                  }}
                >
                  {HEAT_BANDS.map((b) => (
                    <option key={b.band} value={b.band}>
                      {b.label}
                    </option>
                  ))}
                </select>
              )}
              <div className="legend-bar" style={{ background: SIGNAL_GRADIENT }} />
              <div className="legend-ticks">
                {coverage === 'signal' ? (
                  SIGNAL_TICKS.map((db) => <span key={db}>{db}</span>)
                ) : (
                  <>
                    <span>rare</span>
                    <span>often</span>
                  </>
                )}
              </div>
              <div className="legend-note">
                {coverage === 'signal'
                  ? 'Average dBFS of decoded frames. Weak frames are lost near the edge, so edge cells look stronger.'
                  : 'Positions per 2° × 5 nm cell, log scale.'}
              </div>
            </>
          )}
        </div>
      )}
    </div>
  )
}
