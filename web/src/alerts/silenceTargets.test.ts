import { describe, expect, it } from 'vitest'

import type { Alert } from '../api/alerts'
import i18n from '../i18n'
import { describeTarget, silenceMessageOf } from './silenceTargets'

const base: Alert = {
  id: '1',
  platform: 'example',
  level: 'WARN',
  message: 'queue X is stuck',
  count: 1,
  first: '2026-09-25T10:00:00Z',
  last: '2026-09-25T10:00:00Z',
}

describe('silence targets', () => {
  it('starts from the message, a slow request from its URL', () => {
    expect(silenceMessageOf(base)).toBe('queue X is stuck')
    expect(
      silenceMessageOf({
        ...base,
        category: 'latency',
        message: '/api/x took 2500 ms',
        data: { url: '/api/x' },
      }),
    ).toBe('/api/x took ')
  })

  it('says in words what a silence hides', async () => {
    await i18n.changeLanguage('en')
    const t = i18n.t.bind(i18n)
    expect(describeTarget('match:contains="stuck"', t)).toBe(
      'Alerts whose message contains “stuck”',
    )
    expect(describeTarget('category:Warning JOB', t)).toBe('All alerts of category “Warning JOB”')
    expect(describeTarget('status:servers/web-2', t)).toBe('status:servers/web-2')
  })
})
