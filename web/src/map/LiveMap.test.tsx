import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MapLegend } from './LiveMap'

afterEach(cleanup)

describe('MapLegend', () => {
  it('shows the altitude scale without a switch when no aircraft has a signal level', () => {
    render(<MapLegend mode="altitude" />)
    expect(screen.getByLabelText('Altitude color scale').textContent).toContain('Altitude, ft')
    expect(screen.queryByRole('group', { name: 'Color markers by' })).toBeNull()
  })

  it('switches between altitude and signal', () => {
    const onMode = vi.fn()
    const { rerender } = render(<MapLegend mode="altitude" onMode={onMode} />)
    const signal = screen.getByRole('button', { name: 'Signal' })
    expect(signal.getAttribute('aria-pressed')).toBe('false')
    fireEvent.click(signal)
    expect(onMode).toHaveBeenCalledWith('signal')

    rerender(<MapLegend mode="signal" onMode={onMode} />)
    expect(screen.getByLabelText('Signal color scale').textContent).toContain('Signal, dBFS')
    expect(screen.getByRole('button', { name: 'Signal' }).getAttribute('aria-pressed')).toBe('true')
    expect(screen.getByText(/-36/)).toBeTruthy()
  })
})

describe('MapLegend coverage view', () => {
  it('switches the coverage view', () => {
    const onCoverage = vi.fn()
    render(<MapLegend mode="altitude" coverage="off" onCoverage={onCoverage} />)
    expect(screen.getByRole('button', { name: 'Off' }).getAttribute('aria-pressed')).toBe('true')
    fireEvent.click(screen.getByRole('button', { name: 'Outline' }))
    expect(onCoverage).toHaveBeenCalledWith('outline')
    fireEvent.click(screen.getByRole('button', { name: 'Count' }))
    expect(onCoverage).toHaveBeenCalledWith('count')
  })

  it('offers the signal heatmap only with signal levels', () => {
    const group = () => screen.getByRole('group', { name: 'Coverage view' })
    const { rerender } = render(<MapLegend mode="altitude" onCoverage={vi.fn()} />)
    expect(group().textContent).not.toContain('Signal')
    rerender(<MapLegend mode="altitude" onCoverage={vi.fn()} heatSignal />)
    expect(group().textContent).toContain('Signal')
  })

  it('shows the band selector, scale and caveat for a heatmap', () => {
    const onBand = vi.fn()
    const { rerender } = render(<MapLegend mode="altitude" coverage="outline" onCoverage={vi.fn()} onBand={onBand} />)
    expect(screen.queryByRole('combobox', { name: 'Heatmap altitude band' })).toBeNull()

    rerender(<MapLegend mode="altitude" coverage="signal" heatSignal onCoverage={vi.fn()} band="all" onBand={onBand} />)
    expect(screen.getByText(/edge cells look stronger/)).toBeTruthy()
    fireEvent.change(screen.getByRole('combobox', { name: 'Heatmap altitude band' }), { target: { value: 'high' } })
    expect(onBand).toHaveBeenCalledWith('high')

    rerender(<MapLegend mode="altitude" coverage="count" onCoverage={vi.fn()} band="high" onBand={onBand} />)
    expect(screen.getByText(/log scale/)).toBeTruthy()
    expect(screen.getByText('often')).toBeTruthy()
  })
})
