import { afterEach, describe, expect, it, vi } from 'vitest'

import { useHealthStore } from './useHealthStore'

const issue = {
  key: 'unknown_signal/demo/checkout/metric',
  source: 'quarantine',
  reason: 'unknown_signal',
  count: 1,
  new: 1,
}

describe('useHealthStore', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    useHealthStore.setState({ issues: [], unknown: 0, error: null, loaded: false })
  })

  it('keeps a mark when a read started before it answers after it', async () => {
    let answerRead: () => void = () => {}
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) => {
        if (url === '/api/health/known') {
          return Promise.resolve({
            ok: true,
            json: async () => ({ issues: [{ ...issue, known: true }], unknown: 0 }),
          })
        }
        return new Promise((resolve) => {
          answerRead = () =>
            resolve({
              ok: true,
              json: async () => ({ issues: [{ ...issue, known: false }], unknown: 1 }),
            })
        })
      }),
    )

    const read = useHealthStore.getState().load()
    await useHealthStore.getState().mark([issue.key], true)
    answerRead()
    await read

    expect(useHealthStore.getState().issues[0]?.known).toBe(true)
    expect(useHealthStore.getState().unknown).toBe(0)
  })

  it('takes a read that started after the mark', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => ({
        ok: true,
        json: async () =>
          url === '/api/health/known'
            ? { issues: [{ ...issue, known: true }], unknown: 0 }
            : { issues: [], unknown: 0 },
      })),
    )

    await useHealthStore.getState().mark([issue.key], true)
    await useHealthStore.getState().load()

    expect(useHealthStore.getState().issues).toEqual([])
  })
})
