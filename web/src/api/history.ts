// Types of the aircraft history endpoints, mirroring internal/history by hand. contract.test.ts checks them against
// history.fixture.json, which internal/api TestAircraftHistoryContract checks against Go.
import type { HistoryFlight, TrackPoint } from './types'

/** history.AircraftMatch: one row of GET /api/history/search */
export interface AircraftMatch {
  icao: string
  lastCallsign?: string
  callsigns: string[] // up to 5, most recent first
  country?: string
  lastSeen: string // RFC 3339, data time
  flights: number
  adsb: boolean
  live: boolean
}

export interface CallsignCount {
  callsign: string
  flights: number
}

/** history.AircraftSummary: GET /api/history/aircraft/{icao}; flights/firstSeen/adsb over all time, the rest over
 * the stored flights (since retainedFrom) */
export interface AircraftHistory {
  icao: string
  country?: string
  firstSeen: string
  lastSeen: string
  flights: number
  adsb: boolean
  live: boolean
  retainedFlights: number
  callsigns: CallsignCount[]
  maxAlt?: number
  minDistanceNm?: number
  maxDistanceNm?: number
  daysSeen: number
  quality?: { ok: number; degraded: number; lost: number }
  retainedFrom?: string
}

/** history.Track: one flight's positions as parallel arrays (t unix ms; alt ft, gs kt, track ° or null) */
export interface HistoryTrack {
  id: number
  icao: string
  t: number[]
  lat: number[]
  lon: number[]
  alt: (number | null)[]
  gs: (number | null)[]
  track: (number | null)[]
}

/** api.TracksResponse: GET /api/history/tracks?ids= (at most MAX_TRACK_IDS) */
export interface TracksResponse {
  tracks: HistoryTrack[]
}

/** history.SamplePoint: an altitude/squawk sample of a Mode-S-only flight */
export interface SamplePoint {
  t: number // unix ms
  alt?: number
  squawk?: string
}

/** history.FlightDetail: GET /api/history/flights/{id} */
export interface FlightDetail extends HistoryFlight {
  positions: TrackPoint[]
  samples: SamplePoint[]
}

/** history.MaxTrackIDs */
export const MAX_TRACK_IDS = 20

type Keys<T> = Record<keyof T, true>

export const aircraftMatchKeys = {
  icao: true,
  lastCallsign: true,
  callsigns: true,
  country: true,
  lastSeen: true,
  flights: true,
  adsb: true,
  live: true,
} satisfies Keys<AircraftMatch>

export const aircraftHistoryKeys = {
  icao: true,
  country: true,
  firstSeen: true,
  lastSeen: true,
  flights: true,
  adsb: true,
  live: true,
  retainedFlights: true,
  callsigns: true,
  maxAlt: true,
  minDistanceNm: true,
  maxDistanceNm: true,
  daysSeen: true,
  quality: { ok: true, degraded: true, lost: true },
  retainedFrom: true,
} satisfies { [K in keyof AircraftHistory]: true | Keys<NonNullable<AircraftHistory[K]>> }

export const historyFlightKeys = {
  id: true,
  icao: true,
  callsign: true,
  squawk: true,
  category: true,
  country: true,
  adsb: true,
  firstSeen: true,
  lastSeen: true,
  closed: true,
  points: true,
  maxAlt: true,
  minDistanceNm: true,
  maxDistanceNm: true,
  durationS: true,
  onTape: true,
} satisfies Keys<HistoryFlight>

export const historyTrackKeys = {
  id: true,
  icao: true,
  t: true,
  lat: true,
  lon: true,
  alt: true,
  gs: true,
  track: true,
} satisfies Keys<HistoryTrack>

export const samplePointKeys = { t: true, alt: true, squawk: true } satisfies Keys<SamplePoint>
