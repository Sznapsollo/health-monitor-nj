import { beforeEach, describe, expect, it } from 'vitest'

import { DEFAULT_COLUMNS, fieldValue, fieldsFound, loadColumns, saveColumns } from './columns'

describe('search columns', () => {
  beforeEach(() => globalThis.localStorage?.clear())

  it('falls back to the default layout when nothing usable is saved', () => {
    expect(loadColumns()).toEqual(DEFAULT_COLUMNS)
    globalThis.localStorage?.setItem('hm.search.columns', 'not json')
    expect(loadColumns()).toEqual(DEFAULT_COLUMNS)
  })

  it('keeps a saved layout and adds built-in columns it does not know, hidden', () => {
    saveColumns('dailyLogs', [
      { id: 'field:ipAddress', visible: true },
      { id: 'when', visible: true },
      { id: 'nonsense', visible: true },
    ])
    const got = loadColumns('dailyLogs')
    expect(got.slice(0, 2)).toEqual([
      { id: 'field:ipAddress', visible: true },
      { id: 'when', visible: true },
    ])
    expect(got.find((c) => c.id === 'nonsense')).toBeUndefined()
    expect(got.find((c) => c.id === 'summary')).toEqual({ id: 'summary', visible: false })
  })

  it('keeps a layout per kind of log, each starting from the one saved before', () => {
    globalThis.localStorage?.setItem(
      'hm.search.columns',
      JSON.stringify([{ id: 'field:port', visible: true }]),
    )
    saveColumns('sendLogs', [{ id: 'field:subject', visible: true }])
    expect(loadColumns('sendLogs')[0]).toEqual({ id: 'field:subject', visible: true })
    expect(loadColumns('dailyLogs')[0]).toEqual({ id: 'field:port', visible: true })
    expect(loadColumns('')[0]).toEqual({ id: 'field:port', visible: true })
  })

  it('offers the fields rows carry, most common first, and shows any value', () => {
    const rows = [
      { platform: 'p', signal: 's', ts: '', payload: { ipAddress: '10.0.0.7', port: 8080 } },
      { platform: 'p', signal: 's', ts: '', payload: { ipAddress: '10.0.0.8', data: { a: 1 } } },
    ]
    expect(fieldsFound(rows, [{ id: 'field:port', visible: true }])).toEqual(['ipAddress', 'data'])
    expect(fieldValue(rows[0]!, 'port')).toBe('8080')
    expect(fieldValue(rows[1]!, 'data')).toBe('{"a":1}')
    expect(fieldValue(rows[1]!, 'missing')).toBe('')
  })
})
