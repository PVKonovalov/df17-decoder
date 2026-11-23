import { describe, expect, it } from 'vitest'
import type { HourRow } from '../api/stats'
import { altitudeOption, healthOption, hourlyOption, qualityOption, rankingOption } from './charts'
import type { ChartTheme } from './theme'

const theme: ChartTheme = {
  text: '#000', muted: '#666', border: '#ccc', surface: '#fff', adsb: 'blue', modes: 'amber', line: 'purple',
  other: 'grey', bad: 'red', warn: 'orange', heatLow: '#eee', heatHigh: '#00f',
}
const row = (h: number, extra: Partial<HourRow> = {}): HourRow => ({
  hour: new Date(Date.UTC(2026, 10, 20, h)).toISOString(), aircraftAdsb: 2, aircraftModes: 1, flightsStarted: 0, positions: 0, ...extra,
})
type Series = { name?: string; data: unknown[]; yAxisIndex?: number }[]

describe('chart options', () => {
  it('draws the partial hour lighter and zooms long ranges', () => {
    const o = hourlyOption([row(10), row(11, { partial: true })], theme, 'UTC')
    const bars = (o.series as Series)[0]?.data as { itemStyle?: { opacity: number } }[]
    expect([bars[0]?.itemStyle, bars[1]?.itemStyle?.opacity]).toEqual([undefined, 0.4])
    expect(o.dataZoom).toEqual([])
    const long = hourlyOption(Array.from({ length: 100 }, (_, i) => row(i % 24)), theme, 'UTC')
    expect((long.dataZoom as unknown[]).length).toBe(2)
  })

  it('mutes Other in rankings', () => {
    const o = rankingOption([{ name: 'KLM', aircraftHours: 5 }, { name: 'Other', aircraftHours: 9 }], theme, 'purple')
    const data = (o.series as Series)[0]?.data as { itemStyle: { color: string } }[]
    expect(data.map((d) => d.itemStyle.color)).toEqual(['purple', 'grey'])
  })

  it('shows frames, the CRC share and silent minutes, unknown hours as gaps', () => {
    const o = healthOption([row(10, { frames: 2000, crcFailures: 500, silentMinutes: 4 }), row(11)], theme, 'UTC')
    const [frames, crc, silent] = o.series as Series
    expect([frames?.data, crc?.data, silent?.data, silent?.yAxisIndex]).toEqual([[2000, null], [25, null], [4, null], 1])
  })

  it('stacks ADS-B aircraft by position quality with the lost or degraded share, old hours as gaps', () => {
    const q = { aircraftAdsb: 10, adsbPositionOk: 6, adsbNoPosition: 1, adsbDegraded: 2, adsbLost: 1 }
    const o = qualityOption([row(10, q), row(11), row(12, { ...q, partial: true })], theme, 'UTC')
    const series = o.series as Series
    expect(series.map((s) => s.name)).toEqual(['Position OK', 'No position received', 'Degraded (NIC < 7 or NACp < 8)', 'Position lost (TC 0)', '% lost or degraded'])
    expect(series[3]?.data).toEqual([{ value: 1 }, { value: null }, { value: 1, itemStyle: { opacity: 0.4 } }])
    expect(series[4]?.data).toEqual([30, null, 30])
  })

  it('colors unknown and ground apart from altitudes', () => {
    const o = altitudeOption([{ band: 'ground', positions: 1 }, { band: '0–5k', minFt: 0, positions: 3 }, { band: 'unknown', positions: 0 }], theme)
    const data = (o.series as Series)[0]?.data as { itemStyle: { color: string } }[]
    expect(data.map((d) => d.itemStyle.color)).toEqual(['grey', 'blue', 'grey'])
  })
})
