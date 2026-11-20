import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Action } from '../state/reducer'
import { connectLive, DOWN_AFTER, parseDuration, reconnectDelay, toAction, wsUrl } from './live'

describe('wsUrl', () => {
  it('uses ws: or wss: on the page host', () => {
    expect(wsUrl({ protocol: 'http:', host: 'pi:8080' })).toBe('ws://pi:8080/api/ws')
    expect(wsUrl({ protocol: 'https:', host: 'adsb.example' })).toBe('wss://adsb.example/api/ws')
  })
})

describe('reconnectDelay', () => {
  it('doubles from 1 s and caps at 30 s', () => {
    expect([0, 1, 2, 3, 4, 5, 10].map(reconnectDelay)).toEqual([1000, 2000, 4000, 8000, 16000, 30000, 30000])
  })
})

describe('parseDuration', () => {
  it('parses Go durations', () => {
    expect(parseDuration('10m0s')).toBe(600_000)
    expect(parseDuration('1h30m')).toBe(5_400_000)
    expect(parseDuration('1.5s')).toBe(1500)
    expect(parseDuration('500ms')).toBe(500)
    for (const bad of ['', '10', 'm', '10x', '-1s']) {
      expect(parseDuration(bad)).toBeNaN()
    }
  })
})

describe('toAction', () => {
  it('maps known messages', () => {
    expect(toAction('{"type":"aircraft","data":[]}')).toEqual({ type: 'snapshot', aircraft: [] })
    expect(toAction('{"type":"receiver","data":{"lat":1,"lon":2,"maxRangeNm":3}}')).toEqual({
      type: 'receiver',
      receiver: { lat: 1, lon: 2, maxRangeNm: 3 },
    })
    expect(toAction('{"type":"stats","data":{"frames":1}}')?.type).toBe('stats')
    const map = { tileUrl: 'http://t.lan/{z}/{x}/{y}.png', attribution: 'lan', maxZoom: 17, darkFilter: false }
    expect(toAction(JSON.stringify({ type: 'map', data: map }))).toEqual({ type: 'map', map })
    const coverage = { receiver: { lat: 1, lon: 2 }, bucketDeg: 2, points: [] }
    expect(toAction(JSON.stringify({ type: 'coverage', data: coverage }))).toEqual({ type: 'coverage', coverage })
    const grid = { receiver: { lat: 1, lon: 2 }, bucketDeg: 2, ringNm: 5, bands: [] }
    expect(toAction(JSON.stringify({ type: 'coverageGrid', data: grid }))).toEqual({ type: 'coverageGrid', grid })
    const trails = { '738001': [{ t: 1, lat: 2, lon: 3 }] }
    const windowMs = 600_000
    expect(toAction(JSON.stringify({ type: 'trails', data: { window: '10m0s', trails } }))).toEqual({ type: 'trails', windowMs, trails })
    expect(toAction(JSON.stringify({ type: 'trailPoints', data: trails }))).toEqual({ type: 'trailPoints', points: trails })
  })

  it('ignores unknown, malformed and binary messages', () => {
    for (const raw of ['{"type":"future","data":{}}', '{"type":"trails","data":{"window":"soon","trails":{}}}', '{"type":"trailPoints","data":[]}', '{"type":"aircraft","data":{}}', '{"type":"map","data":{}}', '{"type":"coverage","data":{"bucketDeg":2}}', '{"type":"coverageGrid","data":{"bucketDeg":2,"ringNm":5}}', 'not json', '42', null]) {
      expect(toAction(raw)).toBeNull()
    }
    expect(toAction(new ArrayBuffer(2))).toBeNull()
  })
})

class FakeSocket {
  static all: FakeSocket[] = []
  onopen: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onclose: (() => void) | null = null
  closed = false
  constructor(readonly url: string) {
    FakeSocket.all.push(this)
  }
  close() {
    this.closed = true
  }
}

describe('connectLive', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeSocket.all = []
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  const start = () => {
    const actions: Action[] = []
    const stop = connectLive('ws://x/api/ws', (a) => actions.push(a), FakeSocket as unknown as typeof WebSocket)
    const conns = () => actions.filter((a) => a.type === 'connection').map((a) => a.conn)
    return { actions, stop, conns, sock: (i: number) => FakeSocket.all[i] as FakeSocket }
  }

  it('dispatches messages and connection state', () => {
    const { actions, conns, sock } = start()
    sock(0).onopen?.()
    sock(0).onmessage?.({ data: '{"type":"aircraft","data":[]}' })
    sock(0).onmessage?.({ data: '{"type":"nope"}' })
    expect(conns()).toEqual(['connecting', 'connected'])
    expect(actions.filter((a) => a.type === 'snapshot')).toHaveLength(1)
  })

  it('reconnects with backoff, reports down after repeated failures and resets after open', () => {
    const { conns, sock } = start()
    for (let i = 0; i < DOWN_AFTER; i++) {
      sock(i).onclose?.()
      vi.advanceTimersByTime(reconnectDelay(i) - 1)
      expect(FakeSocket.all).toHaveLength(i + 1)
      vi.advanceTimersByTime(1)
      expect(FakeSocket.all).toHaveLength(i + 2)
    }
    expect(conns()).toEqual(['connecting', 'reconnecting', 'reconnecting', 'down'])

    sock(DOWN_AFTER).onopen?.()
    sock(DOWN_AFTER).onclose?.()
    expect(conns().at(-1)).toBe('reconnecting')
    vi.advanceTimersByTime(reconnectDelay(0))
    expect(FakeSocket.all).toHaveLength(DOWN_AFTER + 2)
  })

  it('stops: closes the socket and does not reconnect', () => {
    const { stop, sock, conns } = start()
    sock(0).onopen?.()
    stop()
    expect(sock(0).closed).toBe(true)
    sock(0).onclose?.()
    vi.advanceTimersByTime(60_000)
    expect(FakeSocket.all).toHaveLength(1)
    expect(conns()).toEqual(['connecting', 'connected'])
  })
})
