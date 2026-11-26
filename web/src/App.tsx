import { useEffect, useMemo, useReducer, useRef, type CSSProperties } from 'react'
import { useLiveData } from './api/live'
import { PLAYBACK_SPEEDS } from './api/types'
import { DetailPanel } from './components/DetailPanel'
import { Header } from './components/Header'
import { HistoryOnlyPanel } from './components/HistoryPanel'
import { Splitter, useSplit } from './components/Splitter'
import type { HistoryFlight } from './api/types'
import { Deck } from './deck/Deck'
import { flightSeek } from './deck/history'
import { HistorySection, type PlayControl } from './history/HistorySection'
import { LiveMap } from './map/LiveMap'
import { useColorMode, usePanel } from './map/prefs'
import { initialState, reducer, type State } from './state/reducer'
import { parseUrl, replaceUrl } from './state/url'
import { AircraftTable } from './table/AircraftTable'

/**
 * initFromUrl starts with the aircraft of a shared link (?icao=…) pending selection and the history-only aircraft
 * (?history=…), which becomes the selection if it is on the map.
 */
function initFromUrl(s: State): State {
  const u = parseUrl(window.location.search)
  const owner = u.history ?? u.icao
  return {
    ...s,
    pendingSelect: u.icao,
    historyIcao: u.history,
    ...(owner !== null && (u.flights.length > 0 || u.profile !== null) && { historyFor: owner, historyProfile: u.profile }),
    ...(owner !== null && u.flights.length > 0 && { historyTracks: u.flights, historyFitted: owner, fitSeq: 1 }),
  }
}

export function App() {
  const [state, dispatch] = useReducer(reducer, initialState, initFromUrl)
  const send = useLiveData(dispatch)
  const linkedIcao = state.selected ?? state.pendingSelect
  useEffect(() => {
    replaceUrl({
      icao: linkedIcao,
      // with the panel closed, the shown tracks keep their aircraft in the link (which opens its panel)
      history: state.historyIcao ?? (state.selected === null && state.historyTracks.length > 0 ? state.historyFor : null),
      flights: [...state.historyTracks],
      profile: state.historyProfile,
    })
  }, [linkedIcao, state.selected, state.historyIcao, state.historyFor, state.historyTracks, state.historyProfile])
  const historyEnabled = state.stats?.historyFlightsOpen !== undefined

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
  const playback: PlayControl = {
    available: pb !== null,
    play: (f: HistoryFlight) => {
      const t = flightSeek(f.firstSeen, f.lastSeen, pb?.tapeStart)
      if (t === null || !send({ action: 'seek', t })) {
        return false
      }
      dispatch({ type: 'selectAfterSeek', icao: f.icao })
      return true
    },
  }
  const historyOf = (icao: string, showLatest = false) =>
    historyEnabled && (
      <HistorySection
        key={icao}
        icao={icao}
        shown={state.historyFor === icao ? state.historyTracks : []}
        profile={state.historyFor === icao ? state.historyProfile : null}
        dispatch={dispatch}
        playback={playback}
        showLatest={showLatest}
      />
    )

  return (
    <div className={moment ? 'app playback' : 'app'}>
      <Header
        state={state}
        dispatch={dispatch}
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
              reference={historyEnabled}
              editing={state.editing === selected.icao}
              gone={live === undefined}
            >
              {historyOf(selected.icao)}
            </DetailPanel>
          )}
          {!selected && historyEnabled && state.historyIcao !== null && (
            <HistoryOnlyPanel key={state.historyIcao} icao={state.historyIcao} dispatch={dispatch} moment={moment}>
              {historyOf(state.historyIcao, true)}
            </HistoryOnlyPanel>
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
