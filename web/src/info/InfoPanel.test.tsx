import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import i18n from '../i18n'
import { ThemeModeProvider } from '../theme/ThemeModeProvider'
import { InfoPanel } from './InfoPanel'
import { contains, epochOf } from './json'

const entries = [
  {
    signal: 'configServerJobs',
    key: 'job_host_ConfigServerVerticle',
    displayName: 'Config server jobs',
    packetType: 'configServerJobsListStatus',
    defined: true,
    received: '2026-09-26T09:15:44Z',
    versions: 3,
  },
]

const report = {
  id: 7,
  received: '2026-09-26T09:15:44Z',
  content: {
    type: 'configServerJobsListStatus',
    jobsStatusMap: {
      saveDocument: { name: 'saveDocument', heartBeat: 1790413801933 },
      deleteBucket: { name: 'deleteBucket', heartBeat: 1790413801000 },
    },
  },
  versions: [
    { id: 7, received: '2026-09-26T09:15:44Z', size: 3900 },
    { id: 6, received: '2026-09-26T09:14:09Z', size: 3900 },
  ],
}

describe('InfoPanel', () => {
  beforeEach(async () => {
    await i18n.changeLanguage('en')
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => ({
        ok: true,
        headers: new Headers({ 'content-type': 'application/json' }),
        json: async () => (String(url).includes('/report') ? report : { entries }),
      })),
    )
  })

  afterEach(() => vi.unstubAllGlobals())

  it('lists each sender and shows its latest report as a tree to search', async () => {
    render(
      <ThemeModeProvider>
        <InfoPanel platform="example" />
      </ThemeModeProvider>,
    )
    expect(await screen.findByText('job_host_ConfigServerVerticle')).toBeInTheDocument()
    expect(screen.getByText('up')).toBeInTheDocument()
    expect(await screen.findByText('saveDocument', { selector: 'span' })).toBeInTheDocument()
    expect(screen.getByText('deleteBucket', { selector: 'span' })).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Search'), { target: { value: 'savedoc' } })
    expect(screen.queryByText('deleteBucket', { selector: 'span' })).not.toBeInTheDocument()
    expect(screen.getAllByText('saveDoc', { selector: 'mark' })).toHaveLength(2)
  })
  it('removes a sender after asking', async () => {
    const calls: { url: string; method?: string }[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        calls.push({ url: String(url), method: init?.method })
        return {
          ok: true,
          headers: new Headers({ 'content-type': 'application/json' }),
          json: async () => (String(url).includes('/report') ? report : { entries }),
        }
      }),
    )
    vi.stubGlobal(
      'confirm',
      vi.fn(() => true),
    )
    render(
      <ThemeModeProvider>
        <InfoPanel platform="example" />
      </ThemeModeProvider>,
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Remove' }))
    await vi.waitFor(() =>
      expect(calls).toContainEqual({
        url: '/api/info/report?platform=example&signal=configServerJobs&key=job_host_ConfigServerVerticle',
        method: 'DELETE',
      }),
    )
  })
})

describe('json helpers', () => {
  it('finds text in names and values at any depth', () => {
    const value = { jobs: { saveDocument: { heartBeat: 1 } }, build: 'abc' }
    expect(contains(null, value, 'heartbeat')).toBe(true)
    expect(contains(null, value, 'abc')).toBe(true)
    expect(contains(null, value, 'nowhere')).toBe(false)
  })

  it('reads only millisecond timestamps of this century as times', () => {
    expect(epochOf(1790413801933)).not.toBeNull()
    expect(epochOf(8080)).toBeNull()
    expect(epochOf('1790413801933')).toBeNull()
  })
})
