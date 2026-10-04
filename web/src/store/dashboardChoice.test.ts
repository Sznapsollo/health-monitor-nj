import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { Dashboard } from '../api/dashboards'
import { useMonitorStore } from './useMonitorStore'

const board = (id: string, isDefault = false): Dashboard => ({
  id,
  platform: 'example',
  name: id,
  ...(isDefault ? { default: true } : {}),
  rows: [{ columns: [{ panels: [{ type: 'status' }] }] }],
})

function serve(dashboards: Dashboard[]) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ ok: true, json: async () => ({ dashboards }) })),
  )
}

describe('the dashboard a viewer likes', () => {
  beforeEach(() => {
    globalThis.localStorage?.clear()
    useMonitorStore.setState({ platform: 'example', dashboards: [], dashboardId: null })
  })

  it('is remembered when opened and preferred over the default after a reload', async () => {
    serve([board('ops', true), board('mine')])
    await useMonitorStore.getState().reloadDashboards(null)
    expect(useMonitorStore.getState().dashboardId).toBe('ops')

    useMonitorStore.getState().openDashboard('mine')
    useMonitorStore.setState({ dashboardId: null })
    await useMonitorStore.getState().reloadDashboards(null)
    expect(useMonitorStore.getState().dashboardId).toBe('mine')
  })

  it('falls back to the default when the remembered one was deleted', async () => {
    globalThis.localStorage?.setItem('hm.dashboard', JSON.stringify({ example: 'gone' }))
    serve([board('ops', true), board('mine')])
    await useMonitorStore.getState().reloadDashboards(null)
    expect(useMonitorStore.getState().dashboardId).toBe('ops')
  })
})
