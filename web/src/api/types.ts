// Hand-written mirrors of the Go JSON (internal/tracker/aircraft.go, internal/api). Optional fields are the
// ones Go omits when unknown: never assume they are present, and compare numbers with `!== undefined`.

/** tracker.Aircraft */
export interface Aircraft {
  icao: string
  callsign?: string
  squawk?: string
  category?: string
  adsbVersion?: number
  country?: string
  emergency?: string

  altBaro?: number // ft
  altGnss?: number // ft
  groundSpeed?: number // kt
  track?: number // degrees true
  ias?: number // kt
  tas?: number // kt
  heading?: number // degrees
  verticalRate?: number // ft/min
  roll?: number // degrees, positive = right wing down (Comm-B 5,0)
  mach?: number // Comm-B 6,0
  onGround?: boolean

  selectedAlt?: number // ft, autopilot/FMS target (ADS-B TC 29)
  selectedAltSource?: string // 'mcp' or 'fms'
  selectedHeading?: number // degrees
  baroSetting?: number // hPa, altimeter setting
  autopilotModes?: string[] // engaged: autopilot, vnav, althold, approach, lnav, tcas

  lat?: number
  lon?: number
  posTime?: string // RFC 3339
  distanceNm?: number
  bearing?: number

  firstSeen: string // RFC 3339
  lastSeen: string // RFC 3339
  messages: number
  messagesByDf: Record<string, number>
  rssi?: number // dBFS, Beast input only

  hasPosition: boolean
  adsb: boolean
}

/** Every Aircraft key; the compiler rejects missing or extra keys, the contract test compares it with the fixture. */
export const aircraftKeys = {
  icao: true,
  callsign: true,
  squawk: true,
  category: true,
  adsbVersion: true,
  country: true,
  emergency: true,
  altBaro: true,
  altGnss: true,
  groundSpeed: true,
  track: true,
  ias: true,
  tas: true,
  heading: true,
  verticalRate: true,
  roll: true,
  mach: true,
  onGround: true,
  selectedAlt: true,
  selectedAltSource: true,
  selectedHeading: true,
  baroSetting: true,
  autopilotModes: true,
  lat: true,
  lon: true,
  posTime: true,
  distanceNm: true,
  bearing: true,
  firstSeen: true,
  lastSeen: true,
  messages: true,
  messagesByDf: true,
  rssi: true,
  hasPosition: true,
  adsb: true,
} as const satisfies Record<keyof Aircraft, true>

/** api.Receiver */
export interface Receiver {
  lat: number
  lon: number
  maxRangeNm: number
}

/** api.MapConfig: the tile layer, from the map section of the service config */
export interface MapConfig {
  tileUrl: string
  attribution: string
  maxZoom: number
  darkFilter: boolean
}

/** api.statsResponse */
export interface Stats {
  version: string
  uptimeSeconds: number
  source: {
    url: string
    connected: boolean
    reconnects: number
    malformedLines: number
    lastFrame?: string
  }
  frames: number
  framesPerDf: Record<string, number>
  messages: number
  messagesPerSecond: number
  crcFailures: number
  fixedBitErrors: number
  unsupported: number
  invalid: number
  addressParityDrops: number
  positions: number
  cprRejected: number
  commBAmbiguous: number // DF20/21 replies that fit more than one Comm-B register
  commBRejected: number // inferred Comm-B reports inconsistent with the aircraft's recent state
  aircraft: number
}

/** coverage.Point: the furthest position seen in one bearing bucket */
export interface CoveragePoint {
  bearing: number // degrees true from the receiver
  distanceNm: number
  lat: number
  lon: number
  alt?: number // ft
  time: string // RFC 3339, data time
}

/** coverage.Snapshot: the range outline, non-empty buckets in bearing order */
export interface Coverage {
  receiver: { lat: number; lon: number }
  bucketDeg: number
  points: CoveragePoint[]
}

/**
 * coverage.Cell: [bucket, ring, positions, positions with a signal level, average dBFS (mean of linear power),
 * min dBFS, max dBFS]; the signal values are 0 when no position had a signal level.
 */
export type CoverageCell = [number, number, number, number, number, number, number]

/** coverage.Grid: the polar coverage grid, one cell list per altitude band (low, mid, high, unknown) */
export interface CoverageGrid {
  receiver: { lat: number; lon: number }
  bucketDeg: number
  ringNm: number
  bands: { name: string; cells: CoverageCell[] }[]
}

/** api.wsMessage as received on /api/ws */
export type WsMessage =
  | { type: 'receiver'; data: Receiver }
  | { type: 'map'; data: MapConfig }
  | { type: 'coverage'; data: Coverage }
  | { type: 'coverageGrid'; data: CoverageGrid }
  | { type: 'aircraft'; data: Aircraft[] }
  | { type: 'stats'; data: Stats }
