// Types of /api/storage and /api/stats/*, mirroring storage.Report and the history stats rows by hand.
// contract.test.ts checks them against stats.fixture.json, which internal/api TestStatsContract checks against Go.

/** storage.Report: GET /api/storage */
export interface StorageReport {
  volume: { totalBytes?: number; freeBytes?: number }
  usage: { tapeBytes?: number; dbBytes?: number; otherBytes: number }
  growth: { tapeBytesPerDay?: number; dbBytesPerDay?: number }
  projection: {
    tapeRetentionDays?: number
    dbRetentionDays?: number
    tapeSteadyStateBytes?: number
    dbSteadyStateBytes?: number
    daysUntilFull?: number
    daysUntilGuard?: number
  }
  horizon: { tapeOldest?: string; flightsOldest?: string; rollupsOldest?: string }
  flags: { guardActive: boolean; guardDeletedSegments: number; warning: boolean; minFreeBytes: number }
}

/** history.HourRow: GET /api/stats/hourly; partial marks the current hour */
export interface HourRow {
  hour: string
  aircraftAdsb: number
  aircraftModes: number
  flightsStarted: number
  positions: number
  messages?: number
  maxRangeNm?: number
  maxRangeBearing?: number
  dbBytes?: number
  tapeBytes?: number
  frames?: number
  crcFailures?: number
  fixedBitErrors?: number
  cprRejected?: number
  reconnects?: number
  silentMinutes?: number
  signalDbfs?: number
  partial?: boolean
}

/** history.AltitudeBand: GET /api/stats/altitude, from the ground up, then unknown */
export interface AltitudeBand {
  band: string
  minFt?: number
  positions: number
}

/** history.NotableEvent */
export interface NotableEvent {
  t: string
  kind: 'emergency' | 'firstSeen'
  icao: string
  country?: string
  callsign?: string
  squawk?: string
  flightId: number
}

/** history.Notable: GET /api/stats/notable, emergencies first, then newest first */
export interface Notable {
  emergencies: number
  firstSeen: number
  events: NotableEvent[]
}

/** history.CountryCount: GET /api/stats/countries */
export interface CountryCount {
  country: string
  aircraftHours: number
}

/** history.OperatorCount: GET /api/stats/operators */
export interface OperatorCount {
  prefix: string
  aircraftHours: number
}

/** history.RangeDay: GET /api/stats/range-daily */
export interface RangeDay {
  day: string
  maxRangeNm: number
  bearing?: number
  hour: string
}

type Keys<T> = Record<keyof T, true>

export const storageKeys = {
  volume: { totalBytes: true, freeBytes: true },
  usage: { tapeBytes: true, dbBytes: true, otherBytes: true },
  growth: { tapeBytesPerDay: true, dbBytesPerDay: true },
  projection: {
    tapeRetentionDays: true,
    dbRetentionDays: true,
    tapeSteadyStateBytes: true,
    dbSteadyStateBytes: true,
    daysUntilFull: true,
    daysUntilGuard: true,
  },
  horizon: { tapeOldest: true, flightsOldest: true, rollupsOldest: true },
  flags: { guardActive: true, guardDeletedSegments: true, warning: true, minFreeBytes: true },
} satisfies { [K in keyof StorageReport]: Keys<StorageReport[K]> }

export const hourRowKeys = {
  hour: true,
  aircraftAdsb: true,
  aircraftModes: true,
  flightsStarted: true,
  positions: true,
  messages: true,
  maxRangeNm: true,
  maxRangeBearing: true,
  dbBytes: true,
  tapeBytes: true,
  frames: true,
  crcFailures: true,
  fixedBitErrors: true,
  cprRejected: true,
  reconnects: true,
  silentMinutes: true,
  signalDbfs: true,
  partial: true,
} satisfies Keys<HourRow>

export const countryKeys = { country: true, aircraftHours: true } satisfies Keys<CountryCount>
export const operatorKeys = { prefix: true, aircraftHours: true } satisfies Keys<OperatorCount>
export const rangeDayKeys = { day: true, maxRangeNm: true, bearing: true, hour: true } satisfies Keys<RangeDay>
export const altitudeKeys = { band: true, minFt: true, positions: true } satisfies Keys<AltitudeBand>
export const notableKeys = { emergencies: true, firstSeen: true, events: true } satisfies Keys<Notable>
export const notableEventKeys = {
  t: true,
  kind: true,
  icao: true,
  country: true,
  callsign: true,
  squawk: true,
  flightId: true,
} satisfies Keys<NotableEvent>
