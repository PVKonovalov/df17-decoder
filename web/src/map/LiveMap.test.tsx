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
