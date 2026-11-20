import { useRef, type ReactNode } from 'react'
import type { ChartClick, ChartOption } from './echarts'
import { useECharts } from './useECharts'

/** Chart is a titled card with one chart, or the note instead of it (loading, empty, disabled). */
export function Chart(props: {
  title: string
  hint?: ReactNode
  option: ChartOption | null
  note?: string | null
  height?: number
  onClick?: (p: ChartClick) => void
}) {
  const { title, hint, option, note, height = 280, onClick } = props
  return (
    <section className="stats-card" aria-label={title}>
      <h2>{title}</h2>
      {hint !== undefined && <p className="stats-hint">{hint}</p>}
      {note != null || option === null ? (
        <p className="stats-note" style={{ minHeight: height }}>
          {note ?? 'Loading…'}
        </p>
      ) : (
        <ChartCanvas option={option} height={height} onClick={onClick} />
      )}
    </section>
  )
}

function ChartCanvas({ option, height, onClick }: { option: ChartOption; height: number; onClick: ((p: ChartClick) => void) | undefined }) {
  const ref = useRef<HTMLDivElement>(null)
  useECharts(ref, option, onClick)
  return <div ref={ref} className={onClick ? 'stats-chart clickable' : 'stats-chart'} style={{ height }} />
}
