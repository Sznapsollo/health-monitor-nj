import { afterEach, describe, expect, it, vi } from 'vitest'

import { apiFetch } from './http'

describe('apiFetch', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('sends a page with a display token as a bearer header', async () => {
    const fetch = vi.fn(async () => ({ ok: true }) as Response)
    vi.stubGlobal('fetch', fetch)
    vi.stubGlobal('location', { search: '?display=1&token=abc%20def' })

    await apiFetch('/api/catalogue', { headers: { Accept: 'application/json' } })

    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/catalogue')
    const headers = new Headers(init.headers)
    expect(headers.get('Authorization')).toBe('Bearer abc def')
    expect(headers.get('Accept')).toBe('application/json')
  })

  it('leaves an ordinary page alone', async () => {
    const fetch = vi.fn(async () => ({ ok: true }) as Response)
    vi.stubGlobal('fetch', fetch)
    vi.stubGlobal('location', { search: '?signal=requests' })

    await apiFetch('/api/catalogue', { method: 'GET' })

    const [, init] = fetch.mock.calls[0] as unknown as [string, RequestInit]
    expect(init).toEqual({ method: 'GET' })
  })
})
