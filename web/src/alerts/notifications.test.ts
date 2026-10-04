import { beforeEach, describe, expect, it } from 'vitest'

import type { Alert } from '../api/alerts'
import { defaultSettings, loadSettings, saveSettings, shouldNotify } from './notifications'

function alert(over: Partial<Alert> = {}): Alert {
  return {
    id: 'a1',
    platform: 'test',
    level: 'ERROR',
    message: 'boom',
    count: 1,
    first: '2026-09-19T10:00:00Z',
    last: '2026-09-19T10:00:00Z',
    ...over,
  }
}

describe('shouldNotify', () => {
  it('interrupts for a new alert at a chosen level', () => {
    expect(shouldNotify(alert(), defaultSettings, true)).toBe(true)
  })

  it('stays quiet for a repeat', () => {
    expect(shouldNotify(alert(), defaultSettings, false)).toBe(false)
  })

  it('stays quiet for a level the viewer did not choose', () => {
    expect(shouldNotify(alert({ level: 'INFO' }), defaultSettings, true)).toBe(false)
  })

  it('never makes a sound for a silenced alert', () => {
    // The whole point of silencing is that it stops the noise, not the record.
    expect(shouldNotify(alert({ silenced: true }), defaultSettings, true)).toBe(false)
  })
})

describe('settings', () => {
  beforeEach(() => globalThis.localStorage?.clear())

  it('round-trips through storage', () => {
    saveSettings({ sound: true, browser: true, levels: ['ERROR'] })
    expect(loadSettings()).toEqual({ sound: true, browser: true, levels: ['ERROR'] })
  })

  it('falls back to the defaults on rubbish', () => {
    globalThis.localStorage.setItem('hm.notifications', 'not json')
    expect(loadSettings()).toEqual(defaultSettings)
  })
})
