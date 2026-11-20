import { useState } from 'react'
import { dayString, localDay, RANGES, type RangeKey, type StatsRange } from './range'

/** RangePicker offers the presets and a custom span of local days; onChange gets the new selection. */
export function RangePicker({ range, onChange }: { range: StatsRange; onChange: (sel: { key: RangeKey; fromDay?: string; toDay?: string }) => void }) {
  const [custom, setCustom] = useState(range.key === 'custom')
  const [fromDay, setFrom] = useState(range.fromDay ?? dayString(range.from))
  const [toDay, setTo] = useState(range.toDay ?? dayString(new Date(range.to.getTime() - 1)))
  const apply = (f: string, t: string) => {
    const a = localDay(f)
    const b = localDay(t)
    if (a !== null && b !== null && a <= b) {
      onChange({ key: 'custom', fromDay: f, toDay: t })
    }
  }
  return (
    <div className="range-picker" role="group" aria-label="Range">
      {RANGES.map((r) => (
        <button
          key={r.key}
          type="button"
          aria-pressed={!custom && range.key === r.key}
          onClick={() => {
            setCustom(false)
            onChange({ key: r.key })
          }}
        >
          {r.label}
        </button>
      ))}
      <button
        type="button"
        aria-pressed={custom}
        onClick={() => {
          setCustom(true)
          apply(fromDay, toDay)
        }}
      >
        Custom
      </button>
      {custom && (
        <span className="range-custom">
          <input
            type="date"
            aria-label="From"
            value={fromDay}
            max={toDay}
            onChange={(e) => {
              setFrom(e.target.value)
              apply(e.target.value, toDay)
            }}
          />
          –
          <input
            type="date"
            aria-label="To"
            value={toDay}
            min={fromDay}
            onChange={(e) => {
              setTo(e.target.value)
              apply(fromDay, e.target.value)
            }}
          />
        </span>
      )}
    </div>
  )
}
