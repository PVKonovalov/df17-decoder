import { useEffect, type Dispatch } from 'react'
import type { Action } from '../state/reducer'
import type { Aircraft, Coverage, CoverageGrid, MapConfig, Receiver, Stats } from './types'

export const RECONNECT_MIN_MS = 1000
export const RECONNECT_MAX_MS = 30_000
/** Consecutive failed attempts after which the connection is shown as down (retries continue). */
export const DOWN_AFTER = 3

/** wsUrl returns the /api/ws URL on the page's own host, wss: for https pages. */
export function wsUrl(loc: Pick<Location, 'protocol' | 'host'>): string {
  return `${loc.protocol === 'https:' ? 'wss:' : 'ws:'}//${loc.host}/api/ws`
}

/** reconnectDelay returns the wait before retry number attempt (0-based): 1 s doubling up to 30 s. */
export function reconnectDelay(attempt: number): number {
  return Math.min(RECONNECT_MIN_MS * 2 ** attempt, RECONNECT_MAX_MS)
}

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null

/** toAction turns one /api/ws text message into a reducer action; unknown or malformed messages give null. */
export function toAction(raw: unknown): Action | null {
  if (typeof raw !== 'string') {
    return null
  }
  let msg: unknown
  try {
    msg = JSON.parse(raw)
  } catch {
    return null
  }
  if (!isObject(msg)) {
    return null
  }
  const { type, data } = msg
  switch (type) {
    case 'aircraft':
      return Array.isArray(data) ? { type: 'snapshot', aircraft: data as Aircraft[] } : null
    case 'receiver':
      return isObject(data) ? { type: 'receiver', receiver: data as unknown as Receiver } : null
    case 'map':
      return isObject(data) && typeof data.tileUrl === 'string'
        ? { type: 'map', map: data as unknown as MapConfig }
        : null
    case 'stats':
      return isObject(data) ? { type: 'stats', stats: data as unknown as Stats } : null
    case 'coverage':
      return isObject(data) && Array.isArray(data.points) && typeof data.bucketDeg === 'number'
        ? { type: 'coverage', coverage: data as unknown as Coverage }
        : null
    case 'coverageGrid':
      return isObject(data) &&
        Array.isArray(data.bands) &&
        typeof data.bucketDeg === 'number' &&
        typeof data.ringNm === 'number'
        ? { type: 'coverageGrid', grid: data as unknown as CoverageGrid }
        : null
    default:
      return null
  }
}

type SocketCtor = new (url: string) => WebSocket

/**
 * connectLive opens url, dispatches every message as an action and the connection state, and reconnects with
 * backoff until the returned stop function is called.
 */
export function connectLive(url: string, dispatch: Dispatch<Action>, Socket: SocketCtor = WebSocket): () => void {
  let ws: WebSocket | null = null
  let timer: ReturnType<typeof setTimeout> | undefined
  let attempt = 0
  let stopped = false

  const connect = () => {
    const sock = new Socket(url)
    ws = sock
    sock.onopen = () => {
      attempt = 0
      dispatch({ type: 'connection', conn: 'connected' })
    }
    sock.onmessage = (e: MessageEvent) => {
      const action = toAction(e.data)
      if (action !== null) {
        dispatch(action)
      }
    }
    sock.onclose = () => {
      if (stopped || ws !== sock) {
        return
      }
      ws = null
      const delay = reconnectDelay(attempt)
      attempt++
      dispatch({ type: 'connection', conn: attempt >= DOWN_AFTER ? 'down' : 'reconnecting' })
      timer = setTimeout(connect, delay)
    }
  }

  dispatch({ type: 'connection', conn: 'connecting' })
  connect()
  return () => {
    stopped = true
    clearTimeout(timer)
    ws?.close()
    ws = null
  }
}

/** useLiveData keeps a /api/ws connection open for the lifetime of the component. */
export function useLiveData(dispatch: Dispatch<Action>): void {
  useEffect(() => connectLive(wsUrl(window.location), dispatch), [dispatch])
}
