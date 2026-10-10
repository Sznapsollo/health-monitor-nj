import { describe, expect, it } from 'vitest'

import { tokens } from '../theme/tokens'
import type { Group, Point } from '../ws/types'
import {
  allMinutes,
  axisTooltip,
  groupSeries,
  minuteSeriesOption,
  showMainOf,
  subStyleOf,
  togetherSeries,
  type SeriesSpec,
} from './options'

function point(minute: number, count: number, avgMs = 10): Point {
  return { minute, count, avgMs, minMs: 1, maxMs: 100 }
}

const formatMinute = (m: number) => String(m)

function spec(series: SeriesSpec[], mode: 'light' | 'dark' = 'light') {
  return {
    series,
    mode,
    formatMinute,
    countLabel: 'count',
    latencyLabel: 'ms',
  }
}

describe('minuteSeriesOption', () => {
  it('takes its whole palette from the active theme', () => {
    for (const mode of ['light', 'dark'] as const) {
      const option = minuteSeriesOption(
        spec([{ name: 'a', points: [point(1, 1)], value: 'count', style: 'column' }], mode),
      )
      expect(option.color).toEqual(tokens[mode].series)
      expect(option.textStyle).toEqual({ color: tokens[mode].text })
    }
  })

  it('puts every series on one shared minute axis', () => {
    const option = minuteSeriesOption(
      spec([
        { name: 'a', points: [point(1, 1), point(3, 1)], value: 'count', style: 'column' },
        { name: 'b', points: [point(2, 5)], value: 'count', style: 'column' },
      ]),
    )
    expect((option.xAxis as { data: string[] }).data).toEqual(['1', '2', '3'])

    const series = option.series as { data: (number | null)[] }[]
    // A minute a series never saw is a gap, not a zero.
    expect(series[0].data).toEqual([1, null, 1])
    expect(series[1].data).toEqual([null, 5, null])
  })

  it('gives latency its own axis only when something uses it', () => {
    const counts = minuteSeriesOption(
      spec([{ name: 'a', points: [point(1, 1)], value: 'count', style: 'column' }]),
    )
    expect(counts.yAxis).toHaveLength(1)

    const both = minuteSeriesOption(
      spec([
        { name: 'count', points: [point(1, 1)], value: 'count', style: 'column' },
        { name: 'ms', points: [point(1, 1, 250)], value: 'avgMs', style: 'line', secondary: true },
      ]),
    )
    expect(both.yAxis).toHaveLength(2)
    const series = both.series as { yAxisIndex: number; type: string }[]
    expect(series[0].yAxisIndex).toBe(0)
    expect(series[1].yAxisIndex).toBe(1)
    expect(series[1].type).toBe('line')
  })

  it('maps the chart styles the old UI offered', () => {
    const option = minuteSeriesOption(
      spec([
        { name: 'a', points: [point(1, 1)], value: 'count', style: 'column' },
        { name: 'b', points: [point(1, 1)], value: 'count', style: 'line' },
        { name: 'c', points: [point(1, 1)], value: 'count', style: 'stackedColumn' },
      ]),
    )
    const series = option.series as { type: string; stack?: string }[]
    expect(series.map((s) => s.type)).toEqual(['bar', 'line', 'bar'])
    expect(series[2].stack).toBe('total')
    expect(series[0].stack).toBeUndefined()
  })

  it('rounds latency so a tooltip does not show sixteen digits', () => {
    const option = minuteSeriesOption(
      spec([{ name: 'ms', points: [point(1, 1, 33.33333)], value: 'avgMs', style: 'line' }]),
    )
    expect((option.series as { data: number[] }[])[0].data).toEqual([33.3])
  })
})

describe('axisTooltip', () => {
  const row = (seriesName: string, value: number | null) => ({
    axisValueLabel: '10:15',
    marker: '<span class="m"></span>',
    seriesName,
    value,
  })

  it('lists only the series the hovered minute holds, largest first', () => {
    const html = axisTooltip([
      row('acme', 3),
      row('globex', null),
      row('initech', 9),
      row('none', 0),
    ])
    expect(html).toContain('10:15')
    expect(html).not.toContain('globex')
    expect(html).not.toContain('none')
    expect(html.indexOf('initech')).toBeLessThan(html.indexOf('acme'))
  })

  it('does not let a name be read as markup', () => {
    expect(axisTooltip([row('<b>x</b>', 1)])).toContain('&lt;b&gt;x&lt;/b&gt;')
  })
})

describe('groupSeries', () => {
  const group: Group = {
    value: '/a',
    count: 3,
    avgMs: 10,
    points: [point(1, 3)],
    groups: [
      { value: 'anna', count: 2, avgMs: 10, points: [point(1, 2)] },
      { value: 'bob', count: 1, avgMs: 10, points: [point(1, 1)] },
    ],
  }

  it('names series by their label when the signal gives one', () => {
    const named: Group = { ...group, groups: [{ ...group.groups![0]!, label: 'Anna K.' }] }
    expect(groupSeries(named, 'count', 'column', 'stackedColumn').map((s) => s.name)).toEqual([
      'Anna K.',
    ])
    const plain: Group = { value: '/a', label: 'Home', count: 3, avgMs: 10, points: [point(1, 3)] }
    expect(groupSeries(plain, 'count', 'column', 'column')[0]?.name).toBe('Home')
  })

  it('draws one series per sub-group when there is one', () => {
    const got = groupSeries(group, 'count', 'column', 'stackedColumn')
    expect(got.map((s) => s.name)).toEqual(['anna', 'bob'])
    expect(got.every((s) => s.style === 'stackedColumn')).toBe(true)
  })

  it('adds what the drawn sub-groups leave out, so the bars add up to the group', () => {
    const topOnly: Group = {
      ...group,
      count: 10,
      points: [point(1, 10), point(2, 3)],
    }
    const got = groupSeries(topOnly, 'count', 'column', 'stackedColumn', 'All other')
    expect(got.map((s) => s.name)).toEqual(['anna', 'bob', 'All other'])
    expect(got[2].points.map((p) => [p.minute, p.count])).toEqual([
      [1, 7],
      [2, 3],
    ])
    expect(got[2].style).toBe('stackedColumn')
  })

  it('adds no rest series when the sub-groups already cover everything', () => {
    expect(groupSeries(group, 'count', 'column', 'stackedColumn', 'All other')).toHaveLength(2)
  })

  it('falls back to the group itself', () => {
    const got = groupSeries({ ...group, groups: undefined }, 'count', 'column', 'stackedColumn')
    expect(got).toHaveLength(1)
    expect(got[0].name).toBe('/a')
    expect(got[0].style).toBe('column')
  })
})

describe('allMinutes', () => {
  it('is the sorted union with no repeats', () => {
    expect(
      allMinutes([
        { name: 'a', points: [point(3, 1), point(1, 1)], value: 'count', style: 'column' },
        { name: 'b', points: [point(1, 1), point(2, 1)], value: 'count', style: 'column' },
      ]),
    ).toEqual([1, 2, 3])
  })
})

describe('subStyleOf', () => {
  it('stacks a second dimension unless told not to', () => {
    expect(subStyleOf({})).toBe('column')
    expect(subStyleOf({ sub: 'account' })).toBe('stackedColumn')
    expect(subStyleOf({ sub: 'account', stacked: true })).toBe('stackedColumn')
    expect(subStyleOf({ sub: 'account', stacked: false })).toBe('column')
  })
})

describe('showMainOf', () => {
  it('hides the main chart only when asked and a group is set', () => {
    expect(showMainOf({})).toBe(true)
    expect(showMainOf({ main: false })).toBe(true)
    expect(showMainOf({ group: 'port' })).toBe(true)
    expect(showMainOf({ group: 'port', main: false })).toBe(false)
  })
})

describe('togetherSeries', () => {
  it('stacks every group and sub-group in one chart, named group - sub', () => {
    const groups: Group[] = [
      {
        value: 'DOWNLOAD',
        count: 2,
        avgMs: 0,
        points: [point(1, 2)],
        groups: [{ value: 'COMPLETED', count: 2, avgMs: 0, points: [point(1, 2)] }],
      },
      { value: 'UPLOAD', count: 1, avgMs: 0, points: [point(1, 1)] },
    ]
    const got = togetherSeries(groups)
    expect(got.map((s) => s.name)).toEqual(['DOWNLOAD - COMPLETED', 'UPLOAD'])
    expect(got.every((s) => s.style === 'stackedColumn')).toBe(true)
  })
})

describe('colours by value', () => {
  it('draws a value in its colour whether it is a group or a sub-group', () => {
    const colors = { COMPLETED: '#2e7d32' }
    const groups: Group[] = [
      {
        value: 'DOWNLOAD',
        count: 3,
        avgMs: 0,
        points: [point(1, 3)],
        groups: [
          { value: 'COMPLETED', count: 2, avgMs: 0, points: [point(1, 2)] },
          { value: 'ACCEPTED', count: 1, avgMs: 0, points: [point(1, 1)] },
        ],
      },
      { value: 'COMPLETED', count: 1, avgMs: 0, points: [point(1, 1)] },
    ]
    expect(togetherSeries(groups, undefined, colors).map((s) => s.color)).toEqual([
      '#2e7d32',
      undefined,
      '#2e7d32',
    ])
    const option = minuteSeriesOption({
      series: togetherSeries(groups, undefined, colors),
      mode: 'light',
      formatMinute: String,
      countLabel: 'n',
      latencyLabel: 'ms',
    })
    const first = (option.series as { itemStyle?: { color?: string } }[])[0]
    expect(first?.itemStyle?.color).toBe('#2e7d32')
  })
})
