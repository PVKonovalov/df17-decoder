import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import type { Aircraft } from '../api/types'
import type { Action } from '../state/reducer'
import { AircraftTable } from './AircraftTable'

const ac = (icao: string, extra: Partial<Aircraft> = {}): Aircraft => ({
  icao,
  firstSeen: '2026-11-19T09:59:00Z',
  lastSeen: '2026-11-19T10:00:20Z',
  messages: 5,
  messagesByDf: {},
  hasPosition: extra.lat !== undefined,
  adsb: extra.lat !== undefined,
  ...extra,
})

const fleet = new Map(
  [
    ac('738001', { callsign: 'ELY001', lat: 25, lon: 23, distanceNm: 12.3, altBaro: 0, verticalRate: -640 }),
    ac('738002', { callsign: 'ISR22', lat: 25.1, lon: 23, distanceNm: 4.5, altBaro: 3500 }),
    ac('738003', { squawk: '1200', altBaro: 2500 }),
    ac('4CA001', { squawk: '7700', lat: 24, lon: 22, distanceNm: 80 }),
  ].map((a) => [a.icao, a]),
)

const rowIcaos = () =>
  screen
    .getAllByRole('row')
    .slice(1)
    .map((r) => r.getAttribute('data-icao'))

const scrollIntoView = vi.fn()
beforeAll(() => {
  Element.prototype.scrollIntoView = scrollIntoView
})
afterEach(cleanup)

function setup(selected: string | null = null) {
  const dispatch = vi.fn<(a: Action) => void>()
  render(<AircraftTable aircraft={fleet} selected={selected} dispatch={dispatch} />)
  return dispatch
}

describe('AircraftTable', () => {
  it('lists all aircraft by distance with the emergency pinned and Mode-S-only rows marked', () => {
    setup()
    expect(rowIcaos()).toEqual(['4CA001', '738002', '738001', '738003'])
    const emergency = screen.getAllByRole('row')[1]
    expect(emergency?.className).toContain('emergency')
    const modeS = screen.getAllByRole('row')[4] as HTMLElement
    expect(modeS.className).toContain('nopos')
    expect(within(modeS).getByText('no pos')).toBeTruthy()
  })

  it('shows a real 0 ft altitude and formats vertical rate', () => {
    setup()
    const row = screen.getAllByRole('row').find((r) => r.getAttribute('data-icao') === '738001') as HTMLElement
    expect(within(row).getByText('0')).toBeTruthy()
    expect(within(row).getByText('↓ 640')).toBeTruthy()
  })

  it('sorts by a clicked header, toggling direction, emergency still first', () => {
    setup()
    const alt = screen.getByRole('columnheader', { name: /Alt ft/ })
    fireEvent.click(alt)
    expect(alt.getAttribute('aria-sort')).toBe('ascending')
    expect(rowIcaos()).toEqual(['4CA001', '738001', '738003', '738002'])
    fireEvent.click(alt)
    expect(alt.getAttribute('aria-sort')).toBe('descending')
    expect(rowIcaos()).toEqual(['4CA001', '738002', '738003', '738001'])
  })

  it('filters by text and by position', () => {
    setup()
    fireEvent.change(screen.getByRole('searchbox', { name: 'Filter' }), { target: { value: 'isr' } })
    expect(rowIcaos()).toEqual(['738002'])
    fireEvent.change(screen.getByRole('searchbox', { name: 'Filter' }), { target: { value: '' } })
    fireEvent.click(screen.getByRole('checkbox', { name: 'with position' }))
    expect(rowIcaos()).not.toContain('738003')
  })

  it('selects on click, panning only aircraft with a position', () => {
    const dispatch = setup()
    const rows = screen.getAllByRole('row')
    fireEvent.click(rows[2] as HTMLElement)
    fireEvent.click(rows[4] as HTMLElement)
    expect(dispatch.mock.calls).toEqual([
      [{ type: 'select', icao: '738002', pan: true }],
      [{ type: 'select', icao: '738003', pan: false }],
    ])
  })

  it('highlights new aircraft with the fade offset by their age, not older ones', () => {
    const withNew = new Map(fleet)
    withNew.set('738099', ac('738099', { firstSeen: '2026-11-19T10:00:16Z', lastSeen: '2026-11-19T10:00:20Z' }))
    render(<AircraftTable aircraft={withNew} selected={null} dispatch={vi.fn()} />)
    const rows = screen.getAllByRole('row')
    const fresh = rows.find((r) => r.getAttribute('data-icao') === '738099') as HTMLElement
    expect(fresh.className).toContain('new')
    expect(fresh.style.animationDelay).toMatch(/^-4\d{3}ms$/) // ~4 s old: fade continues from there
    const old = rows.find((r) => r.getAttribute('data-icao') === '738002') as HTMLElement
    expect(old.className).not.toContain('new')
    expect(old.style.animationDelay).toBe('')
  })

  it('highlights and scrolls to the selected row, hides RSSI without data', () => {
    setup('738003')
    const row = screen.getAllByRole('row').find((r) => r.getAttribute('data-icao') === '738003')
    expect(row?.className).toContain('selected')
    expect(scrollIntoView).toHaveBeenCalled()
    expect(screen.queryByRole('columnheader', { name: /RSSI/ })).toBeNull()
  })
})
