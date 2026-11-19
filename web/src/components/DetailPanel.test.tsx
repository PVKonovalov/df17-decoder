import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import fixture from '../api/aircraft.fixture.json'
import type { Aircraft } from '../api/types'
import type { Action } from '../state/reducer'
import { DetailPanel, detailRows, trackerUrl } from './DetailPanel'

afterEach(cleanup)

const minimal: Aircraft = {
  icao: '738003',
  firstSeen: '2026-11-19T10:00:00Z',
  lastSeen: '2026-11-19T10:00:20Z',
  messages: 3,
  messagesByDf: { '4': 3 },
  hasPosition: false,
  adsb: false,
  altBaro: 0,
}

describe('DetailPanel', () => {
  it('lists only known fields and keeps a real 0 ft', () => {
    const labels = detailRows(minimal).map(([l]) => l)
    expect(labels).toContain('Altitude (baro)')
    expect(labels).not.toContain('Callsign')
    expect(labels).not.toContain('Position')
    expect(Object.fromEntries(detailRows(minimal))['Altitude (baro)']).toBe('0 ft')
    expect(Object.fromEntries(detailRows(minimal)).Type).toBe('Mode S only')
  })

  it('shows every fixture field group for a fully known aircraft', () => {
    const rows = Object.fromEntries(detailRows(fixture))
    expect(rows.Callsign).toBe('ELY001')
    expect(rows.Type).toBe('ADS-B v2')
    expect(rows.Emergency).toBe('general')
    expect(rows['Vertical rate']).toBe('↓ 640 ft/min')
    expect(rows.RSSI).toBe('-12.3 dBFS')
    expect(rows['Selected altitude']).toBe('4,992 ft (MCP)')
    expect(rows['Selected heading']).toBe('120.2°')
    expect(rows.Altimeter).toBe('1013.6 hPa')
    expect(rows.Autopilot).toBe('AP · VNAV · LNAV · TCAS')
    expect(rows.Mach).toBe('0.784')
    expect(rows.Roll).toBe('12.3° left')
    expect(Object.fromEntries(detailRows({ ...minimal, roll: 0 })).Roll).toBe('0.0°')
  })

  it('shows the autopilot row when modes are known but none is engaged', () => {
    const rows = Object.fromEntries(detailRows({ ...minimal, autopilotModes: [], selectedAlt: 35000, selectedAltSource: 'fms' }))
    expect(rows.Autopilot).toBe('none engaged')
    expect(rows['Selected altitude']).toBe('35,000 ft (FMS)')
    expect(Object.fromEntries(detailRows(minimal)).Autopilot).toBeUndefined()
  })

  it('links to the external tracker and closes', () => {
    const dispatch = vi.fn<(a: Action) => void>()
    render(<DetailPanel aircraft={fixture} dispatch={dispatch} />)
    expect(screen.getByRole('link').getAttribute('href')).toBe(trackerUrl('738ABC'))
    expect(trackerUrl('738ABC')).toBe('https://globe.adsbexchange.com/?icao=738abc')
    fireEvent.click(screen.getByRole('button', { name: 'Close details' }))
    expect(dispatch).toHaveBeenCalledWith({ type: 'clearSelection' })
  })
})
