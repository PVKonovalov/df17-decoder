import { useEffect, useMemo, useSyncExternalStore } from 'react'
import { MAX_TRACK_IDS, type HistoryTrack, type TracksResponse } from '../api/history'

// Stored tracks never change once their flight is closed, so they are kept for the session by flight id.
const cache = new Map<number, HistoryTrack | null>() // null: the server has no such flight
const inflight = new Set<number>()
const listeners = new Set<() => void>()
let version = 0

function changed() {
  version++
  for (const l of listeners) {
    l()
  }
}

/** tracksUrl is the batch request for ids (at most MAX_TRACK_IDS). */
export function tracksUrl(ids: readonly number[]): string {
  return `/api/history/tracks?ids=${ids.join(',')}`
}

/** requestTracks fetches the ids not cached or in flight, in batches of MAX_TRACK_IDS. */
export function requestTracks(ids: readonly number[]): void {
  const missing = ids.filter((id) => !cache.has(id) && !inflight.has(id))
  for (let i = 0; i < missing.length; i += MAX_TRACK_IDS) {
    const batch = missing.slice(i, i + MAX_TRACK_IDS)
    for (const id of batch) {
      inflight.add(id)
    }
    fetch(tracksUrl(batch))
      .then((r) => (r.ok ? (r.json() as Promise<TracksResponse>) : Promise.reject(new Error(String(r.status)))))
      .then((res) => {
        for (const id of batch) {
          cache.set(id, res.tracks.find((t) => t.id === id) ?? null)
        }
      })
      .catch(() => undefined)
      .finally(() => {
        for (const id of batch) {
          inflight.delete(id)
        }
        changed()
      })
  }
}

/** cachedTrack returns a loaded track; undefined while unknown, null when the flight has none. */
export function cachedTrack(id: number): HistoryTrack | null | undefined {
  return cache.get(id)
}

/** clearTrackCache empties the cache (tests). */
export function clearTrackCache(): void {
  cache.clear()
  inflight.clear()
  changed()
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => {
    listeners.delete(l)
  }
}

/**
 * useTracks returns the loaded tracks of ids in their order, requesting the missing ones, and whether all of
 * them have been answered.
 */
export function useTracks(ids: readonly number[]): { tracks: readonly HistoryTrack[]; complete: boolean } {
  const v = useSyncExternalStore(subscribe, () => version)
  const key = ids.join(',')
  useEffect(() => {
    requestTracks(ids)
  }, [key])
  return useMemo(() => {
    const tracks: HistoryTrack[] = []
    let complete = true
    for (const id of ids) {
      const t = cache.get(id)
      if (t === undefined) {
        complete = false
      } else if (t !== null) {
        tracks.push(t)
      }
    }
    return { tracks, complete }
  }, [key, v])
}
