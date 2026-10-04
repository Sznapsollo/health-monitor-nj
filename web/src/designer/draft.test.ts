import { describe, expect, it } from 'vitest'

import type { Candidate } from '../api/signals'
import {
  blankDraft,
  draftFromCandidate,
  draftFromSpec,
  editOf,
  previewPoints,
  problemOf,
} from './draft'

const checkout: Candidate = {
  platform: 'demo',
  signal: 'checkout',
  kind: 'metric',
  count: 3,
  first: '2026-09-23T10:00:00Z',
  last: '2026-09-23T10:01:00Z',
  dims: { url: ['/pay'], port: ['8080', '8081'] },
  values: { count: { min: 1, max: 2 }, ms: { min: 10, max: 90 } },
  minutes: [
    { minute: 100, packets: 2, sums: { count: 3, ms: 100 } },
    { minute: 101, packets: 1, sums: { count: 1, ms: 90 } },
  ],
}

describe('draftFromCandidate', () => {
  it('keeps every dimension seen and maps the usual value fields', () => {
    const d = draftFromCandidate(checkout)
    expect(d.kind).toBe('timeseries')
    expect(d.dims.map((dim) => dim.name)).toEqual(['port', 'url'])
    expect(d.dims[0]?.samples).toEqual(['8080', '8081'])
    expect(d.countField).toBe('count')
    expect(d.msField).toBe('ms')
  })

  it('names an id by its companion name field', () => {
    const d = draftFromCandidate({ ...checkout, dims: { account: ['42'], accountName: ['Acme'] } })
    expect(d.dims.map((dim) => dim.labelDim)).toEqual(['accountName', ''])
  })

  it('makes a gauge of a gauge, with no dimensions or value mapping', () => {
    const d = draftFromCandidate({ ...checkout, kind: 'gauge', dims: { label: ['a'] } })
    expect(d.kind).toBe('gauge')
    expect(d.dims).toEqual([])
    expect(d.countField).toBe('')
  })
})

describe('editOf', () => {
  it('sends only the kept dimensions, named', () => {
    const d = draftFromCandidate(checkout)
    d.dims[1] = { ...d.dims[1]!, include: false }
    d.dims[0] = { ...d.dims[0]!, displayName: 'Port' }
    d.displayName = ''
    const edit = editOf(d)
    expect(edit.dims).toEqual([{ name: 'port', displayName: 'Port' }])
    expect(edit.displayName).toBe('checkout')
    expect(edit.values).toEqual({ count: 'count', ms: 'ms' })
  })

  it('drops a label naming a dimension that is not kept', () => {
    const d = draftFromCandidate({ ...checkout, dims: { account: ['42'], accountName: ['Acme'] } })
    expect(editOf(d).dims[0]).toEqual({
      name: 'account',
      displayName: 'account',
      labelDim: 'accountName',
    })
    d.dims[1] = { ...d.dims[1]!, include: false }
    expect(editOf(d).dims).toEqual([{ name: 'account', displayName: 'account' }])
  })

  it('round-trips a definition the catalogue describes', () => {
    const d = draftFromSpec('demo', {
      name: 'jobs',
      kind: 'timeseries',
      displayName: 'Jobs',
      dims: [{ name: 'jobName', displayName: 'Job' }],
      values: { count: 'count', ms: '' },
      views: [{ id: 'byGroup', type: 'minuteSeriesPerGroup', top: 12 }],
      retention: { hotDetailMinutes: 60, hotTotalsMinutes: 600, durableDays: 7 },
      maxKeys: 500,
    })
    expect(d.isNew).toBe(false)
    expect(editOf(d)).toMatchObject({
      dims: [{ name: 'jobName', displayName: 'Job' }],
      values: { count: 'count', ms: '' },
      retention: { hotDetailMinutes: 60, hotTotalsMinutes: 600, durableDays: 7 },
      maxKeys: 500,
      groupTop: 12,
    })
  })
})

describe('problemOf', () => {
  it('names what stops a save', () => {
    expect(problemOf(blankDraft('demo'))).toBe('designer.badName')
    const d = { ...blankDraft('demo'), name: 'checkout' }
    expect(problemOf(d)).toBeNull()
    expect(
      problemOf({
        ...d,
        dims: [{ name: '', displayName: '', labelDim: '', include: true, samples: [] }],
      }),
    ).toBe('designer.emptyDim')
    const twice = { name: 'a', displayName: 'a', labelDim: '', include: true, samples: [] }
    expect(problemOf({ ...d, dims: [twice, twice] })).toBe('designer.duplicateDim')
    expect(problemOf({ ...d, hotTotalsMinutes: 10, hotDetailMinutes: 20 })).toBe(
      'designer.badRetention',
    )
  })
})

describe('previewPoints', () => {
  it('draws what the main chart would, for the mapping chosen', () => {
    expect(previewPoints(checkout, 'count', 'ms').map((p) => [p.count, p.avgMs])).toEqual([
      [3, 50],
      [1, 90],
    ])
    expect(previewPoints(checkout, '', '').map((p) => [p.count, p.avgMs])).toEqual([
      [2, 0],
      [1, 0],
    ])
  })
})

describe('kept pairs and detail days', () => {
  const spec = {
    name: 'apiRequests',
    kind: 'timeseries',
    displayName: 'API requests',
    dims: [
      { name: 'port', displayName: 'Port' },
      { name: 'account', displayName: 'Account' },
      { name: 'url', displayName: 'URL' },
    ],
    values: { count: 'count', ms: 'ms' },
    retention: { hotDetailMinutes: 120, hotTotalsMinutes: 2880, durableDays: 30, detailDays: 5 },
    keepPairs: [['port', 'account']] as [string, string][],
  }

  it('survive a re-save from the designer', () => {
    const edit = editOf(draftFromSpec('example', spec))
    expect(edit.keepPairs).toEqual([['port', 'account']])
    expect(edit.retention.detailDays).toBe(5)
  })

  it('drop a pair whose dimension is no longer included', () => {
    const draft = draftFromSpec('example', spec)
    draft.dims = draft.dims.map((d) => (d.name === 'account' ? { ...d, include: false } : d))
    expect(editOf(draft).keepPairs).toEqual([])
  })
})

describe('an info signal', () => {
  it('names its packet type and how many reports to keep', () => {
    const draft = {
      ...blankDraft('example'),
      name: 'configServerJobs',
      kind: 'info' as const,
      durableDays: 30,
    }
    expect(problemOf(draft)).toBe('designer.noPacketType')

    const named = { ...draft, packetType: ' configServerJobsListStatus ' }
    expect(problemOf(named)).toBeNull()
    const edit = editOf(named)
    expect(edit.packetType).toBe('configServerJobsListStatus')
    expect(edit.retention.versions).toBe(20)
    expect(problemOf({ ...named, versions: 0 })).toBe('designer.badInfoRetention')
    expect(editOf({ ...named, merge: true }).merge).toBe(true)
    expect(editOf(named).noStatus).toBe(false)
    expect(editOf({ ...named, noStatus: true }).noStatus).toBe(true)
  })
})

describe('a report type waiting to be defined', () => {
  it('opens as an info signal naming that packet type', () => {
    const draft = draftFromCandidate({
      platform: 'example',
      signal: 'serverMemReport',
      kind: 'info',
      count: 3,
      first: '2026-09-26T10:00:00Z',
      last: '2026-09-26T10:05:00Z',
      dims: { serverMemName: [], serverMemUsage: [] },
      values: {},
      minutes: [],
    })
    expect(draft).toMatchObject({
      kind: 'info',
      packetType: 'serverMemReport',
      name: 'serverMemReport',
    })
    expect(problemOf(draft)).toBeNull()
  })
})

describe('a report type charted from its fields', () => {
  it('offers its text fields as dimensions and keeps the packet type', () => {
    const draft = draftFromCandidate({
      platform: 'example',
      signal: 'ksefEventStatusData',
      kind: 'info',
      count: 2,
      first: '2026-09-29T14:00:00Z',
      last: '2026-09-29T14:05:00Z',
      dims: {
        type: ['ksefEventStatusData'],
        taskStatus: ['COMPLETED'],
        itemsCount: ['1'],
        nested: [],
      },
      values: { itemsCount: { min: 1, max: 1 }, executionTime: { min: 1, max: 1 } },
      minutes: [],
    })
    const chart = { ...draft, kind: 'timeseries' as const, countField: 'itemsCount' }
    expect(chart.dims.filter((d) => d.include).map((d) => d.name)).toEqual(['taskStatus'])
    expect(chart.msField).toBe('executionTime')
    expect(editOf(chart)).toMatchObject({
      packetType: 'ksefEventStatusData',
      values: { count: 'itemsCount', ms: 'executionTime' },
    })
  })
})

describe('colours', () => {
  it('saves value colours and refuses a row without a value or a hex colour', () => {
    const d = {
      ...blankDraft('example'),
      name: 'tasks',
      colors: [['COMPLETED', '#2e7d32']] as [string, string][],
    }
    expect(editOf(d).colors).toEqual({ COMPLETED: '#2e7d32' })
    expect(problemOf(d)).toBeNull()
    expect(problemOf({ ...d, colors: [['', '#2e7d32']] })).toBe('designer.badColor')
    expect(problemOf({ ...d, colors: [['COMPLETED', 'green']] })).toBe('designer.badColor')
  })
})
