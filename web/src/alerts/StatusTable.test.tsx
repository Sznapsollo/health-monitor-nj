import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { useAlertStore } from '../store/useAlertStore'
import { StatusTable } from './StatusTable'

const entity = {
  platform: 'example',
  signal: 'servers',
  key: 'Jobs_1',
  lastSeen: '2026-10-01T16:15:00Z',
  offline: false,
}

describe('StatusTable', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    useAlertStore.setState({ entities: [], packets: {} })
  })

  it('opens the received packets on one sender', async () => {
    await i18n.changeLanguage('en')
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        headers: new Headers(),
        json: async () => ({ entries: [], seq: 0, keep: 500, rawBytes: 4096, windowSeconds: 30 }),
      })),
    )
    useAlertStore.setState({ entities: [entity], packets: {} })
    render(<StatusTable platform="example" />)

    fireEvent.click(screen.getByLabelText('Show packets from this sender'))
    expect(await screen.findByText('Sender: Jobs_1')).toBeTruthy()
  })

  it('offers no packets inside a dashboard panel', async () => {
    await i18n.changeLanguage('en')
    useAlertStore.setState({ entities: [entity], packets: {} })
    render(<StatusTable platform="example" compact />)

    expect(screen.getByText('Jobs_1')).toBeTruthy()
    expect(screen.queryByLabelText('Show packets from this sender')).toBeNull()
    expect(screen.queryByText('Show received packets')).toBeNull()
  })
})
