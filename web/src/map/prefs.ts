import { useState } from 'react'
import type { HeatBand } from './heatmap'
import type { ColorMode } from './style'

/**
 * useStored is a useState whose value is remembered per browser under key; parse maps the stored string (null
 * when absent or storage is unavailable) to a value.
 */
export function useStored<T extends string | boolean>(key: string, parse: (raw: string | null) => T): [T, (v: T) => void] {
  const [value, setValue] = useState<T>(() => {
    try {
      return parse(localStorage.getItem(key))
    } catch {
      return parse(null)
    }
  })
  const update = (v: T) => {
    setValue(v)
    try {
      localStorage.setItem(key, String(v))
    } catch {
      // storage unavailable: keep the value for this session only
    }
  }
  return [value, update]
}

/** useColorMode returns the marker color mode, remembered per browser (altitude by default). */
export function useColorMode(): [ColorMode, (m: ColorMode) => void] {
  return useStored<ColorMode>('df17.colorMode', (raw) => (raw === 'signal' ? 'signal' : 'altitude'))
}

export type CoverageView = 'off' | 'outline' | 'signal' | 'count'

const VIEWS: readonly CoverageView[] = ['off', 'outline', 'signal', 'count']
const BANDS: readonly HeatBand[] = ['all', 'low', 'mid', 'high']

/**
 * useCoverageView returns the coverage view (off, range outline, signal or count heatmap), remembered per browser
 * (off by default; the earlier stored value "true" means the outline).
 */
export function useCoverageView(): [CoverageView, (v: CoverageView) => void] {
  return useStored<CoverageView>('df17.coverage', (raw) =>
    raw === 'true' ? 'outline' : (VIEWS.find((v) => v === raw) ?? 'off'),
  )
}

/** useHeatBand returns the heatmap's altitude band, remembered per browser (all by default). */
export function useHeatBand(): [HeatBand, (b: HeatBand) => void] {
  return useStored<HeatBand>('df17.heatBand', (raw) => BANDS.find((b) => b === raw) ?? 'all')
}

/**
 * usePanel returns whether a UI panel or map layer (the aircraft table, the map legend, the waypoints) is shown,
 * remembered per browser (shown by default).
 */
export function usePanel(name: 'table' | 'legend' | 'waypoints'): [boolean, (on: boolean) => void] {
  return useStored<boolean>(`df17.show.${name}`, (raw) => raw !== 'false')
}
