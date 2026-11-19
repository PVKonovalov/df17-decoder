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
  onGround?: boolean

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
  onGround: true,
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
  aircraft: number
}

/** api.wsMessage as received on /api/ws */
export type WsMessage =
  | { type: 'receiver'; data: Receiver }
  | { type: 'map'; data: MapConfig }
  | { type: 'aircraft'; data: Aircraft[] }
  | { type: 'stats'; data: Stats }
