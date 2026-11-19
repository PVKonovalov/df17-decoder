import { describe, expect, it } from 'vitest'
import type { CoverageGrid } from '../api/types'
import { cellAt, cellPolygon, countColor, destination, distanceNm, hasSignal, heatCells, heatLabel } from './heatmap'
import { signalColor } from './style'

const grid: CoverageGrid = {
  receiver: { lat: 32, lon: 35 },
  bucketDeg: 2,
  ringNm: 5,
  bands: [
    { name: 'low', cells: [[22, 20, 3, 3, -20, -25, -15], [0, 0, 4, 0, 0, 0, 0]] },
    { name: 'mid', cells: [[22, 20, 1, 1, -10, -10, -10]] },
    { name: 'high', cells: [] },
    { name: 'unknown', cells: [[0, 0, 2, 0, 0, 0, 0]] },
  ],
}

describe('heatCells', () => {
  it('keeps one band as is', () => {
    expect(heatCells(grid, 'mid')).toEqual([{ bucket: 22, ring: 20, n: 1, sn: 1, avg: -10, min: -10, max: -10 }])
    expect(heatCells(grid, 'high')).toEqual([])
    expect(heatCells(null, 'all')).toEqual([])
  })

  it('merges all bands with a power-weighted average', () => {
    const cells = heatCells(grid, 'all')
    const c = cells.find((x) => x.bucket === 22)
    expect(c?.n).toBe(4)
    expect(c?.sn).toBe(4)
    expect(c?.avg).toBeCloseTo(10 * Math.log10((3 * 0.01 + 0.1) / 4), 6)
    expect([c?.min, c?.max]).toEqual([-25, -10])
    expect(cells.find((x) => x.bucket === 0)).toEqual({ bucket: 0, ring: 0, n: 6, sn: 0 })
  })
})

describe('hasSignal', () => {
  it('is true only when some cell has a signal level', () => {
    expect(hasSignal(grid)).toBe(true)
    expect(hasSignal({ ...grid, bands: [{ name: 'low', cells: [[0, 0, 4, 0, 0, 0, 0]] }] })).toBe(false)
    expect(hasSignal(null)).toBe(false)
  })
})

describe('countColor', () => {
  it('runs red to green on a log scale', () => {
    expect(countColor(1, 1000)).toBe(signalColor(-36))
    expect(countColor(1000, 1000)).toBe(signalColor(-3))
    expect(countColor(5, 1)).toBe(signalColor(-3))
  })
})

describe('geometry', () => {
  it('round-trips destination and distance', () => {
    const [lat, lon] = destination(32, 35, 90, 100)
    expect(distanceNm(32, 35, lat, lon)).toBeCloseTo(100, 6)
    expect(destination(32, 35, 0, 60)[0]).toBeCloseTo(33, 1)
  })

  it('finds the cell under a point', () => {
    const [lat, lon] = destination(32, 35, 45, 102.5)
    expect(cellAt(grid, lat, lon)).toEqual({ bucket: 22, ring: 20 })
  })

  it('builds wedges, closing at the receiver for the first ring', () => {
    const wedge = cellPolygon(grid, { bucket: 22, ring: 20, n: 1, sn: 0 })
    expect(wedge).toHaveLength(6)
    for (const [i, d] of [[0, 105], [2, 105], [3, 100], [5, 100]] as const) {
      const p = wedge[i] ?? [0, 0]
      expect(distanceNm(32, 35, p[0], p[1])).toBeCloseTo(d, 6)
    }
    const inner = cellPolygon(grid, { bucket: 0, ring: 0, n: 1, sn: 0 })
    expect(inner.at(-1)).toEqual([32, 35])
  })
})

describe('heatLabel', () => {
  it('describes a cell with and without a signal level', () => {
    expect(heatLabel(grid, { bucket: 22, ring: 20, n: 1312, sn: 3, avg: -18.34, min: -24, max: -12.08 })).toBe(
      '044–046° · 100–105 nm · avg -18.3 dBFS (-24.0…-12.1) · 1,312 positions',
    )
    expect(heatLabel(grid, { bucket: 179, ring: 0, n: 1, sn: 0 })).toBe('358–000° · 0–5 nm · 1 position')
  })
})
