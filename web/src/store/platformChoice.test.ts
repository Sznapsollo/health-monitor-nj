import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useMonitorStore } from './useMonitorStore'

const catalogue = {
  platforms: [
    { name: 'example', signals: [] },
    { name: 'scaneiro', signals: [] },
  ],
}

function serve() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => ({
      ok: true,
      json: async () =>
        String(url).startsWith('/api/catalogue')
          ? catalogue
          : { dashboards: [], required: false, authenticated: true },
    })),
  )
}

function reload() {
  useMonitorStore.setState({ platform: '', catalogue: null, platforms: [] })
  return useMonitorStore.getState().loadCatalogue()
}

describe('the platform a viewer was on', () => {
  beforeEach(() => {
    globalThis.localStorage?.clear()
    globalThis.history.replaceState(null, '', '/')
    serve()
  })

  it('is opened again after a reload', async () => {
    await reload()
    expect(useMonitorStore.getState().platform).toBe('example')

    useMonitorStore.getState().setPlatform('scaneiro')
    expect(globalThis.location.search).toBe('?platform=scaneiro')

    globalThis.history.replaceState(null, '', '/')
    await reload()
    expect(useMonitorStore.getState().platform).toBe('scaneiro')
  })

  it('wins over an old link to another platform once switched', async () => {
    globalThis.history.replaceState(null, '', '/?platform=example&signal=requests')
    await reload()
    useMonitorStore.getState().setPlatform('scaneiro')
    await reload()
    expect(useMonitorStore.getState().platform).toBe('scaneiro')
  })

  it('is forgotten when that platform is gone', async () => {
    globalThis.localStorage?.setItem(
      'hm.criteria',
      JSON.stringify({ platform: 'gone', criteria: [] }),
    )
    await reload()
    expect(useMonitorStore.getState().platform).toBe('example')
  })
})
