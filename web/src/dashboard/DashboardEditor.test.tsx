import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { slugOf, type Dashboard } from '../api/dashboards'
import i18n from '../i18n'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { DashboardEditor } from './DashboardEditor'

const specs = [
  {
    name: 'requests',
    kind: 'timeseries',
    displayName: 'Requests',
    dims: [{ name: 'url', displayName: 'URL' }],
    retention: { hotDetailMinutes: 120, hotTotalsMinutes: 2880, durableDays: 30 },
  },
]

const board: Dashboard = {
  id: 'mine',
  platform: 'example',
  name: 'Mine',
  rows: [
    {
      columns: [
        {
          width: 1,
          panels: [{ type: 'chart', signal: 'requests', group: 'url' }, { type: 'status' }],
        },
      ],
    },
  ],
}

describe('slugOf', () => {
  it('makes a file-safe, unique id from a name', () => {
    expect(slugOf('Anna: ops (copy)', [])).toBe('anna-ops-copy')
    expect(slugOf('Ops', ['ops', 'ops-2'])).toBe('ops-3')
    expect(slugOf('???', [])).toBe('dashboard')
  })
})

describe('DashboardEditor', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('reorders panels and saves the arrangement as one PUT', async () => {
    await i18n.changeLanguage('en')
    const fetch = vi.fn(async (_url: string, init?: RequestInit) => ({
      ok: true,
      json: async () => JSON.parse(String(init?.body)) as Dashboard,
    }))
    vi.stubGlobal('fetch', fetch)
    const onSaved = vi.fn()

    render(
      <ThemeModeProvider>
        <DashboardEditor
          dashboard={board}
          platform="example"
          specs={specs as never}
          onSaved={onSaved}
          onCancel={() => {}}
        />
      </ThemeModeProvider>,
    )

    fireEvent.click(screen.getAllByLabelText('Move down')[0]!)
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Renamed' } })
    fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(onSaved).toHaveBeenCalled())
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/dashboards/mine?platform=example')
    expect(init.method).toBe('PUT')
    const sent = JSON.parse(String(init.body)) as Dashboard
    expect(sent.name).toBe('Renamed')
    expect(sent.rows[0]!.columns[0]!.panels.map((p) => p.type)).toEqual(['status', 'chart'])
  })

  it('will not save a column with nothing in it', async () => {
    await i18n.changeLanguage('en')
    render(
      <ThemeModeProvider>
        <DashboardEditor
          dashboard={{ ...board, rows: [{ columns: [{ panels: [] }] }] }}
          platform="example"
          specs={specs as never}
          onSaved={() => {}}
          onCancel={() => {}}
        />
      </ThemeModeProvider>,
    )
    expect(screen.getByText('Save')).toBeDisabled()
  })

  describe('drag and drop', () => {
    const two: Dashboard = {
      id: 'two',
      platform: 'example',
      name: 'Two',
      rows: [
        {
          columns: [
            { width: 1, panels: [{ type: 'chart', signal: 'requests', title: 'A' }] },
            {
              width: 1,
              panels: [
                { type: 'status', title: 'B' },
                { type: 'status', title: 'C' },
              ],
            },
          ],
        },
        { columns: [{ width: 1, panels: [{ type: 'alerts', title: 'D' }] }] },
      ],
    }

    async function dragAndSave(drag: (container: HTMLElement) => void): Promise<Dashboard> {
      await i18n.changeLanguage('en')
      const fetch = vi.fn(async (_url: string, init?: RequestInit) => ({
        ok: true,
        json: async () => JSON.parse(String(init?.body)) as Dashboard,
      }))
      vi.stubGlobal('fetch', fetch)
      const onSaved = vi.fn()
      const { container } = render(
        <ThemeModeProvider>
          <DashboardEditor
            dashboard={two}
            platform="example"
            specs={specs as never}
            onSaved={onSaved}
            onCancel={() => {}}
          />
        </ThemeModeProvider>,
      )
      drag(container)
      fireEvent.click(screen.getByText('Save'))
      await waitFor(() => expect(onSaved).toHaveBeenCalled())
      const [, init] = fetch.mock.calls[0] as unknown as [string, RequestInit]
      return JSON.parse(String(init.body)) as Dashboard
    }

    const drop = (handle: Element, target: Element) => {
      fireEvent.dragStart(handle)
      fireEvent.dragOver(target)
      fireEvent.drop(target)
    }
    const titles = (d: Dashboard) =>
      d.rows.map((r) => r.columns.map((c) => c.panels.map((p) => p.title).join('')))

    it('moves a row above another', async () => {
      const sent = await dragAndSave((c) =>
        drop(screen.getAllByLabelText('Drag to move this row')[1]!, c.querySelector('#row-0')!),
      )
      expect(titles(sent)).toEqual([['D'], ['A', 'BC']])
    })

    it('moves a column within its row', async () => {
      const sent = await dragAndSave((c) =>
        drop(
          screen.getAllByLabelText('Drag to move this column, within the row or into another')[1]!,
          c.querySelector('#column-0-0')!,
        ),
      )
      expect(titles(sent)).toEqual([['BC', 'A'], ['D']])
    })

    it('moves a panel into a column of another row', async () => {
      const sent = await dragAndSave((c) =>
        drop(
          screen.getAllByLabelText('Drag onto another panel or into a column')[2]!,
          c.querySelector('#column-1-0')!,
        ),
      )
      expect(titles(sent)).toEqual([['A', 'B'], ['DC']])
    })

    it('puts a panel dropped on another panel in its place', async () => {
      const sent = await dragAndSave((c) =>
        drop(
          screen.getAllByLabelText('Drag onto another panel or into a column')[1]!,
          c.querySelector('#panel-0-0-0')!,
        ),
      )
      expect(titles(sent)).toEqual([['BA', 'C'], ['D']])
    })
  })
})
