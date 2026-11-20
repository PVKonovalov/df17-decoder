import type { StorageReport } from '../api/stats'

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB'] as const

/** formatBytes formats n bytes in binary units: one decimal below 10, whole numbers above. */
export function formatBytes(n: number): string {
  let v = n
  let u = 0
  while (Math.abs(v) >= 1024 && u < UNITS.length - 1) {
    v /= 1024
    u++
  }
  return `${u === 0 || Math.abs(v) >= 10 ? Math.round(v) : v.toFixed(1)} ${UNITS[u] ?? 'B'}`
}

/** formatDays formats a duration in days in the largest sensible unit. */
export function formatDays(d: number): string {
  if (d < 1) {
    return 'less than a day'
  }
  if (d < 90) {
    const n = Math.round(d)
    return `${n} day${n === 1 ? '' : 's'}`
  }
  if (d < 730) {
    return `${Math.round(d / 30.44)} months`
  }
  return `${Math.round(d / 365.25)} years`
}

/** within phrases a time ahead: "within a day" or "in about 14 days". */
function within(d: number): string {
  return d < 1 ? 'within a day' : `in about ${formatDays(d)}`
}

export type Severity = 'red' | 'amber'

/** storageAlert returns the panel's warning, red when recording is affected, amber when space runs low; or null. */
export function storageAlert(r: StorageReport): { severity: Severity; text: string } | null {
  const free = r.volume.freeBytes
  if (r.flags.guardActive) {
    return { severity: 'red', text: 'Tape recording is paused: free space is below the disk guard minimum.' }
  }
  if (r.projection.daysUntilGuard === 0) {
    return { severity: 'red', text: 'Free space is below the disk guard minimum: the oldest recordings are being deleted.' }
  }
  if (r.flags.warning && free !== undefined) {
    return {
      severity: 'amber',
      text: `Only ${formatBytes(free)} free, less than twice the disk guard minimum of ${formatBytes(r.flags.minFreeBytes)}.`,
    }
  }
  if (r.flags.guardDeletedSegments > 0) {
    const n = r.flags.guardDeletedSegments
    return { severity: 'amber', text: `The disk guard deleted ${n} tape segment${n === 1 ? '' : 's'} since the service started.` }
  }
  return null
}

/** storageLines describes growth and projections in plain words. */
export function storageLines(r: StorageReport): string[] {
  const lines: string[] = []
  const part = (name: string, bytes: number | undefined, perDay: number | undefined, days: number | undefined, steady: number | undefined) => {
    if (bytes === undefined) {
      return
    }
    if (perDay === undefined) {
      lines.push(`${name} growth is not known yet: it needs an hour of data.`)
      return
    }
    const grows = `${name} grows about ${formatBytes(perDay)} per day`
    if (days === undefined) {
      lines.push(`${grows} and is kept forever.`)
    } else if (steady !== undefined) {
      lines.push(`${grows}; it levels off at about ${formatBytes(steady)} after ${formatDays(days)}.`)
    }
  }
  const p = r.projection
  part('The tape', r.usage.tapeBytes, r.growth.tapeBytesPerDay, p.tapeRetentionDays, p.tapeSteadyStateBytes)
  part('The flight history', r.usage.dbBytes, r.growth.dbBytesPerDay, p.dbRetentionDays, p.dbSteadyStateBytes)
  if (p.daysUntilFull !== undefined) {
    lines.push(`At this rate the disk is full ${within(p.daysUntilFull)}.`)
  }
  if (p.daysUntilGuard !== undefined && p.daysUntilGuard > 0) {
    lines.push(`The disk guard starts deleting the oldest recordings ${within(p.daysUntilGuard)}.`)
  }
  return lines
}
