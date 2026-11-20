import { useEffect, useRef, type RefObject } from 'react'
import { initChart, type ChartClick, type ChartOption } from './echarts'

/**
 * useECharts draws option into the element of ref: it creates the chart on mount, replaces its option on change,
 * resizes it with the element and disposes it on unmount. onClick gets data clicks.
 */
export function useECharts(ref: RefObject<HTMLElement | null>, option: ChartOption | null, onClick?: (p: ChartClick) => void) {
  const chart = useRef<ReturnType<typeof initChart> | null>(null)
  const click = useRef(onClick)
  useEffect(() => {
    click.current = onClick
  })

  useEffect(() => {
    const el = ref.current
    if (el === null) {
      return
    }
    const c = initChart(el)
    chart.current = c
    c.on('click', (p) => {
      click.current?.(p)
    })
    const ro = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(() => {
      c.resize()
    })
    ro?.observe(el)
    return () => {
      ro?.disconnect()
      c.dispose()
      chart.current = null
    }
  }, [ref])

  useEffect(() => {
    if (option !== null) {
      chart.current?.setOption(option, { notMerge: true })
    }
  }, [option])
}
