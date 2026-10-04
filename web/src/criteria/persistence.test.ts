import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Criteria } from '../ws/types'
import {
  criteriaFromSearch,
  deleteSaved,
  loadActive,
  loadSaved,
  saveActive,
  saveNamed,
  searchFromCriteria,
} from './persistence'

const criteria: Criteria = {
  signal: 'requests',
  historyMinutes: 180,
  group: 'url',
  groupTop: 10,
  groupFilter: 'api, admin',
  sub: 'user',
  sortBy: 'avgMs',
}

describe('saved criteria', () => {
  beforeEach(() => globalThis.localStorage?.clear())

  it('saves, lists and deletes by name', () => {
    expect(loadSaved()).toEqual([])

    saveNamed({ name: 'slow pages', platform: 'p', criteria: [criteria] })
    saveNamed({ name: 'all', platform: 'p', criteria: [] })
    expect(loadSaved().map((s) => s.name)).toEqual(['all', 'slow pages'])

    // Saving the same name again replaces it rather than duplicating.
    saveNamed({ name: 'all', platform: 'p', criteria: [criteria] })
    expect(loadSaved()).toHaveLength(2)
    expect(loadSaved().find((s) => s.name === 'all')?.criteria).toHaveLength(1)

    deleteSaved('all')
    expect(loadSaved().map((s) => s.name)).toEqual(['slow pages'])
  })

  it('remembers the criteria in use', () => {
    expect(loadActive()).toBeNull()
    saveActive('example', [criteria])
    expect(loadActive()).toEqual({ platform: 'example', criteria: [criteria] })
  })

  it('ignores rubbish in storage rather than breaking the page', () => {
    globalThis.localStorage.setItem('hm.savedCriteria', 'not json')
    expect(loadSaved()).toEqual([])

    globalThis.localStorage.setItem('hm.savedCriteria', '{"not":"an array"}')
    expect(loadSaved()).toEqual([])

    globalThis.localStorage.setItem('hm.criteria', '{"criteria":"nope"}')
    expect(loadActive()).toBeNull()
  })

  it('survives storage being unavailable', () => {
    const throwing = {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
    }
    vi.stubGlobal('localStorage', throwing)
    expect(loadSaved()).toEqual([])
    expect(() => saveActive('p', [criteria])).not.toThrow()
    vi.unstubAllGlobals()
  })
})

describe('criteria in the URL', () => {
  it('round-trips through the query string', () => {
    const search = searchFromCriteria(criteria, 'example')
    const back = criteriaFromSearch('?' + search)
    expect(back).toEqual(criteria)
  })

  it('leaves defaults out of the link', () => {
    const search = searchFromCriteria({ signal: 'requests', sortBy: 'count' })
    expect(search).toBe('signal=requests')
  })

  it('is null without a signal', () => {
    expect(criteriaFromSearch('?group=url')).toBeNull()
  })

  it('ignores values that make no sense', () => {
    const got = criteriaFromSearch('?signal=requests&minutes=-5&top=abc&sort=sideways')
    expect(got).toEqual({
      signal: 'requests',
      historyMinutes: undefined,
      group: undefined,
      groupTop: undefined,
      groupFilter: undefined,
      sub: undefined,
      subTop: undefined,
      subFilter: undefined,
    })
  })
})

afterEach(() => {
  globalThis.localStorage?.clear()
})
