import { useState } from 'react'
import type { ColorMode } from './style'

const KEY = 'df17.colorMode'

/** useColorMode returns the marker color mode, remembered per browser (altitude by default). */
export function useColorMode(): [ColorMode, (m: ColorMode) => void] {
  const [mode, setMode] = useState<ColorMode>(() => {
    try {
      return localStorage.getItem(KEY) === 'signal' ? 'signal' : 'altitude'
    } catch {
      return 'altitude'
    }
  })
  const update = (m: ColorMode) => {
    setMode(m)
    try {
      localStorage.setItem(KEY, m)
    } catch {
      // storage unavailable: keep the mode for this session only
    }
  }
  return [mode, update]
}
