import type { FlightDetail } from '../api/history'
import type { ChartOption } from '../stats/echarts'
import type { ChartTheme } from '../stats/theme'

/** ProfileData is a flight's profile as time-ordered points: altitude and ground speed, or Mode-S samples. */
export interface ProfileData {
  modeS: boolean
  t: number[]
  alt: (number | null)[]
  gs: (number | null)[]
  squawks: { t: number; alt: number | null; squawk: string }[] // Mode-S squawk changes, the first included
}

/** profileData takes the positions of an ADS-B flight, else the samples (with their squawk changes). */
export function profileData(d: FlightDetail): ProfileData {
  if (d.positions.length > 0) {
    return {
      modeS: false,
      t: d.positions.map((p) => p.t),
      alt: d.positions.map((p) => p.alt ?? null),
      gs: d.positions.map((p) => p.gs ?? null),
      squawks: [],
    }
  }
  const squawks: ProfileData['squawks'] = []
  let last: string | undefined
  let lastAlt: number | null = null
  for (const s of d.samples) {
    lastAlt = s.alt ?? lastAlt
    if (s.squawk !== undefined && s.squawk !== last) {
      squawks.push({ t: s.t, alt: lastAlt, squawk: s.squawk })
      last = s.squawk
    }
  }
  return {
    modeS: true,
    t: d.samples.map((s) => s.t),
    alt: d.samples.map((s) => s.alt ?? null),
    gs: [],
    squawks,
  }
}

const timeLabel = (v: number) => new Date(v).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

/**
 * profileOption draws altitude over time (left axis) and, for ADS-B, ground speed (right axis); Mode-S samples
 * are dots with their squawk changes marked.
 */
export function profileOption(p: ProfileData, theme: ChartTheme): ChartOption {
  const axisLine = { lineStyle: { color: theme.border } }
  const axisLabel = { color: theme.muted, fontSize: 10 }
  const splitLine = { lineStyle: { color: theme.border, opacity: 0.5 } }
  return {
    animation: false,
    grid: { left: 44, right: p.modeS ? 12 : 40, top: 22, bottom: 22 },
    legend: { top: 0, right: 0, itemWidth: 12, itemHeight: 6, textStyle: { color: theme.muted, fontSize: 10 } },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'line' },
      backgroundColor: theme.surface,
      borderColor: theme.border,
      textStyle: { color: theme.text, fontSize: 11 },
    },
    xAxis: { type: 'time', axisLine, axisLabel: { ...axisLabel, formatter: timeLabel }, splitLine: { show: false } },
    yAxis: [
      { type: 'value', name: 'ft', nameTextStyle: axisLabel, axisLine, axisLabel, splitLine },
      ...(p.modeS ? [] : [{ type: 'value' as const, name: 'kt', nameTextStyle: axisLabel, axisLine, axisLabel, splitLine: { show: false } }]),
    ],
    series: [
      {
        type: 'line',
        name: 'Altitude',
        yAxisIndex: 0,
        data: p.t.map((t, i) => [t, p.alt[i] ?? null]),
        showSymbol: p.modeS,
        symbolSize: 4,
        connectNulls: false,
        lineStyle: { width: 1.5, color: theme.adsb },
        itemStyle: { color: theme.adsb },
        ...(p.squawks.length > 0 && {
          markPoint: {
            symbol: 'pin',
            symbolSize: 28,
            label: { fontSize: 9, color: '#fff' },
            itemStyle: { color: theme.modes },
            data: p.squawks.map((s) => ({ coord: [s.t, s.alt ?? 0], value: s.squawk, name: s.squawk })),
          },
        }),
      },
      ...(p.modeS
        ? []
        : [
            {
              type: 'line' as const,
              name: 'Ground speed',
              yAxisIndex: 1,
              data: p.t.map((t, i) => [t, p.gs[i] ?? null]),
              showSymbol: false,
              connectNulls: false,
              lineStyle: { width: 1, color: theme.line },
              itemStyle: { color: theme.line },
            },
          ]),
    ],
  }
}
