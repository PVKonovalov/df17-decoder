import { describe, expect, it } from 'vitest'
import type { Waypoint } from '../api/types'
import { WAYPOINT_ICONS, waypointIcon, waypointSvg } from './icons'
import iconFixture from './waypointIcons.fixture.json'
import { escapeHtml, labelsVisible, nearest, waypointColor, waypointHtml, waypointTooltip } from './waypoints'

// fictional points near the placeholder receiver
const alpha: Waypoint = {
  name: 'ALPHA',
  lat: 25.07,
  lon: 22.871666666666666,
  latDms: '250412.00N',
  lonDms: '0225218.00E',
  icon: 'star',
}
const bravo: Waypoint = {
  name: 'BR<A>VO',
  lat: 25.57,
  lon: 22.87,
  latDms: '253412.00N',
  lonDms: '0225212.00E',
  icon: 'triangleOpen',
  color: '#f80',
  note: 'fictional & "quoted"',
}

describe('waypoint icons', () => {
  it('match the Go list (config.WaypointIcons, via the shared fixture)', () => {
    expect([...WAYPOINT_ICONS]).toEqual(iconFixture)
    expect(WAYPOINT_ICONS.length).toBeGreaterThanOrEqual(5)
    expect(WAYPOINT_ICONS.length).toBeLessThanOrEqual(10)
  })

  it('picks the configured icon, else the triangle', () => {
    expect(waypointIcon('triangleOpen')).toBe('triangleOpen')
    expect(waypointIcon('hexagon')).toBe('hexagon')
    expect(waypointIcon('pentagon')).toBe('triangle')
    expect(waypointIcon('')).toBe('triangle')
  })

  it('draws filled symbols with wp-fill and open ones with wp-line', () => {
    expect(waypointSvg('triangle')).toContain('class="wp-fill"')
    expect(waypointSvg('triangleOpen')).toContain('class="wp-line"')
    expect(waypointSvg('cross')).toContain('class="wp-line"')
    for (const i of WAYPOINT_ICONS) {
      expect(waypointSvg(i)).toMatch(/^<svg class="wp-svg" viewBox="0 0 12 12" width="12" height="12".*<\/svg>$/)
    }
  })
})

describe('waypoint markers', () => {
  it('uses the configured color, else the theme token', () => {
    expect(waypointColor(bravo)).toBe('#f80')
    expect(waypointColor(alpha)).toBeUndefined()
    expect(waypointColor({ ...alpha, color: 'red;x' })).toBeUndefined()
    expect(waypointHtml(bravo)).toContain('style="--wp-color:#f80"')
    expect(waypointHtml(alpha)).not.toContain('style=')
  })

  it('labels the symbol with the escaped name', () => {
    expect(waypointHtml(alpha)).toContain('<span class="wp-label">ALPHA</span>')
    expect(waypointHtml(bravo)).toContain('BR&#60;A&#62;VO')
    expect(escapeHtml(`<&>"'`)).toBe('&#60;&#38;&#62;&#34;&#39;')
  })

  it('shows labels from the label zoom up', () => {
    expect(labelsVisible(8, 9)).toBe(false)
    expect(labelsVisible(9, 9)).toBe(true)
    expect(labelsVisible(12, 9)).toBe(true)
  })
})

describe('waypoint tooltip', () => {
  it('shows the name, DMS, decimal degrees, distance and bearing, and the note', () => {
    expect(waypointTooltip(bravo, { lat: 25.07, lon: 22.87, maxRangeNm: 300 })).toBe(
      '<b>BR&#60;A&#62;VO</b><br>253412.00N 0225212.00E<br>25.57000, 22.87000<br>30.0 nm at 000° from the receiver' +
        '<br>fictional &#38; &#34;quoted&#34;',
    )
  })

  it('leaves out the distance without a receiver and the note when empty', () => {
    expect(waypointTooltip(alpha, null)).toBe('<b>ALPHA</b><br>250412.00N 0225218.00E<br>25.07000, 22.87167')
  })

  it('gives the bearing from the receiver', () => {
    const east = { ...alpha, lat: 25.07, lon: 23.4 }
    expect(waypointTooltip(east, { lat: 25.07, lon: 22.87, maxRangeNm: 300 })).toContain('nm at 090° from the receiver')
  })
})

describe('nearest', () => {
  it('finds the closest point within the radius', () => {
    const pts = [
      { x: 0, y: 0 },
      { x: 10, y: 0 },
      { x: 4, y: 3 },
    ]
    expect(nearest(pts, 5, 2)).toBe(2)
    expect(nearest(pts, 11, 1)).toBe(1)
    expect(nearest(pts, 50, 50)).toBe(-1)
    expect(nearest([], 0, 0)).toBe(-1)
  })
})
