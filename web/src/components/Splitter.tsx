import { useState } from 'react'

const KEY = 'df17.split'
const MIN = 25
const MAX = 85
export const DEFAULT_SPLIT = 65

export const clampSplit = (pct: number) => Math.min(MAX, Math.max(MIN, pct))

/** useSplit returns the map pane width in percent, remembered per browser. */
export function useSplit(): [number, (pct: number) => void] {
  const [split, setSplit] = useState(() => {
    try {
      const v = Number(localStorage.getItem(KEY))
      return v > 0 ? clampSplit(v) : DEFAULT_SPLIT
    } catch {
      return DEFAULT_SPLIT
    }
  })
  const update = (pct: number) => {
    const v = clampSplit(pct)
    setSplit(v)
    try {
      localStorage.setItem(KEY, String(Math.round(v)))
    } catch {
      // storage unavailable: keep the split for this session only
    }
  }
  return [split, update]
}

/** Splitter is a vertical drag handle between the map and the table (desktop layout only). */
export function Splitter({ onChange }: { onChange: (pct: number) => void }) {
  return (
    <div
      className="splitter"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize map and table"
      onPointerDown={(e) => {
        const parent = e.currentTarget.parentElement
        if (parent === null) {
          return
        }
        e.currentTarget.setPointerCapture(e.pointerId)
        const rect = parent.getBoundingClientRect()
        const move = (ev: PointerEvent) => {
          onChange(((ev.clientX - rect.left) / rect.width) * 100)
        }
        const target = e.currentTarget
        const up = () => {
          target.removeEventListener('pointermove', move)
          target.removeEventListener('pointerup', up)
        }
        target.addEventListener('pointermove', move)
        target.addEventListener('pointerup', up)
      }}
    />
  )
}
