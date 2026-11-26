import type { DailyRow, DailyTotals } from '../api/stats'
import type { ChartOption } from './echarts'
import type { ChartTheme } from './theme'

export const DAILY_ZOOM_FROM = 60 // days: longer series get a zoom slider

/** percentNew is the share of new aircraft in whole percent, or null without aircraft. */
export function percentNew(r: Pick<DailyRow, 'aircraft' | 'new'>): number | null {
  return r.aircraft === 0 ? null : Math.round((r.new / r.aircraft) * 100)
}

/** dayLabel formats a server day 'YYYY-MM-DD' as "Tue 25 Nov" (the day itself, in no timezone). */
export function dayLabel(day: string): string {
  const d = new Date(`${day}T12:00:00Z`)
  return Number.isNaN(d.getTime())
    ? day
    : new Intl.DateTimeFormat('en-GB', { timeZone: 'UTC', weekday: 'short', day: 'numeric', month: 'short' }).format(d)
}

/** zoneNote names the server's day timezone when it isn't the browser's, else null. */
export function zoneNote(server: string, browser: string): string | null {
  return server === '' || server === browser ? null : `Days are in the server's timezone, ${server}.`
}

/** todayAndYesterday picks today (the partial day) and the day before it from the rows. */
export function todayAndYesterday(days: readonly DailyRow[]): { today?: DailyRow | undefined; yesterday?: DailyRow | undefined } {
  const i = days.findIndex((d) => d.partial)
  if (i < 0) {
    return {}
  }
  return { today: days[i], yesterday: days[i - 1] }
}

const esc = (s: string) => s.replace(/[&<>"]/g, (c) => `&#${String(c.charCodeAt(0))};`)

/** dailyTooltip lists every field of a day (HTML). */
export function dailyTooltip(r: DailyRow, note: string | null): string {
  const pct = percentNew(r)
  return [
    `<strong>${esc(dayLabel(r.day))}${r.partial ? ' (so far)' : ''}</strong>`,
    `Aircraft: ${String(r.aircraft)} (ADS-B ${String(r.adsb)}, Mode-S only ${String(r.modeS)})`,
    `New: ${String(r.new)} (ADS-B ${String(r.newAdsb)}, Mode-S only ${String(r.newModeS)})`,
    `Returning: ${String(r.returning)}`,
    `New share: ${pct === null ? '—' : `${String(pct)}%`}`,
    r.learning ? '<em>Learning period: recording had just started, so most aircraft are new.</em>' : '',
    r.approximate ? '<em>Approximate: some flights of this day are no longer stored or were counted in another timezone.</em>' : '',
    note === null ? '' : `<span style="opacity:.7">${esc(note)}</span>`,
  ]
    .filter((l) => l !== '')
    .join('<br>')
}

/** hatch is the decal of the learning-period bars. */
export const HATCH = { symbol: 'rect', dashArrayX: [1, 0], dashArrayY: [2, 4], rotation: -Math.PI / 4, color: 'rgba(255,255,255,0.55)' }

/**
 * dailyOption stacks new (accent) and returning (muted) aircraft per day with % new on a second axis; learning
 * days are hatched and today (partial) is lighter.
 */
export function dailyOption(d: DailyTotals, t: ChartTheme, browserTz: string): ChartOption {
  const rows = d.days
  const note = zoneNote(d.timezone, browserTz)
  const bar = (v: (r: DailyRow) => number) =>
    rows.map((r) => ({
      value: v(r),
      itemStyle: { ...(r.partial ? { opacity: 0.4 } : {}), ...(r.learning ? { decal: HATCH } : {}) },
    }))
  const axis = {
    axisLine: { lineStyle: { color: t.border } },
    axisLabel: { color: t.muted },
    splitLine: { lineStyle: { color: t.border, opacity: 0.6 } },
  }
  const zoom = rows.length > DAILY_ZOOM_FROM
  const hasLearning = rows.some((r) => r.learning)
  return {
    textStyle: { color: t.text },
    grid: { left: 8, right: 16, top: 36, bottom: zoom ? 48 : 8, containLabel: true },
    legend: { textStyle: { color: t.text }, top: 0 },
    tooltip: {
      backgroundColor: t.surface,
      borderColor: t.border,
      textStyle: { color: t.text },
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      formatter: (params: unknown) => {
        const first = (Array.isArray(params) ? params[0] : params) as { dataIndex?: number } | undefined
        const r = first?.dataIndex === undefined ? undefined : rows[first.dataIndex]
        return r === undefined ? '' : dailyTooltip(r, note)
      },
    },
    xAxis: { type: 'category', data: rows.map((r) => dayLabel(r.day) + (r.partial ? ' (so far)' : '')), ...axis, splitLine: { show: false } },
    yAxis: [
      { type: 'value', name: 'aircraft', nameTextStyle: { color: t.muted }, minInterval: 1, ...axis },
      { type: 'value', name: '% new', min: 0, max: 100, nameTextStyle: { color: t.muted }, ...axis, splitLine: { show: false } },
    ],
    dataZoom: zoom ? [{ type: 'inside' }, { type: 'slider', height: 20, bottom: 8, textStyle: { color: t.muted } }] : [],
    series: [
      { name: 'New', type: 'bar', stack: 'aircraft', data: bar((r) => r.new), itemStyle: { color: t.adsb } },
      { name: 'Returning', type: 'bar', stack: 'aircraft', data: bar((r) => r.returning), itemStyle: { color: t.other } },
      {
        name: '% new',
        type: 'line',
        yAxisIndex: 1,
        showSymbol: rows.length <= DAILY_ZOOM_FROM,
        symbolSize: 4,
        connectNulls: false,
        data: rows.map(percentNew),
        itemStyle: { color: t.line },
        lineStyle: { width: 1.5 },
      },
      ...(hasLearning
        ? [{ name: 'Learning period', type: 'bar' as const, stack: 'aircraft', data: [], itemStyle: { color: t.adsb, decal: HATCH } }]
        : []),
    ],
  }
}
