import { useEffect, useMemo, useRef, useState } from 'react'
import type { FlightDetail } from '../api/history'
import { useChartTheme } from '../stats/theme'
import { useECharts } from '../stats/useECharts'
import { clearHover, getHover, nearestIndex, setHover, subscribeHover } from './hover'
import { profileData, profileOption } from './profile'

/** flightUrl is one stored flight with its positions and samples. */
export function flightUrl(id: number): string {
  return `/api/history/flights/${String(id)}`
}

interface AxisPointerEvent {
  axesInfo?: { axisDim?: string; value?: number }[]
}

/**
 * FlightProfile charts the stored flight id (a lazy chunk with ECharts): altitude and ground speed over time, or
 * Mode-S altitude samples with squawk changes. Its hover and the map track's hover are shared (hover.ts).
 */
export default function FlightProfile({ id }: { id: number }) {
  const [detail, setDetail] = useState<{ id: number; d: FlightDetail | null } | null>(null)
  useEffect(() => {
    const ctl = new AbortController()
    fetch(flightUrl(id), { signal: ctl.signal })
      .then((r) => (r.ok ? (r.json() as Promise<FlightDetail>) : null))
      .then((d) => {
        setDetail({ id, d })
      })
      .catch(() => undefined)
    return () => {
      ctl.abort()
    }
  }, [id])
  const theme = useChartTheme()
  const d = detail?.id === id ? detail.d : undefined
  const data = useMemo(() => (d ? profileData(d) : null), [d])
  const option = useMemo(() => (data && data.t.length > 0 ? profileOption(data, theme) : null), [data, theme])
  const ref = useRef<HTMLDivElement>(null)
  const chart = useECharts(ref, option)
  const times = useRef<number[]>([])
  times.current = data?.t ?? []

  useEffect(() => {
    const c = chart.current
    if (c === null) {
      return
    }
    const onPointer = (e: unknown) => {
      const x = (e as AxisPointerEvent).axesInfo?.find((a) => a.axisDim === 'x')?.value
      const h = getHover()
      if (x === undefined || (h?.source === 'map' && h.flight === id)) {
        return
      }
      const i = nearestIndex(times.current, x)
      if (i >= 0) {
        setHover({ flight: id, t: times.current[i] ?? x, source: 'chart' })
      }
    }
    const onOut = () => {
      clearHover('chart')
    }
    c.on('updateAxisPointer', onPointer)
    c.on('globalout', onOut)
    const unsubscribe = subscribeHover(() => {
      const h = getHover()
      if (h?.source !== 'map') {
        return
      }
      if (h.flight !== id) {
        c.dispatchAction({ type: 'hideTip' })
        return
      }
      const i = nearestIndex(times.current, h.t)
      if (i >= 0) {
        c.dispatchAction({ type: 'showTip', seriesIndex: 0, dataIndex: i })
      }
    })
    return () => {
      unsubscribe()
      c.off('updateAxisPointer', onPointer)
      c.off('globalout', onOut)
      clearHover('chart')
    }
  }, [chart, id])

  return (
    <div className="history-profile" aria-label="Flight profile">
      {d === undefined && <p className="history-note">Loading the profile…</p>}
      {d === null && <p className="history-note">This flight is no longer stored.</p>}
      {data !== null && data.t.length === 0 && <p className="history-note">No positions or altitudes stored.</p>}
      <div ref={ref} className="history-chart" style={{ display: option === null ? 'none' : undefined }} />
    </div>
  )
}
