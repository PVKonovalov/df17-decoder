import { useReducer } from 'react'
import { useLiveData } from './api/live'
import { Header } from './components/Header'
import { initialState, reducer } from './state/reducer'

export function App() {
  const [state, dispatch] = useReducer(reducer, initialState)
  useLiveData(dispatch)

  return (
    <div className="app">
      <Header state={state} />
      <main className="main">
        <p className="placeholder">
          {state.aircraft.size} aircraft tracked. Map and table are coming next.
        </p>
      </main>
    </div>
  )
}
