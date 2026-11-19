import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import type { Stats } from '../api/types'
import { initialState } from '../state/reducer'
import { Header } from './Header'

afterEach(cleanup)

const stats: Stats = {
  version: 'v1.0.0',
  uptimeSeconds: 5,
  source: { url: 'beast://pi:30005', connected: true, reconnects: 0, malformedLines: 0 },
  frames: 10,
  framesPerDf: { '17': 10 },
  messages: 10,
  messagesPerSecond: 2.5,
  crcFailures: 0,
  fixedBitErrors: 0,
  unsupported: 0,
  invalid: 0,
  addressParityDrops: 0,
  positions: 4,
  cprRejected: 0,
  aircraft: 1,
}

describe('Header', () => {
  it('shows the server version on hover of the title', () => {
    render(<Header state={{ ...initialState, stats }} />)
    expect(screen.getByRole('heading', { name: 'df17-decoder' }).getAttribute('title')).toBe('version v1.0.0')
    expect(screen.getByText('2.5')).toBeTruthy()
  })

  it('has no version before the first stats message', () => {
    render(<Header state={initialState} />)
    expect(screen.getByRole('heading', { name: 'df17-decoder' }).hasAttribute('title')).toBe(false)
  })
})
