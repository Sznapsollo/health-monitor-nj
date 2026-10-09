import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { Dashboard } from '../api/dashboards'
import i18n from '../i18n'
import { CopyToPlatformButton } from './CopyToPlatform'
import { copyToPlatform } from './transfer'

const ops: Dashboard = {
  id: 'ops',
  platform: 'example',
  name: 'Ops',
  default: true,
  readOnly: true,
  createdBy: 'anna',
  rows: [{ columns: [{ panels: [{ type: 'chart', signal: 'requests' }] }] }],
}

function stubServer(taken: string[]) {
  const saved: { url: string; body: Dashboard }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as Dashboard
        saved.push({ url: String(url), body })
        return { ok: true, json: async () => body }
      }
      return { ok: true, json: async () => ({ dashboards: taken.map((id) => ({ id })) }) }
    }),
  )
  return saved
}

describe('copying a dashboard to another platform', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
  })

  it('saves the arrangement there under a free id, without its origin', async () => {
    const saved = stubServer(['ops', 'overview'])
    await copyToPlatform(ops, 'scaneiro')
    expect(saved).toHaveLength(1)
    expect(saved[0]?.url).toContain('/api/dashboards/ops-2?platform=scaneiro')
    expect(saved[0]?.body).toEqual({
      id: 'ops-2',
      platform: 'scaneiro',
      name: 'Ops',
      default: true,
      rows: ops.rows,
    })
  })

  it('offers only the other platforms', async () => {
    stubServer([])
    const onCopied = vi.fn()
    render(
      <CopyToPlatformButton
        dashboard={ops}
        platforms={['example', 'scaneiro']}
        onCopied={onCopied}
        onError={() => {}}
      />,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Copy to platform…' }))
    expect(screen.queryByRole('menuitem', { name: 'example' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('menuitem', { name: 'scaneiro' }))
    await vi.waitFor(() => expect(onCopied).toHaveBeenCalledWith('scaneiro'))
  })

  it('is not shown with nowhere to copy to', () => {
    render(
      <CopyToPlatformButton
        dashboard={ops}
        platforms={['example']}
        onCopied={() => {}}
        onError={() => {}}
      />,
    )
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })
})
