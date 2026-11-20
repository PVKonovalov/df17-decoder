import { render } from '@testing-library/react'
import { useRef } from 'react'
import { describe, expect, it, vi } from 'vitest'
import type { ChartOption } from './echarts'
import { useECharts } from './useECharts'

const fake = vi.hoisted(() => {
  const chart = { on: vi.fn(), setOption: vi.fn(), resize: vi.fn(), dispose: vi.fn() }
  return { chart, init: vi.fn(() => chart) }
})
vi.mock('./echarts', () => ({ initChart: fake.init }))

function Probe({ option, onClick }: { option: ChartOption | null; onClick?: (p: { dataIndex: number }) => void }) {
  const ref = useRef<HTMLDivElement>(null)
  useECharts(ref, option, onClick)
  return <div ref={ref} />
}

describe('useECharts', () => {
  it('creates the chart once, replaces its option, forwards clicks and disposes on unmount', () => {
    const a: ChartOption = { series: [] }
    const b: ChartOption = { series: [{ type: 'bar', data: [1] }] }
    const clicks: number[] = []
    const { rerender, unmount } = render(<Probe option={a} onClick={(p) => clicks.push(p.dataIndex)} />)
    rerender(<Probe option={b} onClick={(p) => clicks.push(p.dataIndex * 10)} />)
    expect(fake.init).toHaveBeenCalledTimes(1)
    expect(fake.chart.setOption).toHaveBeenLastCalledWith(b, { notMerge: true })
    const handler = fake.chart.on.mock.calls[0]?.[1] as (p: { dataIndex: number }) => void
    handler({ dataIndex: 2 })
    expect(clicks).toEqual([20]) // the latest handler
    expect(fake.chart.dispose).not.toHaveBeenCalled()
    unmount()
    expect(fake.chart.dispose).toHaveBeenCalledTimes(1)
  })
})
