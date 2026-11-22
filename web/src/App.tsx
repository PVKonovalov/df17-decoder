import { useEffect, useMemo, useReducer, useRef, type CSSProperties } from 'react'
import { useLiveData } from './api/live'
import { PLAYBACK_SPEEDS } from './api/types'
import { DetailPanel } from './components/DetailPanel'
import { Header } from './components/Header'
import { Splitter, useSplit } from './components/Splitter'
import { Deck } from './deck/Deck'
import { LiveMap } from './map/LiveMap'
import { useColorMode, usePanel } from './map/prefs'
import { initialState, reducer, type State } from './state/reducer'
import { parseUrl, replaceUrl } from './state/url'
import { AircraftTable } from './table/AircraftTable'

/** initFromUrl starts with the aircraft of a shared link (?icao=…) pending selection. */
function initFromUrl(s: State): State {
  return { ...s, pendingSelect: parseUrl(window.location.search).icao }
}

export function App() {
  const [state, dispatch] = useReducer(reducer, initialState, initFromUrl)
  const send = useLiveData(dispatch)
  const linkedIcao = state.selected ?? state.pendingSelect
  useEffect(() => {
    replaceUrl({ icao: linkedIcao })
  }, [linkedIcao])

  // a moment link (?t=…&speed=…) seeks once the server reports playback; back in live, the link loses its moment
  const pendingMoment = useRef(parseUrl(window.location.search))
  const pb = state.playback
  useEffect(() => {
    const m = pendingMoment.current
    if (pb === null || state.conn !== 'connected') {
      return
    }
    if (m.t !== null && pb.mode === 'live') {
      pendingMoment.current = { ...m, t: null }
      send({ action: 'seek', t: m.t })
      const speed = PLAYBACK_SPEEDS.find((s) => s === m.speed)
      if (speed !== undefined) {
        send({ action: 'speed', speed })
      }
    } else if (m.t === null && pb.mode === 'live') {
      replaceUrl({ t: null, speed: null })
    }
  }, [pb, state.conn, send])
  const moment = pb?.mode === 'playback' ? { t: pb.t, speed: pb.speed } : undefined
  const [split, setSplit] = useSplit()
  const [showTable, setShowTable] = usePanel('table')
  const [showLegend, setShowLegend] = usePanel('legend')
  const [chosenMode, setColorMode] = useColorMode()
  const hasSignal = useMemo(() => [...state.aircraft.values()].some((a) => a.rssi !== undefined), [state.aircraft])
  const colorMode = hasSignal ? chosenMode : 'altitude'
  const live = state.selected === null ? undefined : state.aircraft.get(state.selected)
  const selected = live ?? (state.held?.icao === state.selected ? state.held : null) ?? undefined

  return (
    <div className={moment ? 'app playback' : 'app'}>
      <Header
        state={state}
        panels={{ table: showTable, onTable: setShowTable, legend: showLegend, onLegend: setShowLegend }}
      />
      <Deck state={state} send={send} dispatch={dispatch} />
      <main
        className={showTable ? 'main' : 'main map-only'}
        style={showTable ? ({ '--split': `${split}%` } as CSSProperties) : undefined}
      >
        <div className="map-pane">
          <LiveMap state={state} dispatch={dispatch} showLegend={showLegend} colorMode={chosenMode} onColorMode={setColorMode} />
          {selected && (
            <DetailPanel
              aircraft={selected}
              dispatch={dispatch}
              moment={moment}
              reference={state.stats?.historyFlightsOpen !== undefined}
              editing={state.editing === selected.icao}
              gone={live === undefined}
            />
          )}
        </div>
        {showTable && (
          <>
            <Splitter onChange={setSplit} />
            <AircraftTable aircraft={state.aircraft} selected={state.selected} dispatch={dispatch} colorMode={colorMode} />
          </>
        )}
      </main>
    </div>
  )
}
