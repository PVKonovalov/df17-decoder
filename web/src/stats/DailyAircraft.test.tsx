import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import statsFixture from '../api/stats.fixture.json'
import type { DailyRow, DailyTotals } from '../api/stats'
import { DailyChart, DailyTiles } from './DailyAircraft'
import { readTheme } from './theme'

const fake = vi.hoisted(() => {
  const handlers: ((p: unknown) => void)[] = []
  return {
    handlers,
    init: vi.fn(() => ({
      on: (_: string, h: (p: unknown) => void) => handlers.push(h),
      setOption: vi.fn(),
      resize: vi.fn(),
      dispose: vi.fn(),
    })),
  }
})
vi.mock('./echarts', () => ({ initChart: fake.init }))

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  fake.handlers.length = 0
  window.history.replaceState(null, '', '/')
})

const row = (over: Partial<DailyRow>): DailyRow => ({ ...statsFixture.daily.days[0], ...over }) as DailyRow
const ok = (d: DailyTotals) => ({ status: 'ok' as const, data: d })

describe('DailyTiles', () => {
  it('shows today so far and yesterday, with — for no aircraft', () => {
    render(
      <DailyTiles
        daily={ok({ timezone: 'UTC', days: [row({ day: '2026-11-24', aircraft: 0, new: 0 }), row({ day: '2026-11-25', partial: true })] })}
      />,
    )
    const today = screen.getByLabelText('Today')
    expect(today.textContent).toContain('so far')
    expect(today.textContent).toContain('214')
    expect(today.textContent).toContain('18%')
    expect(today.textContent).toContain('ADS-B 171 · Mode-S 43')
    expect(screen.getByLabelText('Yesterday').textContent).toContain('—')
  })
})

describe('DailyChart', () => {
  it('opens the new aircraft of a clicked day, linking to their history', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify([statsFixture.newAircraft]))))
    vi.stubGlobal('fetch', fetchMock)
    const Probe = () => {
      const [day, setDay] = useState<string | null>(null)
      return <DailyChart daily={ok(statsFixture.daily)} theme={readTheme()} browserTz="Europe/Berlin" off="off" day={day} onDay={setDay} />
    }
    render(<Probe />)
    expect(screen.queryByText(/server's timezone/)).toBeNull() // same zone
    act(() => {
      fake.handlers[0]?.({ dataIndex: 0 })
    })
    expect(await screen.findByText('New on Wed 25 Nov')).toBeTruthy()
    expect(fetchMock).toHaveBeenCalledWith('/api/stats/new?day=2026-11-25&limit=200', expect.anything())
    const link = (await screen.findByText('AB12CD')).closest('a') as HTMLAnchorElement
    expect(link.getAttribute('href')).toBe('/?history=AB12CD')
    fireEvent.click(link)
    expect(window.location.search).toBe('?history=AB12CD')
  })

  it('names the server timezone when it differs and says when there is no data', () => {
    render(<DailyChart daily={ok({ ...statsFixture.daily, timezone: 'UTC' })} theme={readTheme()} browserTz="Europe/Berlin" off="off" day={null} onDay={vi.fn()} />)
    expect(screen.getByText(/Days are in the server's timezone, UTC/)).toBeTruthy()
    cleanup()
    render(<DailyChart daily={ok({ timezone: 'UTC', days: [] })} theme={readTheme()} browserTz="UTC" off="off" day={null} onDay={vi.fn()} />)
    expect(screen.getByText('No data yet: daily totals start after the first full hour.')).toBeTruthy()
  })
})
