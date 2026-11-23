import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import fixture from '../api/aircraft.fixture.json'
import type { Aircraft } from '../api/types'
import type { Action } from '../state/reducer'
import { copyText, DetailPanel, detailRows, trackerUrl } from './DetailPanel'

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
    expect(labels).not.toContain('Position quality')
    expect(Object.fromEntries(detailRows(minimal))['Altitude (baro)']).toBe('0 ft')
    expect(Object.fromEntries(detailRows(minimal)).Transponder).toBe('Mode S only')
  })

  it('shows every fixture field group for a fully known aircraft', () => {
    const rows = Object.fromEntries(detailRows(fixture))
    expect(rows.Callsign).toBe('ELY001')
    expect(rows.Transponder).toBe('ADS-B v2')
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

  it('marks a lost position', () => {
    render(<DetailPanel aircraft={{ ...fixture, positionQuality: 'lost', nic: 0, nacp: 0 }} dispatch={vi.fn()} />)
    const quality = screen.getByText('Lost (no position from the aircraft)')
    expect(quality.className).toBe('quality lost')
    expect(quality.parentElement?.textContent).toBe('Lost (no position from the aircraft) · NIC 0 · NACp 0')
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

describe('reference data', () => {
  const info = { icao: '738ABC', registration: '4X-EKA', typeCode: 'B738', typeName: 'Boeing 737-800', updated: '2026-11-22T10:00:00Z' }
  const route = { callsign: 'ELY001', origin: 'LLBG', destination: 'EGLL', updated: '2026-11-22T10:00:00Z' }

  it('adds the registration, type and route rows when known', () => {
    const rows = Object.fromEntries(detailRows(fixture, { aircraft: info, route }))
    expect(rows.Registration).toBe('4X-EKA')
    expect(rows['Aircraft type']).toBe('B738 · Boeing 737-800')
    expect(rows.Route).toBe('LLBG → EGLL')
    expect(Object.fromEntries(detailRows(fixture, { route: { callsign: 'ELY001', destination: 'EGLL', updated: '' } })).Route).toBe('? → EGLL')
    const none = detailRows(fixture, { aircraft: null, route: null }).map(([l]) => l)
    expect(none).not.toContain('Registration')
    expect(none).not.toContain('Route')
  })

  it('loads the entries, and saves edits by ICAO and callsign', async () => {
    const calls: [string, RequestInit | undefined][] = []
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string, init?: RequestInit) => {
        calls.push([url, init])
        if (init?.method === 'PUT') {
          const body = JSON.parse(init.body as string) as Record<string, string>
          const key = url.includes('/routes/') ? { callsign: 'ELY001' } : { icao: '738ABC' }
          return Promise.resolve(new Response(JSON.stringify({ ...key, ...body, updated: '' }), { status: 200 }))
        }
        if (url.includes('/aircraft/')) {
          return Promise.resolve(new Response(JSON.stringify(info), { status: 200 }))
        }
        return Promise.resolve(new Response('{"error":"no reference entry"}', { status: 404 }))
      }),
    )
    const dispatch = vi.fn<(a: Action) => void>()
    const { rerender } = render(<DetailPanel aircraft={fixture} dispatch={dispatch} reference />)
    await waitFor(() => {
      expect(screen.getByText('B738 · Boeing 737-800')).toBeTruthy()
    })
    expect(calls.map(([u]) => u).sort()).toEqual(['/api/reference/aircraft/738ABC', '/api/reference/routes/ELY001'])
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }))
    expect(dispatch).toHaveBeenLastCalledWith({ type: 'editing', icao: '738ABC' })
    rerender(<DetailPanel aircraft={fixture} dispatch={dispatch} reference editing />)
    expect(screen.getByLabelText<HTMLInputElement>('Registration').value).toBe('4X-EKA')
    fireEvent.change(screen.getByLabelText('From'), { target: { value: 'LLBG' } })
    fireEvent.change(screen.getByLabelText('To'), { target: { value: 'EGLL' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => {
      expect(screen.getByText('LLBG → EGLL')).toBeTruthy()
    })
    const put = calls.find(([u, i]) => i?.method === 'PUT' && u.includes('/routes/'))
    expect(JSON.parse(put?.[1]?.body as string)).toEqual({ origin: 'LLBG', destination: 'EGLL' })
    expect(dispatch).toHaveBeenLastCalledWith({ type: 'editing', icao: null })
    vi.unstubAllGlobals()
  })

  it('says when the aircraft is no longer received', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => undefined)))
    render(<DetailPanel aircraft={fixture} dispatch={vi.fn()} reference editing gone />)
    expect(screen.getByText(/^No longer received · last seen /)).toBeTruthy()
    expect(screen.getByRole('form', { name: 'Edit reference data' })).toBeTruthy()
    vi.unstubAllGlobals()
  })

  it('has no Edit button and fetches nothing without history', () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    render(<DetailPanel aircraft={fixture} dispatch={vi.fn()} />)
    expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull()
    expect(fetchMock).not.toHaveBeenCalled()
    vi.unstubAllGlobals()
  })
})

describe('Copy link', () => {
  it('copies the page URL with the Clipboard API and confirms', async () => {
    window.history.replaceState(null, '', '/?icao=738003')
    const writeText = vi.fn(() => Promise.resolve())
    vi.stubGlobal('isSecureContext', true)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    render(<DetailPanel aircraft={minimal} dispatch={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: 'Copy link' }))
    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Copied' })).toBeTruthy()
    })
    expect(writeText).toHaveBeenCalledWith(`${window.location.origin}/?icao=738003`)
    Reflect.deleteProperty(navigator, 'clipboard')
    vi.unstubAllGlobals()
    window.history.replaceState(null, '', '/')
  })

  it('falls back to execCommand outside a secure context', async () => {
    vi.stubGlobal('isSecureContext', false)
    const exec = vi.fn(() => true)
    Object.defineProperty(document, 'execCommand', { value: exec, configurable: true })
    await expect(copyText('http://pi.local:8080/?icao=738003')).resolves.toBe(true)
    expect(exec).toHaveBeenCalledWith('copy')
    expect(document.querySelector('textarea')).toBeNull()
    exec.mockReturnValue(false)
    await expect(copyText('x')).resolves.toBe(false)
    Reflect.deleteProperty(document, 'execCommand')
    vi.unstubAllGlobals()
  })
})
