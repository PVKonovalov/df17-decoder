import { useSyncExternalStore } from 'react'

/** Hover is a moment of a stored flight pointed at on the map track or the profile chart. */
export interface Hover {
  flight: number
  t: number // unix ms
  source: 'map' | 'chart'
}

let current: Hover | null = null
const listeners = new Set<() => void>()

/** setHover shares the hovered moment; null clears it. Unchanged values don't notify. */
export function setHover(h: Hover | null): void {
  if (h === current || (h !== null && current !== null && h.flight === current.flight && h.t === current.t && h.source === current.source)) {
    return
  }
  current = h
  for (const l of listeners) {
    l()
  }
}

/** clearHover clears the hover only when it came from source. */
export function clearHover(source: Hover['source']): void {
  if (current?.source === source) {
    setHover(null)
  }
}

export function getHover(): Hover | null {
  return current
}

export function subscribeHover(l: () => void): () => void {
  listeners.add(l)
  return () => {
    listeners.delete(l)
  }
}

export function useHover(): Hover | null {
  return useSyncExternalStore(subscribeHover, getHover)
}

/** nearestIndex returns the index of the time in ts (ascending) closest to t; -1 for an empty list. */
export function nearestIndex(ts: readonly number[], t: number): number {
  if (ts.length === 0) {
    return -1
  }
  let lo = 0
  let hi = ts.length - 1
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if ((ts[mid] ?? 0) < t) {
      lo = mid + 1
    } else {
      hi = mid
    }
  }
  return lo > 0 && t - (ts[lo - 1] ?? 0) <= (ts[lo] ?? 0) - t ? lo - 1 : lo
}
