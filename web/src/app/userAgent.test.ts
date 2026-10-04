import { describe, expect, it } from 'vitest'

import { describeUserAgent } from './userAgent'

describe('describeUserAgent', () => {
  it.each([
    [
      'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36',
      'Chrome 128',
      'Linux',
    ],
    [
      'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 Edg/128.0.0.0',
      'Edge 128',
      'Windows',
    ],
    [
      'Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0',
      'Firefox 131',
      'Linux',
    ],
    [
      'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1',
      'Safari 17',
      'iOS',
    ],
    [
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15',
      'Safari 17',
      'macOS',
    ],
  ])('reads %s', (ua, browser, system) => {
    expect(describeUserAgent(ua)).toEqual({ browser, system })
  })

  it('says nothing for nothing', () => {
    expect(describeUserAgent(undefined)).toEqual({ browser: '', system: '' })
  })
})
