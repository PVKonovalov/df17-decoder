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

  nic?: number // navigation integrity category of the last position message
  nacp?: number // navigation accuracy category for position (TC 31)
  positionQuality?: string // tracker.Quality, ADS-B only once known: 'ok', 'degraded' (NIC < 7 or NACp < 8), 'lost'

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
  nic: true,
  nacp: true,
  positionQuality: true,
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

/** tracker.TrackPoint: one thinned trail point; unknown values and false flags are omitted */
export interface TrackPoint {
  t: number // ms since epoch, data time
  lat: number
  lon: number
  alt?: number // ft, barometric unless altGnss
  altGnss?: boolean
  gs?: number // kt
  track?: number // degrees true
  vr?: number // ft/min
  onGround?: boolean
}

/** Every TrackPoint key, compared with trackpoint.fixture.json by the contract test. */
export const trackPointKeys = {
  t: true,
  lat: true,
  lon: true,
  alt: true,
  altGnss: true,
  gs: true,
  track: true,
  vr: true,
  onGround: true,
} as const satisfies Record<keyof TrackPoint, true>

/** api.wsTrails: every current trail and the window (Go duration, e.g. "10m0s") to trim them to */
export interface Trails {
  window: string
  trails: Record<string, TrackPoint[]>
}

/** api.Receiver */
export interface Receiver {
  lat: number
  lon: number
  maxRangeNm: number
}

/** api.MapConfig: the tile layer and waypoints, from the map section of the service config */
export interface MapConfig {
  tileUrl: string
  attribution: string
  maxZoom: number
  darkFilter: boolean
  /** waypoint names are shown from this zoom up */
  waypointLabelMinZoom: number
  waypoints: Waypoint[]
}

/** api.Waypoint: a configured fix in decimal degrees, with the AIP DMS text it was written in */
export interface Waypoint {
  name: string
  lat: number
  lon: number
  latDms: string
  lonDms: string
  /** one of config.WaypointIcons, e.g. "triangleOpen" */
  icon: string
  /** #rgb or #rrggbb; absent: the theme default */
  color?: string
  note?: string
}

/** Every MapConfig key, compared with map.fixture.json by the contract test. */
export const mapConfigKeys = {
  tileUrl: true,
  attribution: true,
  maxZoom: true,
  darkFilter: true,
  waypointLabelMinZoom: true,
  waypoints: true,
} as const satisfies Record<keyof MapConfig, true>

/** Every Waypoint key, compared with map.fixture.json by the contract test. */
export const waypointKeys = {
  name: true,
  lat: true,
  lon: true,
  latDms: true,
  lonDms: true,
  icon: true,
  color: true,
  note: true,
} as const satisfies Record<keyof Waypoint, true>

/** api.statsResponse */
export interface Stats {
  version: string
  serverTime: string // RFC 3339: the clock the other times in stats compare with
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
  noPosition: number // ADS-B TC 0 messages: the aircraft reports no valid position
  commBAmbiguous: number // DF20/21 replies that fit more than one Comm-B register
  commBRejected: number // inferred Comm-B reports inconsistent with the aircraft's recent state
  aircraft: number
  // flight history, present only when history.enabled
  historyFlightsOpen?: number
  historyPointsWritten?: number
  historyDropped?: number // events dropped because the write queue was full
  historyWriteErrors?: number
  historyLastFlush?: string // RFC 3339
  // raw frame tape, present only when tape.enabled
  tapeRecording?: boolean
  tapePaused?: boolean // free disk space below tape.minFreeMB
  tapeOldest?: string // RFC 3339
  tapeNewest?: string // RFC 3339
  tapeBytes?: number
  tapeFramesWritten?: number
  tapeDropped?: number
  tapeWriteErrors?: number
  tapeLastFlush?: string // RFC 3339
  playbackSessions?: number // active tape playback sessions, when playback is enabled
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

/** api.wsPlayback: the "playback" message, the tape counter's state (sent only when playback is enabled) */
export interface Playback {
  mode: 'live' | 'playback'
  t: string // RFC 3339 UTC: the playback position, or now when live
  speed: number
  playing: boolean
  gapSkipped?: { from: string; to: string } // once, in the update after a skipped silence
  tapeStart?: string // RFC 3339
  tapeEnd?: string // RFC 3339
}

/** history.Flight as served by /api/history/flights; checked by contract.test.ts (history.fixture.json) */
export interface HistoryFlight {
  id: number
  icao: string
  callsign?: string
  squawk?: string
  category?: string
  country?: string
  adsb: boolean
  firstSeen: string // RFC 3339, data time
  lastSeen: string
  closed: boolean
  points: number
  maxAlt?: number
  minDistanceNm?: number
  maxDistanceNm?: number
  durationS: number
  onTape: boolean // the tape covers some of the flight
}

/** history.Event: an emergency squawk on the timeline */
export interface TimelineEvent {
  t: string // RFC 3339
  flightId: number
  icao: string
  callsign?: string
  squawk: string
}

/** history.Timeline: GET /api/history/timeline, flights active per bucket as [ADS-B, Mode-S only] */
export interface Timeline {
  from: string // RFC 3339, the first bucket's start
  bucket: string // Go duration
  buckets: [number, number][]
  events: TimelineEvent[]
}

/** Allowed playback speeds (playback.Speeds) */
export const PLAYBACK_SPEEDS = [1, 2, 4, 8, 16, 60] as const

/** api.wsControl: the data of a client {type: 'playback'} message */
export type PlaybackControl =
  | { action: 'play' | 'pause' | 'live' }
  | { action: 'seek'; t: string }
  | { action: 'speed'; speed: (typeof PLAYBACK_SPEEDS)[number] }
  | { action: 'step'; seconds: number }

/** api.wsMessage as received on /api/ws */
export type WsMessage =
  | { type: 'receiver'; data: Receiver }
  | { type: 'map'; data: MapConfig }
  | { type: 'coverage'; data: Coverage }
  | { type: 'coverageGrid'; data: CoverageGrid }
  | { type: 'trails'; data: Trails }
  | { type: 'trailPoints'; data: Record<string, TrackPoint[]> }
  | { type: 'aircraft'; data: Aircraft[] }
  | { type: 'stats'; data: Stats }
  | { type: 'playback'; data: Playback }
  | { type: 'error'; data: { message: string } }
