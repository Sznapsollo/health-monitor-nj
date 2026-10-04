import { describe, expect, it } from 'vitest'

import type { Point, SignalView } from '../ws/types'
import { mergePoints, mergeView } from './merge'

function point(minute: number, count: number): Point {
  return { minute, count, avgMs: count * 10, minMs: 1, maxMs: 100 }
}

function view(over: Partial<SignalView> = {}): SignalView {
  return {
    platform: 'test',
    signal: 'requests',
    from: 100,
    to: 110,
    detailFrom: 100,
    total: [],
    ...over,
  }
}

describe('mergePoints', () => {
  it('replaces the minute that changed and keeps the rest', () => {
    const got = mergePoints([point(100, 1), point(101, 2)], [point(101, 9)], 100)
    expect(got.map((p) => [p.minute, p.count])).toEqual([
      [100, 1],
      [101, 9],
    ])
  })

  it('appends a new minute in order', () => {
    const got = mergePoints([point(101, 1)], [point(100, 5)], 100)
    expect(got.map((p) => p.minute)).toEqual([100, 101])
  })

  it('drops minutes that fell out of the window', () => {
    const got = mergePoints([point(90, 1), point(100, 1)], [point(101, 1)], 100)
    expect(got.map((p) => p.minute)).toEqual([100, 101])
  })
})

describe('mergeView', () => {
  it('takes the update whole when there is nothing yet', () => {
    const update = view({ total: [point(100, 1)] })
    expect(mergeView(undefined, update)).toBe(update)
  })

  it('keeps history the update does not mention', () => {
    const current = view({ total: [point(100, 1), point(101, 1)] })
    const update = view({ from: 100, to: 102, total: [point(102, 7)] })
    const got = mergeView(current, update)
    expect(got.total.map((p) => [p.minute, p.count])).toEqual([
      [100, 1],
      [101, 1],
      [102, 7],
    ])
  })

  it('merges each group and its sub-groups', () => {
    const current = view({
      group: 'url',
      total: [point(100, 2)],
      groups: [
        {
          value: '/a',
          count: 2,
          avgMs: 20,
          points: [point(100, 2)],
          groups: [{ value: 'anna', count: 2, avgMs: 20, points: [point(100, 2)] }],
        },
      ],
    })
    const update = view({
      group: 'url',
      total: [point(101, 3)],
      groups: [
        {
          value: '/a',
          count: 5,
          avgMs: 30,
          points: [point(101, 3)],
          groups: [{ value: 'anna', count: 5, avgMs: 30, points: [point(101, 3)] }],
        },
      ],
    })

    const got = mergeView(current, update)
    expect(got.groups?.[0].points.map((p) => p.minute)).toEqual([100, 101])
    expect(got.groups?.[0].groups?.[0].points.map((p) => p.minute)).toEqual([100, 101])
    // The window totals come from the server, which ranked over the window.
    expect(got.groups?.[0].count).toBe(5)
  })

  it('follows the server when a value drops out of the top N', () => {
    const current = view({
      group: 'url',
      groups: [
        { value: '/a', count: 5, avgMs: 1, points: [point(100, 5)] },
        { value: '/b', count: 1, avgMs: 1, points: [point(100, 1)] },
      ],
    })
    const update = view({
      group: 'url',
      groups: [{ value: '/a', count: 6, avgMs: 1, points: [point(101, 1)] }],
    })
    expect(mergeView(current, update).groups?.map((g) => g.value)).toEqual(['/a'])
  })

  it('carries the honest edges of the window across', () => {
    const current = view({ detailFrom: 100, partial: false })
    const update = view({ detailFrom: 105, partial: true, capped: { url: 3 } })
    const got = mergeView(current, update)
    expect(got.detailFrom).toBe(105)
    expect(got.partial).toBe(true)
    expect(got.capped).toEqual({ url: 3 })
  })
})

describe('a server that sends null where a list was promised', () => {
  // Go marshals a nil slice as null. When that reached the merge it threw,
  // the socket layer swallowed the error, and the page froze while still
  // showing "Live" — with nothing in the console.
  it('survives a null list of points', () => {
    const current = view({ total: [point(100, 1)] })
    const update = { ...view({ from: 100, to: 101 }), total: null } as unknown as SignalView
    expect(() => mergeView(current, update)).not.toThrow()
    expect(mergeView(current, update).total.map((p) => p.minute)).toEqual([100])
  })

  it('survives a group whose points are null', () => {
    const current = view({
      group: 'url',
      groups: [{ value: '/a', count: 1, avgMs: 1, points: [point(100, 1)] }],
    })
    const update = {
      ...view({ from: 100, to: 101 }),
      group: 'url',
      groups: [{ value: '/a', count: 1, avgMs: 1, points: null }],
    } as unknown as SignalView

    expect(() => mergeView(current, update)).not.toThrow()
    const merged = mergeView(current, update)
    expect(merged.groups?.[0].points.map((p) => p.minute)).toEqual([100])
  })
})

describe('group tiles with their own window', () => {
  it('trims the tiles to groupsFrom while the main chart keeps its longer window', () => {
    const base: SignalView = {
      platform: 'example',
      signal: 'requests',
      from: 100,
      to: 200,
      detailFrom: 100,
      groupsFrom: 181,
      total: [point(100, 1), point(190, 2)],
      groups: [{ value: '8080', count: 3, avgMs: 10, points: [point(181, 1), point(190, 2)] }],
    }
    const next = mergeView(base, {
      ...base,
      from: 101,
      to: 201,
      groupsFrom: 182,
      total: [point(201, 5)],
      groups: [{ value: '8080', count: 7, avgMs: 10, points: [point(201, 5)] }],
    })
    expect(next.total.map((p) => p.minute)).toEqual([190, 201])
    expect(next.groups?.[0]?.points.map((p) => p.minute)).toEqual([190, 201])
    expect(next.groupsFrom).toBe(182)
  })
})

describe('merging keeps what did not change', () => {
  it('returns the same points when the update repeats the last minute', () => {
    const have = [point(100, 1), point(101, 2)]
    expect(mergePoints(have, [point(101, 2)], 100)).toBe(have)
  })

  it('returns the same points when there is nothing to add or trim', () => {
    const have = [point(100, 1), point(101, 2)]
    expect(mergePoints(have, [], 100)).toBe(have)
  })

  it('replaces or appends the latest minute without touching the others', () => {
    const have = [point(100, 1), point(101, 2)]
    const got = mergePoints(have, [point(101, 5), point(102, 1)], 100)
    expect(got.map((p) => [p.minute, p.count])).toEqual([
      [100, 1],
      [101, 5],
      [102, 1],
    ])
    expect(got[0]).toBe(have[0])
  })

  it('keeps a group that saw nothing new, so its chart is not redrawn', () => {
    const quiet = { value: '/quiet', count: 3, avgMs: 10, points: [point(100, 3)] }
    const busy = { value: '/busy', count: 1, avgMs: 10, points: [point(100, 1)] }
    const current = view({ total: [point(100, 4)], groups: [busy, quiet] })
    const got = mergeView(
      current,
      view({
        total: [point(100, 5)],
        groups: [
          { ...busy, count: 2, points: [point(100, 2)] },
          { ...quiet, points: [] },
        ],
      }),
    )
    expect(got.groups?.[1]).toBe(quiet)
    expect(got.groups?.[0]).not.toBe(busy)
  })

  it('returns the same view when nothing changed at all', () => {
    const current = view({ total: [point(100, 4)] })
    expect(mergeView(current, view({ total: [point(100, 4)] }))).toBe(current)
  })
})
