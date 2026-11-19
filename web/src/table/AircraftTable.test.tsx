import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import type { Aircraft } from '../api/types'
import type { Action } from '../state/reducer'
import { AircraftTable } from './AircraftTable'

const ac = (icao: string, extra: Partial<Aircraft> = {}): Aircraft => ({
  icao,
  firstSeen: '2026-11-19T10:00:00Z',
  lastSeen: '2026-11-19T10:00:20Z',
  messages: 5,
  messagesByDf: {},
  hasPosition: extra.lat !== undefined,
  adsb: extra.lat !== undefined,
  ...extra,
})

const fleet = new Map(
  [
    ac('738001', { callsign: 'ELY001', lat: 32, lon: 35, distanceNm: 12.3, altBaro: 0, verticalRate: -640 }),
    ac('738002', { callsign: 'ISR22', lat: 32.1, lon: 35, distanceNm: 4.5, altBaro: 3500 }),
    ac('738003', { squawk: '1200', altBaro: 2500 }),
    ac('4CA001', { squawk: '7700', lat: 31, lon: 34, distanceNm: 80 }),
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

  it('highlights and scrolls to the selected row, hides RSSI without data', () => {
    setup('738003')
    const row = screen.getAllByRole('row').find((r) => r.getAttribute('data-icao') === '738003')
    expect(row?.className).toContain('selected')
    expect(scrollIntoView).toHaveBeenCalled()
    expect(screen.queryByRole('columnheader', { name: /RSSI/ })).toBeNull()
  })
})
