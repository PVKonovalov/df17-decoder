import type { ConnState, State } from '../state/reducer'

const connLabel: Record<ConnState, string> = {
  connecting: 'connecting',
  connected: 'live',
  reconnecting: 'reconnecting',
  down: 'down',
}

/** PanelToggles are the on/off switches of the optional panels; each is shown when its setter is given. */
export interface PanelToggles {
  table: boolean
  onTable?: (on: boolean) => void
  legend: boolean
  onLegend?: (on: boolean) => void
}

/**
 * Header shows the title with the server version, aircraft counts, message rate, receiver source status and the /api/ws connection state, and the
 * switches that show or hide the aircraft table and the map legend.
 */
export function Header({ state, panels }: { state: State; panels?: PanelToggles }) {
  const { aircraft, stats, conn } = state
  let withPos = 0
  for (const a of aircraft.values()) {
    if (a.hasPosition) {
      withPos++
    }
  }
  const source = stats?.source

  return (
    <header className="header">
      <div className="header-brand">
        <h1 className="header-title">df17-decoder</h1>
        {stats && (
          <span className="header-version" title="Server version">
            {stats.version}
          </span>
        )}
      </div>
      <dl className="header-stats">
        <div>
          <dt>Aircraft</dt>
          <dd>
            {aircraft.size} <span className="muted">/ {withPos} pos</span>
          </dd>
        </div>
        <div>
          <dt>Msg/s</dt>
          <dd>{stats ? stats.messagesPerSecond.toFixed(1) : '—'}</dd>
        </div>
        <div>
          <dt>Source</dt>
          <dd title={source?.url}>
            {source ? (
              <span className={source.connected ? 'ok' : 'bad'}>{source.connected ? 'connected' : 'disconnected'}</span>
            ) : (
              '—'
            )}
          </dd>
        </div>
        <div>
          <dt>Stream</dt>
          <dd>
            <span className={`conn conn-${conn}`} role="status">
              {connLabel[conn]}
            </span>
          </dd>
        </div>
      </dl>
      {panels && (
        <div className="header-panels" role="group" aria-label="Show panels">
          {panels.onTable && <PanelButton label="Table" on={panels.table} onChange={panels.onTable} />}
          {panels.onLegend && <PanelButton label="Legend" on={panels.legend} onChange={panels.onLegend} />}
        </div>
      )}
    </header>
  )
}

function PanelButton({ label, on, onChange }: { label: string; on: boolean; onChange: (on: boolean) => void }) {
  return (
    <button
      type="button"
      aria-pressed={on}
      title={`${on ? 'Hide' : 'Show'} the ${label.toLowerCase()}`}
      onClick={() => {
        onChange(!on)
      }}
    >
      {label}
    </button>
  )
}
