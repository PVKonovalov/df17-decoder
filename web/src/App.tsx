import { useEffect, useReducer, type CSSProperties } from 'react'
import { useLiveData } from './api/live'
import { DetailPanel } from './components/DetailPanel'
import { Header } from './components/Header'
import { Splitter, useSplit } from './components/Splitter'
import { LiveMap } from './map/LiveMap'
import { usePanel } from './map/prefs'
import { initialState, reducer, type State } from './state/reducer'
import { parseUrl, replaceUrl } from './state/url'
import { AircraftTable } from './table/AircraftTable'

/** initFromUrl starts with the aircraft of a shared link (?icao=…) pending selection. */
function initFromUrl(s: State): State {
  return { ...s, pendingSelect: parseUrl(window.location.search).icao }
}

export function App() {
  const [state, dispatch] = useReducer(reducer, initialState, initFromUrl)
  useLiveData(dispatch)
  const linkedIcao = state.selected ?? state.pendingSelect
  useEffect(() => {
    replaceUrl({ icao: linkedIcao })
  }, [linkedIcao])
  const [split, setSplit] = useSplit()
  const [showTable, setShowTable] = usePanel('table')
  const [showLegend, setShowLegend] = usePanel('legend')
  const selected = state.selected === null ? undefined : state.aircraft.get(state.selected)

  return (
    <div className="app">
      <Header
        state={state}
        panels={{ table: showTable, onTable: setShowTable, legend: showLegend, onLegend: setShowLegend }}
      />
      <main
        className={showTable ? 'main' : 'main map-only'}
        style={showTable ? ({ '--split': `${split}%` } as CSSProperties) : undefined}
      >
        <div className="map-pane">
          <LiveMap state={state} dispatch={dispatch} showLegend={showLegend} />
          {selected && <DetailPanel aircraft={selected} dispatch={dispatch} />}
        </div>
        {showTable && (
          <>
            <Splitter onChange={setSplit} />
            <AircraftTable aircraft={state.aircraft} selected={state.selected} dispatch={dispatch} />
          </>
        )}
      </main>
    </div>
  )
}
