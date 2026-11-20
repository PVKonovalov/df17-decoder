import { describe, expect, it } from 'vitest'
import type { StorageReport } from '../api/stats'
import { formatBytes, formatDays, storageAlert, storageLines } from './format'

const GB = 1024 ** 3
const MB = 1024 ** 2

function report(patch: Partial<StorageReport> = {}): StorageReport {
  return {
    volume: { totalBytes: 64 * GB, freeBytes: 40 * GB },
    usage: { tapeBytes: 300 * MB, dbBytes: 90 * MB, otherBytes: 1000 },
    growth: { tapeBytesPerDay: 36 * MB, dbBytesPerDay: 9 * MB },
    projection: { tapeRetentionDays: 14, dbRetentionDays: 30, tapeSteadyStateBytes: 504 * MB, dbSteadyStateBytes: 270 * MB, daysUntilGuard: 0.5 },
    horizon: {},
    flags: { guardActive: false, guardDeletedSegments: 0, warning: false, minFreeBytes: GB },
    ...patch,
  }
}

describe('storage text', () => {
  it('formats sizes and durations', () => {
    expect([formatBytes(0), formatBytes(1536), formatBytes(504 * MB), formatBytes(2.5 * GB)]).toEqual(['0 B', '1.5 KB', '504 MB', '2.5 GB'])
    expect([formatDays(0.3), formatDays(1), formatDays(14), formatDays(200), formatDays(4300)]).toEqual([
      'less than a day', '1 day', '14 days', '7 months', '12 years',
    ])
  })

  it('describes growth and projections in plain words', () => {
    expect(storageLines(report())).toEqual([
      'The tape grows about 36 MB per day; it levels off at about 504 MB after 14 days.',
      'The flight history grows about 9.0 MB per day; it levels off at about 270 MB after 30 days.',
      'The disk guard starts deleting the oldest recordings within a day.',
    ])
    const forever = report({
      growth: { tapeBytesPerDay: 36 * MB },
      projection: { tapeRetentionDays: 14, tapeSteadyStateBytes: 504 * MB, daysUntilFull: 4300 },
    })
    expect(storageLines(forever)).toEqual([
      'The tape grows about 36 MB per day; it levels off at about 504 MB after 14 days.',
      'The flight history growth is not known yet: it needs an hour of data.',
      'At this rate the disk is full in about 12 years.',
    ])
    expect(storageLines(report({ usage: { otherBytes: 0 }, projection: {} }))).toEqual([])
  })

  it('warns amber when space runs low and red when recording suffers', () => {
    expect(storageAlert(report())).toBeNull()
    const flags = report().flags
    expect(storageAlert(report({ flags: { ...flags, warning: true } }))?.severity).toBe('amber')
    expect(storageAlert(report({ flags: { ...flags, guardDeletedSegments: 3 } }))?.text).toContain('3 tape segments')
    expect(storageAlert(report({ projection: { daysUntilGuard: 0 } }))?.severity).toBe('red')
    expect(storageAlert(report({ flags: { ...flags, guardActive: true, warning: true } }))?.text).toContain('paused')
  })
})
