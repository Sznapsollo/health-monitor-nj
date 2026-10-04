import { afterEach, describe, expect, it, vi } from 'vitest'

import { fetchCatalogue } from './catalogue'

describe('fetchCatalogue', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('turns a missing dimension list into an empty one', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        json: async () => ({
          platforms: [{ name: 'p', signals: [{ name: 'queues', kind: 'gauge', dims: null }] }],
        }),
      })),
    )
    const catalogue = await fetchCatalogue()
    expect(catalogue.platforms[0]?.signals[0]?.dims).toEqual([])
  })
})
