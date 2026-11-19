import type { ConnState, State } from '../state/reducer'

const connLabel: Record<ConnState, string> = {
  connecting: 'connecting',
  connected: 'live',
  reconnecting: 'reconnecting',
  down: 'down',
}

/** Header shows aircraft counts, message rate, receiver source status and the /api/ws connection state. */
export function Header({ state }: { state: State }) {
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
      <h1 className="header-title" title={stats ? `version ${stats.version}` : undefined}>
        df17-decoder
      </h1>
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
    </header>
  )
}
