import { cleanup, render } from '@testing-library/react'
import { MapContainer } from 'react-leaflet'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Aircraft } from '../api/types'
import { AircraftLayer } from './AircraftLayer'
import { GROUND_COLOR } from './style'

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

function renderLayer(list: Aircraft[]) {
  const aircraft = new Map(list.map((a) => [a.icao, a]))
  const r = render(
    <MapContainer center={[25, 23]} zoom={8} style={{ width: 400, height: 300 }}>
      <AircraftLayer aircraft={aircraft} trails={new Map()} selected={null} dispatch={vi.fn()} />
    </MapContainer>,
  )
  const marker = (icao: string) => r.container.querySelector<HTMLElement>(`.ac[title="${icao}"]`)
  return { ...r, marker }
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
