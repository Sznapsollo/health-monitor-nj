import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import i18n from '../i18n'
import { useServerStore } from '../store/useServerStore'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { ResourceBadge } from './ResourceBadge'

const base = {
  rssBytes: 83 * 1024 ** 2,
  heapBytes: 20 * 1024 ** 2,
  cpuPercent: 4,
  cpuPercent1m: 3.5,
  cores: 2,
  goroutines: 40,
  gcCycles: 12,
  started: '2026-09-23T10:00:00Z',
  uptimeSeconds: 7200,
}

function badge() {
  render(
    <ThemeModeProvider>
      <ResourceBadge />
    </ThemeModeProvider>,
  )
  return screen.getByText(/CPU/).closest('.MuiChip-root')
}

describe('ResourceBadge', () => {
  afterEach(() => useServerStore.setState({ resources: null }))

  it('shows memory and CPU in the header', async () => {
    await i18n.changeLanguage('en')
    useServerStore.setState({ resources: base })
    const chip = badge()
    expect(chip).toHaveTextContent('83 MB · CPU 3.5 %')
    expect(chip?.className).not.toMatch(/Warning/)
  })

  it('adds the database size when the server reports it', async () => {
    await i18n.changeLanguage('en')
    useServerStore.setState({ resources: { ...base, dbBytes: 250 * 1024 ** 2 } })
    expect(badge()).toHaveTextContent('83 MB · CPU 3.5 % · DB 250 MB')
  })

  it('adds the packets of the last minute', async () => {
    await i18n.changeLanguage('en')
    useServerStore.setState({ resources: { ...base, packetsLastMinute: 25560 } })
    expect(badge()).toHaveTextContent('83 MB · CPU 3.5 % · 25,560 pkt/min')
  })

  it('adds the log rows of the last minute', async () => {
    await i18n.changeLanguage('en')
    useServerStore.setState({
      resources: { ...base, packetsLastMinute: 25560, logsLastMinute: 1204 },
    })
    expect(badge()).toHaveTextContent('83 MB · CPU 3.5 % · 25,560 pkt/min · 1,204 logs/min')
  })

  it('turns red and says so when a disk is almost full', async () => {
    await i18n.changeLanguage('en')
    useServerStore.setState({
      resources: {
        ...base,
        disks: [
          {
            folders: ['archive', 'database'],
            freeBytes: 8 * 1024 ** 3,
            totalBytes: 600 * 1024 ** 3,
            low: true,
          },
        ],
      },
    })
    const chip = badge()
    expect(chip).toHaveTextContent('disk 8.0 GB free!')
    expect(chip?.className).toMatch(/Error/)
  })

  it('turns to a warning near the container limit', async () => {
    await i18n.changeLanguage('en')
    useServerStore.setState({ resources: { ...base, memLimitBytes: 90 * 1024 ** 2 } })
    expect(badge()?.className).toMatch(/Warning/)
  })

  it('shows nothing before the first reading', () => {
    const { container } = render(
      <ThemeModeProvider>
        <ResourceBadge />
      </ThemeModeProvider>,
    )
    expect(container).toBeEmptyDOMElement()
  })
})
