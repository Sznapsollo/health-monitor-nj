import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { PacketsDialog } from './PacketsDialog'

const page = {
  seq: 2,
  keep: 500,
  rawBytes: 4096,
  windowSeconds: 30,
  entries: [
    {
      seq: 1,
      at: '2026-10-01T16:15:00Z',
      from: '10.0.0.7',
      port: 8082,
      platform: 'example',
      sender: 'Jobs_1',
      type: 'gauge:jobQueuesLoad',
      size: 120,
      raw: '{"type":"jobQueuesLoad","serverName":"Jobs_1"}',
    },
    {
      seq: 2,
      at: '2026-10-01T16:15:01Z',
      from: '10.0.0.7',
      port: 8082,
      platform: 'example',
      type: 'customWarning',
      size: 90,
      raw: '{"type":"customWarning","message":"MessageProcessingData ERROR rows"}',
    },
  ],
}

describe('PacketsDialog', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('shows what arrives and narrows to packets without a sender', async () => {
    await i18n.changeLanguage('en')
    const fetch = vi.fn(async () => ({ ok: true, headers: new Headers(), json: async () => page }))
    vi.stubGlobal('fetch', fetch)
    render(<PacketsDialog open onClose={() => {}} />)

    expect(await screen.findByText('Jobs_1')).toBeTruthy()
    expect(fetch).toHaveBeenCalledWith('/api/packets?since=0', undefined)

    fireEvent.click(screen.getByLabelText('Without a sender only'))
    expect(screen.queryByText('Jobs_1')).toBeNull()
    fireEvent.click(screen.getByText('(none)'))
    expect(await screen.findByText(/MessageProcessingData ERROR rows/)).toBeTruthy()
  })

  it('opens on one sender and lets go of it', async () => {
    await i18n.changeLanguage('en')
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, headers: new Headers(), json: async () => page })),
    )
    render(<PacketsDialog open sender="Jobs_1" onClose={() => {}} />)

    expect(await screen.findByText('Jobs_1')).toBeTruthy()
    expect(screen.queryByText('(none)')).toBeNull()
    fireEvent.click(screen.getByTestId('CancelIcon'))
    expect(await screen.findByText('(none)')).toBeTruthy()
  })
})
