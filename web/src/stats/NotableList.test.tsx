import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { NotableList } from './NotableList'

afterEach(() => {
  cleanup()
  window.history.replaceState(null, '', '/')
})

describe('NotableList', () => {
  it('lists events and opens the map in playback with the aircraft selected', () => {
    window.history.replaceState(null, '', '/stats?range=7d')
    render(
      <NotableList
        notable={{
          emergencies: 1,
          gnssLost: 1,
          firstSeen: 3,
          events: [
            { t: '2026-11-20T12:41:07Z', kind: 'emergency', icao: '4840D6', squawk: '7700', callsign: 'KLM1023', country: 'Netherlands', flightId: 42 },
            { t: '2026-11-20T11:30:00Z', kind: 'gnssLost', icao: '4B1805', callsign: 'SWR12', flightId: 7 },
            { t: '2026-11-20T11:00:00Z', kind: 'firstSeen', icao: '738ABC', flightId: 3 },
          ],
        }}
      />,
    )
    expect(screen.getByText(/1 emergency · 1 flight lost their position · 3 addresses/)).toBeTruthy()
    expect(screen.getByText('GNSS')).toBeTruthy()
    expect(screen.getByText('and 2 more; pick a shorter range to see them.')).toBeTruthy()
    expect(screen.getByText('NEW')).toBeTruthy()
    const link = screen.getByText('7700').closest('a')
    expect(link?.getAttribute('href')).toBe('/?t=2026-11-20T12%3A41%3A07Z&icao=4840D6')
    link?.click()
    expect(window.location.pathname + window.location.search).toBe('/?t=2026-11-20T12%3A41%3A07Z&icao=4840D6')
  })
})
