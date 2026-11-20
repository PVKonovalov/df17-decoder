import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Stats } from '../api/types'
import { initialState } from '../state/reducer'
import { Header } from './Header'

afterEach(cleanup)

const stats: Stats = {
  version: 'v1.0.0',
  serverTime: '2026-11-20T10:00:00Z',
  uptimeSeconds: 5,
  source: { url: 'beast://pi:30005', connected: true, reconnects: 0, malformedLines: 0 },
  frames: 10,
  framesPerDf: { '17': 10 },
  messages: 10,
  messagesPerSecond: 2.5,
  crcFailures: 0,
  fixedBitErrors: 0,
  unsupported: 0,
  invalid: 0,
  addressParityDrops: 0,
  positions: 4,
  cprRejected: 0,
  commBAmbiguous: 0,
  commBRejected: 0,
  aircraft: 1,
}

describe('Header', () => {
  it('shows the server version next to the title', () => {
    render(<Header state={{ ...initialState, stats }} />)
    expect(screen.getByRole('heading', { name: 'df17-decoder' })).toBeTruthy()
    expect(screen.getByTitle('Server version').textContent).toBe('v1.0.0')
    expect(screen.getByText('2.5')).toBeTruthy()
  })

  it('has no version before the first stats message', () => {
    render(<Header state={initialState} />)
    expect(screen.queryByTitle('Server version')).toBeNull()
  })
})

describe('Header panel switches', () => {
  it('shows and toggles the table and legend', () => {
    const onTable = vi.fn()
    const onLegend = vi.fn()
    render(<Header state={initialState} panels={{ table: true, onTable, legend: false, onLegend }} />)
    const table = screen.getByRole('button', { name: 'Table' })
    const legend = screen.getByRole('button', { name: 'Legend' })
    expect([table.getAttribute('aria-pressed'), legend.getAttribute('aria-pressed')]).toEqual(['true', 'false'])
    fireEvent.click(table)
    fireEvent.click(legend)
    expect(onTable).toHaveBeenCalledWith(false)
    expect(onLegend).toHaveBeenCalledWith(true)
  })

  it('has no switches without panels', () => {
    render(<Header state={initialState} />)
    expect(screen.queryByRole('group', { name: 'Show panels' })).toBeNull()
  })
})
