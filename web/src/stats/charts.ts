import type { AltitudeBand, HourRow, RangeDay } from '../api/stats'
import { bestDay, crcShare, OTHER, WEEKDAYS, type Heatmap, type Ranked } from './aggregate'
import type { ChartOption } from './echarts'
import type { ChartTheme } from './theme'

const ZOOM_FROM = 72 // hours: longer series get a zoom slider

function base(t: ChartTheme) {
  const axis = {
    axisLine: { lineStyle: { color: t.border } },
    axisLabel: { color: t.muted },
    splitLine: { lineStyle: { color: t.border, opacity: 0.6 } },
  }
  const tooltip = { backgroundColor: t.surface, borderColor: t.border, textStyle: { color: t.text } }
  return { axis, tooltip, textStyle: { color: t.text }, grid: { left: 8, right: 16, top: 36, bottom: 8, containLabel: true } }
}

/** hourLabel formats an hour start for an axis in tz: "20 Nov 14:00". */
export function hourLabel(iso: string, tz: string): string {
  return new Intl.DateTimeFormat('en-GB', { timeZone: tz, day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
    .format(new Date(iso))
}

/** hourlyOption stacks ADS-B and Mode-S-only aircraft per hour (the partial current hour lighter), messages on a second axis. */
export function hourlyOption(rows: HourRow[], t: ChartTheme, tz: string): ChartOption {
  const b = base(t)
  const bar = (v: (r: HourRow) => number) =>
    rows.map((r) => (r.partial === true ? { value: v(r), itemStyle: { opacity: 0.4 } } : { value: v(r) }))
  const hasMessages = rows.some((r) => r.messages !== undefined)
  return {
    textStyle: b.textStyle,
    grid: { ...b.grid, bottom: rows.length > ZOOM_FROM ? 48 : 8 },
    legend: { textStyle: { color: t.text }, top: 0 },
    tooltip: { ...b.tooltip, trigger: 'axis', axisPointer: { type: 'shadow' } },
    xAxis: { type: 'category', data: rows.map((r) => hourLabel(r.hour, tz) + (r.partial === true ? ' (so far)' : '')), ...b.axis, splitLine: { show: false } },
    yAxis: [
      { type: 'value', name: 'aircraft', nameTextStyle: { color: t.muted }, minInterval: 1, ...b.axis },
      ...(hasMessages ? [{ type: 'value' as const, name: 'messages', nameTextStyle: { color: t.muted }, ...b.axis, splitLine: { show: false } }] : []),
    ],
    dataZoom: rows.length > ZOOM_FROM ? [{ type: 'inside' }, { type: 'slider', height: 20, bottom: 8, textStyle: { color: t.muted } }] : [],
    series: [
      { name: 'ADS-B', type: 'bar', stack: 'aircraft', data: bar((r) => r.aircraftAdsb), itemStyle: { color: t.adsb } },
      { name: 'Mode-S only', type: 'bar', stack: 'aircraft', data: bar((r) => r.aircraftModes), itemStyle: { color: t.modes } },
      ...(hasMessages
        ? [{ name: 'Messages', type: 'line' as const, yAxisIndex: 1, showSymbol: false, data: rows.map((r) => r.messages ?? null), itemStyle: { color: t.line }, lineStyle: { width: 1 } }]
        : []),
    ],
  }
}

const HOURS = Array.from({ length: 24 }, (_, h) => String(h).padStart(2, '0'))

/** heatmapOption draws the 7 × 24 average aircraft per local hour, Monday on top. */
export function heatmapOption(hm: Heatmap, t: ChartTheme): ChartOption {
  const b = base(t)
  return {
    textStyle: b.textStyle,
    grid: { ...b.grid, top: 8, bottom: 48 },
    tooltip: {
      ...b.tooltip,
      formatter: (p: unknown) => {
        const [h, d, v] = (p as { value: [number, number, number] }).value
        return `${WEEKDAYS[d] ?? ''} ${HOURS[h] ?? ''}:00 — ${v} aircraft on average`
      },
    },
    xAxis: { type: 'category', data: HOURS, ...b.axis, splitArea: { show: false } },
    yAxis: { type: 'category', data: [...WEEKDAYS], inverse: true, ...b.axis },
    visualMap: {
      min: 0, max: Math.max(1, hm.max), calculable: false, orient: 'horizontal', left: 'center', bottom: 0,
      itemHeight: 120, textStyle: { color: t.muted }, inRange: { color: [t.heatLow, t.heatHigh] },
    },
    series: [{ type: 'heatmap', data: hm.cells, itemStyle: { borderColor: t.surface, borderWidth: 1 }, emphasis: { itemStyle: { borderColor: t.text } } }],
  }
}

/** rankingOption is a horizontal bar per country or prefix, largest on top, "Other" muted. */
export function rankingOption(list: Ranked[], t: ChartTheme, color: string): ChartOption {
  const b = base(t)
  return {
    textStyle: b.textStyle,
    grid: { ...b.grid, top: 8 },
    tooltip: { ...b.tooltip, trigger: 'axis', axisPointer: { type: 'shadow' }, valueFormatter: (v) => `${String(v)} aircraft-hours` },
    xAxis: { type: 'value', minInterval: 1, ...b.axis },
    yAxis: { type: 'category', data: list.map((c) => c.name), inverse: true, ...b.axis, splitLine: { show: false } },
    series: [{
      type: 'bar',
      data: list.map((c) => ({ value: c.aircraftHours, itemStyle: { color: c.name === OTHER ? t.other : color } })),
    }],
  }
}

/** rangeOption draws the furthest position per day, with a marker on the best day. */
export function rangeOption(days: RangeDay[], t: ChartTheme): ChartOption {
  const b = base(t)
  const best = days[bestDay(days)]
  return {
    textStyle: b.textStyle,
    tooltip: {
      ...b.tooltip,
      trigger: 'axis',
      formatter: (p: unknown) => {
        const d = days[(p as { dataIndex: number }[])[0]?.dataIndex ?? -1]
        return d === undefined ? '' : `${d.day}: ${d.maxRangeNm.toFixed(1)} NM${d.bearing === undefined ? '' : ` at ${Math.round(d.bearing)}°`}`
      },
    },
    grid: { ...b.grid, top: 40 },
    xAxis: { type: 'category', data: days.map((d) => d.day), boundaryGap: false, ...b.axis, splitLine: { show: false } },
    yAxis: { type: 'value', name: 'NM', nameTextStyle: { color: t.muted }, ...b.axis },
    series: [{
      type: 'line',
      data: days.map((d) => Math.round(d.maxRangeNm * 10) / 10),
      itemStyle: { color: t.line },
      symbolSize: 6,
      ...(best === undefined ? {} : {
        markPoint: {
          data: [{ name: 'Best day', coord: [best.day, Math.round(best.maxRangeNm * 10) / 10], value: Math.round(best.maxRangeNm) }],
          itemStyle: { color: t.line },
          label: { color: '#fff' },
        },
      }),
    }],
  }
}

/** healthOption shows frames per hour, the CRC failure share and silent minutes; the tooltip lists every counter. */
export function healthOption(rows: HourRow[], t: ChartTheme, tz: string): ChartOption {
  const b = base(t)
  const fmt = (v: number | undefined, unit = '') => (v === undefined ? '—' : `${v.toLocaleString()}${unit}`)
  return {
    textStyle: b.textStyle,
    grid: { ...b.grid, bottom: rows.length > ZOOM_FROM ? 48 : 8 },
    legend: { textStyle: { color: t.text }, top: 0 },
    tooltip: {
      ...b.tooltip,
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      formatter: (p: unknown) => {
        const r = rows[(p as { dataIndex: number }[])[0]?.dataIndex ?? -1]
        if (r === undefined) {
          return ''
        }
        const share = crcShare(r)
        return [
          `<b>${hourLabel(r.hour, tz)}${r.partial === true ? ' (so far)' : ''}</b>`,
          `Frames: ${fmt(r.frames)}`,
          `CRC failures: ${fmt(r.crcFailures)}${share === null ? '' : ` (${share}%)`}`,
          `Bit errors fixed: ${fmt(r.fixedBitErrors)}`,
          `CPR rejected: ${fmt(r.cprRejected)}`,
          `Reconnects: ${fmt(r.reconnects)}`,
          `Silent minutes: ${fmt(r.silentMinutes)}`,
          `Mean signal: ${r.signalDbfs === undefined ? '—' : `${r.signalDbfs.toFixed(1)} dBFS`}`,
        ].join('<br>')
      },
    },
    xAxis: { type: 'category', data: rows.map((r) => hourLabel(r.hour, tz)), ...b.axis, splitLine: { show: false } },
    yAxis: [
      { type: 'value', name: 'frames', nameTextStyle: { color: t.muted }, ...b.axis },
      { type: 'value', name: '% · min', min: 0, max: 100, nameTextStyle: { color: t.muted }, ...b.axis, splitLine: { show: false } },
    ],
    dataZoom: rows.length > ZOOM_FROM ? [{ type: 'inside' }, { type: 'slider', height: 20, bottom: 8, textStyle: { color: t.muted } }] : [],
    series: [
      { name: 'Frames', type: 'bar', data: rows.map((r) => r.frames ?? null), itemStyle: { color: t.adsb, opacity: 0.7 } },
      { name: 'CRC failures %', type: 'line', yAxisIndex: 1, showSymbol: false, data: rows.map(crcShare), itemStyle: { color: t.line } },
      { name: 'Silent minutes', type: 'bar', yAxisIndex: 1, data: rows.map((r) => r.silentMinutes ?? null), itemStyle: { color: t.bad } },
    ],
  }
}

/** altitudeOption is a histogram of stored positions per altitude band, ground first; the tooltip gives shares. */
export function altitudeOption(bands: AltitudeBand[], t: ChartTheme): ChartOption {
  const b = base(t)
  const total = bands.reduce((s, x) => s + x.positions, 0)
  return {
    textStyle: b.textStyle,
    grid: { ...b.grid, top: 8 },
    tooltip: {
      ...b.tooltip,
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      valueFormatter: (v) => `${String(v)} positions (${total === 0 ? 0 : Math.round((Number(v) / total) * 1000) / 10}%)`,
    },
    xAxis: { type: 'category', data: bands.map((x) => x.band), ...b.axis, splitLine: { show: false } },
    yAxis: { type: 'value', name: 'positions', nameTextStyle: { color: t.muted }, ...b.axis },
    series: [{
      type: 'bar',
      data: bands.map((x) => ({ value: x.positions, itemStyle: { color: x.minFt === undefined ? t.other : t.adsb } })),
    }],
  }
}
