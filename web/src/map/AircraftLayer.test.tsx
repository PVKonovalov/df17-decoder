import { cleanup, render } from '@testing-library/react'
import { MapContainer } from 'react-leaflet'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Aircraft } from '../api/types'
import { AircraftLayer } from './AircraftLayer'
import { type ColorMode, GROUND_COLOR, markerColor } from './style'

afterEach(cleanup)

const ac = (icao: string, firstSeen: string, extra: Partial<Aircraft> = {}): Aircraft => ({
  icao,
  firstSeen,
  lastSeen: '2026-11-19T10:01:00Z',
  posTime: '2026-11-19T10:01:00Z',
  lat: 25.08,
  lon: 22.81,
  messages: 1,
  messagesByDf: {},
  hasPosition: true,
  adsb: true,
  ...extra,
})

const modeSOnly = (icao: string, firstSeen: string): Aircraft => ({
  icao,
  firstSeen,
  lastSeen: '2026-11-19T10:01:00Z',
  messages: 1,
  messagesByDf: {},
  hasPosition: false,
  adsb: false,
})

function renderLayer(list: Aircraft[], colorMode: ColorMode = 'altitude') {
  const aircraft = new Map(list.map((a) => [a.icao, a]))
  const dispatch = vi.fn()
  const layer = (mode: ColorMode) => (
    <MapContainer center={[25, 23]} zoom={8} style={{ width: 400, height: 300 }}>
      <AircraftLayer aircraft={aircraft} trails={new Map()} selected={null} dispatch={dispatch} colorMode={mode} />
    </MapContainer>
  )
  const r = render(layer(colorMode))
  const marker = (icao: string) => r.container.querySelector<HTMLElement>(`.ac[title="${icao}"]`)
  const setMode = (mode: ColorMode) => {
    r.rerender(layer(mode))
  }
  return { ...r, marker, setMode }
}

describe('AircraftLayer', () => {
  it('pulses markers of new aircraft only', () => {
    const { marker } = renderLayer([
      ac('738001', '2026-11-19T10:00:50Z'), // first seen 10 s before the newest data: new
      ac('738002', '2026-11-19T09:55:00Z'), // long known
      modeSOnly('738003', '2026-11-19T10:00:58Z'),
    ])
    expect(marker('738001')?.classList.contains('pulse')).toBe(true)
    expect(marker('738002')?.classList.contains('pulse')).toBe(false)
    expect(marker('738003')).toBeNull() // no position, no marker
  })

  it('labels markers with callsign and altitude', () => {
    const { marker } = renderLayer([ac('738001', '2026-11-19T09:55:00Z', { callsign: 'ELY1', altBaro: 4000 })])
    expect(marker('738001')?.querySelector('.ac-label')?.textContent).toBe('ELY1 040')
  })
})

const groundColor = document.createElement('i')
groundColor.style.color = GROUND_COLOR

describe('AircraftLayer on the ground', () => {
  it('labels taxiing aircraft GND and colors them with the ground color', () => {
    const { marker } = renderLayer([ac('738004', '2026-11-19T09:55:00Z', { callsign: 'ELY2', onGround: true, groundSpeed: 12 })])
    const m = marker('738004')
    expect(m?.querySelector('.ac-label')?.textContent).toBe('ELY2 GND')
    const plane = m?.querySelector<HTMLElement>('.ac-plane')
    expect(plane?.style.color).toBe(groundColor.style.color)
  })
})

describe('AircraftLayer icons', () => {
  it('shapes markers by category, and reshapes one when its category arrives', () => {
    const heli = ac('738010', '2026-11-19T09:55:00Z', { category: 'A7', track: 90 })
    const balloon = ac('738011', '2026-11-19T09:55:00Z', { category: 'B2', track: 90 })
    const unknown = ac('738012', '2026-11-19T09:55:00Z', { track: 90 })
    const aircraft = new Map([heli, balloon, unknown].map((a) => [a.icao, a]))
    const layer = (m: Map<string, Aircraft>) => (
      <MapContainer center={[25, 23]} zoom={8} style={{ width: 400, height: 300 }}>
        <AircraftLayer aircraft={m} trails={new Map()} selected={null} dispatch={vi.fn()} />
      </MapContainer>
    )
    const r = render(layer(aircraft))
    const plane = (icao: string) => r.container.querySelector<HTMLElement>(`.ac[title="${icao}"] .ac-plane`)
    const svg = (icao: string) => plane(icao)?.innerHTML ?? ''
    expect(svg('738010')).not.toBe(svg('738012'))
    expect(plane('738010')?.style.transform).toBe('rotate(90deg)')
    expect(plane('738011')?.style.transform).toBe('rotate(0deg)') // balloons don't turn
    const generic = svg('738012')

    const before = plane('738012')
    r.rerender(layer(new Map([...aircraft, ['738012', { ...unknown, category: 'A5' }]])))
    expect(plane('738012')).toBe(before) // same marker, new shape
    expect(svg('738012')).not.toBe(generic)
    expect(svg('738012')).toContain('width="30"')
  })
})

describe('AircraftLayer color mode', () => {
  it('recolors existing markers in place when switching to signal', () => {
    const strong = ac('738005', '2026-11-19T09:55:00Z', { altBaro: 30000, rssi: -4 })
    const noRssi = ac('738006', '2026-11-19T09:55:00Z', { altBaro: 30000 })
    const { marker, setMode } = renderLayer([strong, noRssi])
    const before = marker('738005')
    const color = (icao: string) => marker(icao)?.querySelector<HTMLElement>('.ac-plane')?.style.color
    const css = (c: string) => {
      const el = document.createElement('i')
      el.style.color = c
      return el.style.color
    }
    expect(color('738005')).toBe(css(markerColor(strong, 'altitude')))

    setMode('signal')
    expect(marker('738005')).toBe(before)
    expect(color('738005')).toBe(css(markerColor(strong, 'signal')))
    expect(color('738006')).toBe(css(markerColor(noRssi, 'signal')))
  })
})
