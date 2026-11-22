/** AircraftInfo is the user's entry for an airframe (GET/PUT /api/reference/aircraft/{icao}). */
export interface AircraftInfo {
  icao: string
  registration?: string
  typeCode?: string
  typeName?: string
  operator?: string
  updated: string
}

/** RouteInfo is the user's entry for a flight number (GET/PUT /api/reference/routes/{callsign}). */
export interface RouteInfo {
  callsign: string
  origin?: string
  destination?: string
  updated: string
}

export const aircraftInfoUrl = (icao: string) => `/api/reference/aircraft/${encodeURIComponent(icao)}`
export const routeUrl = (callsign: string) => `/api/reference/routes/${encodeURIComponent(callsign)}`

/** ReferenceDisabled is thrown when the server has no history store (503). */
export class ReferenceDisabled extends Error {}

async function result<T>(r: Response): Promise<T | null> {
  if (r.status === 404 || r.status === 204) {
    return null
  }
  if (r.status === 503) {
    throw new ReferenceDisabled('reference data needs history.enabled')
  }
  if (!r.ok) {
    const body = (await r.json().catch(() => null)) as { error?: string } | null
    throw new Error(body?.error ?? `HTTP ${r.status}`)
  }
  return (await r.json()) as T
}

/** getReference returns the entry at url, or null when there is none. */
export async function getReference<T>(url: string, signal?: AbortSignal): Promise<T | null> {
  return result<T>(await fetch(url, { signal: signal ?? null }))
}

/** putReference stores fields at url and returns the entry, or null when all fields were empty (deleted). */
export async function putReference<T>(url: string, fields: Record<string, string>): Promise<T | null> {
  return result<T>(
    await fetch(url, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(fields) }),
  )
}
