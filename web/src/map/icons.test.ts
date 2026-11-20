import { describe, expect, it } from 'vitest'
import { categoryText, ICON_ORDER, ICONS, iconFor, iconRotation, iconSvg } from './icons'

describe('aircraft icons', () => {
  it('picks the icon of the emitter category', () => {
    const got = ['A1', 'A2', 'A3', 'A4', 'A5', 'A6', 'A7', 'B1', 'B2', 'B4', 'B6', 'C1', 'C2'].map(iconFor)
    expect(got).toEqual(['light', 'small', 'large', 'large', 'heavy', 'fast', 'rotor', 'glider', 'balloon', 'light', 'drone', 'vehicle', 'vehicle'])
    for (const c of [undefined, 'A0', 'B0', 'B3', 'B7', 'C3', 'D1', 'bogus']) {
      expect(iconFor(c)).toBe('generic')
    }
    expect([...ICON_ORDER].sort()).toEqual(Object.keys(ICONS).sort())
  })

  it('draws every icon at its size and scale, turning only those that turn', () => {
    expect(iconSvg('heavy')).toContain('width="30"')
    expect(iconSvg('large', 0.75)).toContain('height="18"')
    expect(iconRotation('large', 135)).toBe(135)
    expect(iconRotation('balloon', 135)).toBe(0)
    for (const k of ICON_ORDER) {
      expect(iconSvg(k)).toMatch(/^<svg viewBox="0 0 24 24".*<\/svg>$/)
    }
  })

  it('names the category', () => {
    expect([categoryText('A3'), categoryText('B6'), categoryText('D2'), categoryText(undefined)]).toEqual([
      'A3 (large)',
      'B6 (UAV)',
      'D2',
      undefined,
    ])
  })
})
