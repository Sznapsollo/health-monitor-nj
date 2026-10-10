import type { EChartsOption } from 'echarts'

import { echartsTheme } from '../theme/theme'
import type { ResolvedMode } from '../theme/tokens'
import type { Group, Point } from '../ws/types'

export type ValueKey = 'count' | 'avgMs'
export type SeriesStyle = 'column' | 'line' | 'stackedColumn' | 'area'

export interface SeriesSpec {
  name: string
  points: Point[]
  value: ValueKey
  style: SeriesStyle
  /** Draw on the second axis, which is how latency shares a chart with counts. */
  secondary?: boolean
  color?: string
}

export interface ChartSpec {
  series: SeriesSpec[]
  mode: ResolvedMode
  /** Minute -> label, so the browser owns the time zone. */
  formatMinute: (minute: number) => string
  showLegend?: boolean
  countLabel: string
  latencyLabel: string
}

/**
 * Builds the ECharts option for a per-minute chart. Kept a pure function of
 * its inputs so the chart can be tested without a DOM, and so the palette
 * always comes from the active theme rather than from anything hard-coded.
 */
export function minuteSeriesOption(spec: ChartSpec): EChartsOption {
  const theme = echartsTheme(spec.mode)
  const minutes = allMinutes(spec.series)
  const hasSecondary = spec.series.some((s) => s.secondary)

  return {
    color: theme.color,
    backgroundColor: theme.backgroundColor,
    textStyle: theme.textStyle,
    animation: false,
    grid: { left: 8, right: 8, top: spec.showLegend ? 32 : 12, bottom: 24, containLabel: true },
    tooltip: {
      trigger: 'axis',
      formatter: axisTooltip,
      appendTo: 'body',
      backgroundColor: theme.tooltip.backgroundColor,
      borderColor: theme.tooltip.borderColor,
      textStyle: theme.tooltip.textStyle,
    },
    legend: spec.showLegend
      ? { type: 'scroll', textStyle: theme.legend.textStyle, top: 0 }
      : undefined,
    xAxis: {
      type: 'category',
      data: minutes.map(spec.formatMinute),
      ...theme.categoryAxis,
      splitLine: { show: false },
    },
    yAxis: [
      { type: 'value', name: spec.countLabel, ...theme.valueAxis },
      ...(hasSecondary
        ? [
            {
              type: 'value' as const,
              name: spec.latencyLabel,
              ...theme.valueAxis,
              splitLine: { show: false },
            },
          ]
        : []),
    ],
    dataZoom: [{ type: 'inside' }],
    series: spec.series.map((s) => toSeries(s, minutes, hasSecondary)),
  }
}

function toSeries(s: SeriesSpec, minutes: number[], hasSecondary: boolean) {
  const byMinute = new Map(s.points.map((p) => [p.minute, p]))
  const data = minutes.map((m) => {
    const point = byMinute.get(m)
    if (!point) return null
    return s.value === 'count' ? point.count : round(point.avgMs)
  })

  const base = {
    name: s.name,
    data,
    yAxisIndex: s.secondary && hasSecondary ? 1 : 0,
    // A gap is a minute with no traffic, not a reason to join the line across.
    connectNulls: false,
    ...(s.color ? { itemStyle: { color: s.color }, lineStyle: { color: s.color } } : {}),
  }

  switch (s.style) {
    case 'line':
      return { ...base, type: 'line' as const, smooth: false, symbol: 'none' }
    case 'area':
      return { ...base, type: 'line' as const, smooth: false, symbol: 'none', areaStyle: {} }
    case 'stackedColumn':
      return { ...base, type: 'bar' as const, stack: 'total' }
    default:
      return { ...base, type: 'bar' as const }
  }
}

interface TooltipRow {
  axisValueLabel?: string
  marker?: unknown
  seriesName?: string
  value?: unknown
}

/**
 * Only what the hovered minute holds, largest first: a series with nothing in
 * that minute stays out, however much it had in the others.
 */
export function axisTooltip(params: unknown): string {
  const rows = (Array.isArray(params) ? params : [params]) as TooltipRow[]
  const held = rows
    .filter(
      (r): r is TooltipRow & { value: number } => typeof r.value === 'number' && r.value !== 0,
    )
    .sort((a, b) => b.value - a.value)
  const head = `<div>${escapeHtml(rows[0]?.axisValueLabel ?? '')}</div>`
  const lines = held.map(
    (r) =>
      `<div>${typeof r.marker === 'string' ? r.marker : ''}${escapeHtml(r.seriesName ?? '')}` +
      `<span style="float:right;margin-left:16px;font-weight:600">${r.value.toLocaleString()}</span></div>`,
  )
  return head + lines.join('')
}

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

/** The union of every series' minutes, ascending, so lines share an axis. */
export function allMinutes(series: SeriesSpec[]): number[] {
  const seen = new Set<number>()
  for (const s of series) {
    for (const p of s.points) seen.add(p.minute)
  }
  return [...seen].sort((a, b) => a - b)
}

export function showMainOf(c: { group?: string; main?: boolean }): boolean {
  return !c.group || c.main !== false
}

export function subStyleOf(c: { sub?: string; stacked?: boolean }): SeriesStyle {
  return c.sub && c.stacked !== false ? 'stackedColumn' : 'column'
}

export function groupName(g: Group): string {
  return g.label || g.value
}

/**
 * Series for one group's tile: the group itself, or its sub-groups plus, when
 * restName is given, what the top sub-groups leave out, so the bars add up to
 * the group's own count.
 */
export function groupSeries(
  group: Group,
  value: ValueKey,
  style: SeriesStyle,
  subStyle: SeriesStyle,
  restName?: string,
  colors?: Record<string, string>,
): SeriesSpec[] {
  if (group.groups && group.groups.length > 0) {
    const subs: SeriesSpec[] = group.groups.map((sub) => ({
      name: groupName(sub),
      points: sub.points,
      value,
      style: subStyle,
      ...colorOf(sub, colors),
    }))
    const rest = restName && value === 'count' ? restPoints(group) : []
    return rest.length > 0
      ? [...subs, { name: restName ?? '', points: rest, value, style: subStyle }]
      : subs
  }
  return [{ name: groupName(group), points: group.points, value, style, ...colorOf(group, colors) }]
}

function colorOf(g: Group, colors?: Record<string, string>): { color?: string } {
  const color = colors?.[g.value] ?? (g.label ? colors?.[g.label] : undefined)
  return color ? { color } : {}
}

/**
 * Every group in one stacked chart: one series per group, or per group and
 * sub-group, named "group - sub".
 */
export function togetherSeries(
  groups: Group[],
  restName?: string,
  colors?: Record<string, string>,
): SeriesSpec[] {
  return groups.flatMap((group) => {
    const series = groupSeries(group, 'count', 'stackedColumn', 'stackedColumn', restName, colors)
    if (!group.groups?.length) return series
    const name = groupName(group)
    return series.map((s) => ({ ...s, name: `${name} - ${s.name}` }))
  })
}

function restPoints(group: Group): Point[] {
  const drawn = new Map<number, number>()
  for (const sub of group.groups ?? []) {
    for (const p of sub.points) drawn.set(p.minute, (drawn.get(p.minute) ?? 0) + p.count)
  }
  return group.points
    .map((p) => ({
      minute: p.minute,
      count: p.count - (drawn.get(p.minute) ?? 0),
      avgMs: 0,
      minMs: 0,
      maxMs: 0,
    }))
    .filter((p) => p.count > 0)
}

function round(v: number): number {
  return Math.round(v * 10) / 10
}
