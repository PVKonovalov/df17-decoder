import { describe, expect, it } from 'vitest'
import statsFixture from '../api/stats.fixture.json'
import type { DailyRow } from '../api/stats'
import { readTheme } from './theme'
import { DAILY_ZOOM_FROM, dailyOption, dailyTooltip, dayLabel, HATCH, percentNew, todayAndYesterday, zoneNote } from './daily'

const row = (over: Partial<DailyRow>): DailyRow => ({ ...statsFixture.daily.days[0], ...over }) as DailyRow

describe('daily helpers', () => {
  it('rounds the new share and has none without aircraft', () => {
    expect(percentNew({ aircraft: 214, new: 38 })).toBe(18)
    expect(percentNew({ aircraft: 3, new: 2 })).toBe(67)
    expect(percentNew({ aircraft: 0, new: 0 })).toBeNull()
  })

  it('labels the server day as is and notes another timezone', () => {
    expect(dayLabel('2026-11-25')).toBe('Wed 25 Nov')
    expect(zoneNote('Europe/Berlin', 'Europe/Berlin')).toBeNull()
    expect(zoneNote('UTC', 'Europe/Berlin')).toBe("Days are in the server's timezone, UTC.")
  })

  it('picks today (partial) and the day before', () => {
    const a = row({ day: '2026-11-24' })
    const b = row({ day: '2026-11-25', partial: true })
    expect(todayAndYesterday([a, b])).toEqual({ today: b, yesterday: a })
    expect(todayAndYesterday([b])).toEqual({ today: b, yesterday: undefined })
    expect(todayAndYesterday([a])).toEqual({})
  })

  it('lists every field in the tooltip', () => {
    const html = dailyTooltip(row({ learning: true, approximate: true, partial: true }), 'Days are in UTC.')
    for (const s of ['(so far)', 'Aircraft: 214 (ADS-B 171, Mode-S only 43)', 'New: 38 (ADS-B 31, Mode-S only 7)', 'Returning: 176', '18%', 'Learning period', 'Approximate', 'Days are in UTC.']) {
      expect(html).toContain(s)
    }
  })
})

describe('dailyOption', () => {
  it('stacks new and returning, hatches learning days, lightens today and adds % new', () => {
    const days = [row({ day: '2026-11-24', learning: true }), row({ day: '2026-11-25', partial: true })]
    const o = dailyOption({ timezone: 'UTC', days }, readTheme(), 'UTC')
    const series = o.series as { name: string; stack?: string; data: unknown[] }[]
    expect(series.map((s) => s.name)).toEqual(['New', 'Returning', '% new', 'Learning period'])
    const bars = series[0]?.data as { value: number; itemStyle: { decal?: unknown; opacity?: number } }[]
    expect(bars.map((b) => b.value)).toEqual([38, 38])
    expect(bars[0]?.itemStyle.decal).toBe(HATCH)
    expect(bars[1]?.itemStyle).toEqual({ opacity: 0.4 })
    expect(series[2]?.data).toEqual([18, 18])
    expect((o.xAxis as { data: string[] }).data[1]).toBe('Wed 25 Nov (so far)')
    expect(o.dataZoom).toEqual([])
  })

  it('zooms long ranges and drops the learning legend when there is none', () => {
    const days = Array.from({ length: DAILY_ZOOM_FROM + 1 }, (_, i) => row({ day: `2026-01-${String((i % 28) + 1).padStart(2, '0')}` }))
    const o = dailyOption({ timezone: 'UTC', days }, readTheme(), 'UTC')
    expect((o.dataZoom as unknown[]).length).toBe(2)
    expect((o.series as { name: string }[]).map((s) => s.name)).not.toContain('Learning period')
  })
})
