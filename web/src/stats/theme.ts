import { useEffect, useState } from 'react'

/** ChartTheme holds the CSS tokens the charts are drawn with, so they follow the light and dark themes. */
export interface ChartTheme {
  text: string
  muted: string
  border: string
  surface: string
  adsb: string
  modes: string
  line: string
  other: string
  bad: string
  heatLow: string
  heatHigh: string
}

const TOKENS: Record<keyof ChartTheme, [string, string]> = {
  text: ['--text', '#1c2128'],
  muted: ['--muted', '#6b7380'],
  border: ['--border', '#d9dde3'],
  surface: ['--surface', '#ffffff'],
  adsb: ['--chart-adsb', '#2a7de1'],
  modes: ['--chart-modes', '#bf8700'],
  line: ['--chart-line', '#8250df'],
  other: ['--chart-other', '#9aa4b2'],
  bad: ['--bad', '#cf222e'],
  heatLow: ['--chart-heat-low', '#eef3fb'],
  heatHigh: ['--chart-heat-high', '#1f5fb8'],
}

/** readTheme reads the chart tokens from the document, with the light values as fallbacks. */
export function readTheme(): ChartTheme {
  const css = getComputedStyle(document.documentElement)
  const out = {} as ChartTheme
  for (const [k, [name, fallback]] of Object.entries(TOKENS) as [keyof ChartTheme, [string, string]][]) {
    out[k] = css.getPropertyValue(name).trim() || fallback
  }
  return out
}

/** useChartTheme returns the chart tokens, re-read when the color scheme changes. */
export function useChartTheme(): ChartTheme {
  const [theme, setTheme] = useState(readTheme)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') {
      return
    }
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const update = () => {
      setTheme(readTheme())
    }
    mq.addEventListener('change', update)
    return () => {
      mq.removeEventListener('change', update)
    }
  }, [])
  return theme
}
