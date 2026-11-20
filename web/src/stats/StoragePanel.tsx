import type { StorageReport } from '../api/stats'
import { formatBytes, storageAlert, storageLines } from './format'

const dateFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })

/** usageParts are the stacked bar's segments as shares of the volume: tape, database, other, free. */
export function usageParts(r: StorageReport) {
  const total = r.volume.totalBytes
  const free = r.volume.freeBytes
  const parts = [
    { key: 'tape', label: 'Tape', bytes: r.usage.tapeBytes },
    { key: 'db', label: 'Flight history', bytes: r.usage.dbBytes },
    { key: 'other', label: 'Other files', bytes: r.usage.otherBytes },
    { key: 'free', label: 'Free', bytes: free },
  ].filter((p): p is { key: string; label: string; bytes: number } => p.bytes !== undefined)
  return parts.map((p) => ({ ...p, share: total === undefined || total === 0 ? null : p.bytes / total }))
}

/** StoragePanel shows where the state directory's disk goes, how fast it grows, and any warning. */
export function StoragePanel({ report }: { report: StorageReport }) {
  const alert = storageAlert(report)
  const parts = usageParts(report)
  const { horizon } = report
  return (
    <section className="stats-card stats-storage" aria-label="Storage">
      <h2>Storage</h2>
      {alert && (
        <p className={`stats-alert ${alert.severity}`} role="alert">
          {alert.text}
        </p>
      )}
      {report.volume.totalBytes !== undefined && (
        <div className="usage-bar" role="img" aria-label="Disk usage">
          {parts.map((p) =>
            p.share === null ? null : (
              <span key={p.key} className={`usage-${p.key}`} style={{ flexGrow: Math.max(p.share, 0.004) }} title={`${p.label}: ${formatBytes(p.bytes)}`} />
            ),
          )}
          <span className="usage-rest" style={{ flexGrow: Math.max(0, 1 - parts.reduce((s, p) => s + (p.share ?? 0), 0)) }} title="Used by other programs" />
        </div>
      )}
      <ul className="usage-legend">
        {parts.map((p) => (
          <li key={p.key}>
            <span className={`swatch usage-${p.key}`} />
            {p.label} <b>{formatBytes(p.bytes)}</b>
          </li>
        ))}
        {report.volume.totalBytes !== undefined && (
          <li className="muted">of {formatBytes(report.volume.totalBytes)}</li>
        )}
      </ul>
      {storageLines(report).map((l) => (
        <p key={l} className="stats-line">
          {l}
        </p>
      ))}
      <dl className="stats-horizon">
        {horizon.tapeOldest !== undefined && (
          <div>
            <dt>Oldest tape</dt>
            <dd>{dateFmt.format(new Date(horizon.tapeOldest))}</dd>
          </div>
        )}
        {horizon.flightsOldest !== undefined && (
          <div>
            <dt>Oldest flight</dt>
            <dd>{dateFmt.format(new Date(horizon.flightsOldest))}</dd>
          </div>
        )}
        {horizon.rollupsOldest !== undefined && (
          <div>
            <dt>Statistics since</dt>
            <dd>{dateFmt.format(new Date(horizon.rollupsOldest))}</dd>
          </div>
        )}
      </dl>
    </section>
  )
}
