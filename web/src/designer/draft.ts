import type { SignalSpec } from '../api/catalogue'
import type { Candidate, SignalEdit } from '../api/signals'
import type { Point } from '../ws/types'

export interface DimDraft {
  name: string
  displayName: string
  labelDim: string
  include: boolean
  samples: string[]
}

export interface SignalDraft {
  platform: string
  name: string
  isNew: boolean
  kind: 'timeseries' | 'gauge' | 'log' | 'info'
  displayName: string
  dims: DimDraft[]
  countField: string
  msField: string
  hotDetailMinutes: number
  hotTotalsMinutes: number
  durableDays: number
  detailDays: number
  keepPairs: [string, string][]
  maxKeys: number
  ttlSeconds: number
  groupTop: number
  /** A log signal's days; '' leaves them to the configuration. */
  logDays: string
  archiveDays: string
  packetType: string
  versions: number
  merge: boolean
  noStatus: boolean
  colors: [string, string][]
}

const NAME = /^[A-Za-z][A-Za-z0-9_.-]{0,63}$/
const COLOR = /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/
const MS_FIELDS = ['ms', 'executionTime', 'latencyMs', 'durationMs']
const PACKET_FIELDS = ['type', 'start']

const defaults = {
  hotDetailMinutes: 120,
  hotTotalsMinutes: 2880,
  durableDays: 30,
  detailDays: 3,
  keepPairs: [] as [string, string][],
  maxKeys: 10000,
  ttlSeconds: 0,
  groupTop: 50,
  logDays: '',
  archiveDays: '',
  packetType: '',
  versions: 20,
  merge: false,
  noStatus: false,
  colors: [] as [string, string][],
}

export function draftFromCandidate(c: Candidate): SignalDraft {
  if (c.kind === 'log') {
    return { ...blankDraft(c.platform), name: c.signal, kind: 'log', displayName: c.signal }
  }
  if (c.kind === 'info') {
    return {
      ...blankDraft(c.platform),
      name: c.signal,
      kind: 'info',
      displayName: c.signal,
      packetType: c.signal,
      dims: Object.keys(c.dims)
        .filter((name) => (c.dims[name] ?? []).length > 0)
        .sort()
        .map((name) => ({
          name,
          displayName: name,
          labelDim: `${name}Name` in c.dims ? `${name}Name` : '',
          include: !PACKET_FIELDS.includes(name) && !(name in c.values),
          samples: c.dims[name] ?? [],
        })),
      countField: '',
      msField: MS_FIELDS.find((f) => f in c.values) ?? '',
    }
  }
  const gauge = c.kind === 'gauge'
  return {
    ...defaults,
    platform: c.platform,
    name: c.signal,
    isNew: true,
    kind: gauge ? 'gauge' : 'timeseries',
    displayName: c.signal,
    dims: gauge
      ? []
      : Object.keys(c.dims)
          .sort()
          .map((name) => ({
            name,
            displayName: name,
            labelDim: `${name}Name` in c.dims ? `${name}Name` : '',
            include: true,
            samples: c.dims[name] ?? [],
          })),
    countField: !gauge && 'count' in c.values ? 'count' : '',
    msField: gauge ? '' : (MS_FIELDS.find((f) => f in c.values) ?? ''),
  }
}

export function blankDraft(platform: string): SignalDraft {
  return {
    ...defaults,
    platform,
    name: '',
    isNew: true,
    kind: 'timeseries',
    displayName: '',
    dims: [],
    countField: 'count',
    msField: '',
  }
}

export function draftFromSpec(platform: string, spec: SignalSpec): SignalDraft {
  return {
    platform,
    name: spec.name,
    isNew: false,
    kind:
      spec.kind === 'gauge' || spec.kind === 'log' || spec.kind === 'info'
        ? spec.kind
        : 'timeseries',
    displayName: spec.displayName,
    dims: spec.dims.map((d) => ({
      name: d.name,
      displayName: d.displayName,
      labelDim: d.labelDim ?? '',
      include: true,
      samples: [],
    })),
    countField: spec.values?.count ?? '',
    msField: spec.values?.ms ?? '',
    hotDetailMinutes: spec.retention.hotDetailMinutes,
    hotTotalsMinutes: spec.retention.hotTotalsMinutes,
    durableDays: spec.retention.durableDays,
    detailDays: spec.retention.detailDays ?? defaults.detailDays,
    keepPairs: spec.keepPairs ?? [],
    maxKeys: spec.maxKeys ?? defaults.maxKeys,
    ttlSeconds: spec.ttlSeconds ?? 0,
    groupTop: spec.views?.find((v) => v.top)?.top ?? defaults.groupTop,
    logDays: spec.retention.logDays ? String(spec.retention.logDays) : '',
    archiveDays: spec.retention.archiveDays !== undefined ? String(spec.retention.archiveDays) : '',
    packetType: spec.packetType ?? '',
    versions: spec.retention.versions ?? defaults.versions,
    merge: spec.merge ?? false,
    noStatus: spec.noStatus ?? false,
    colors: Object.entries(spec.colors ?? {}),
  }
}

export function editOf(d: SignalDraft): SignalEdit {
  const kept = d.dims.filter((dim) => dim.include && dim.name.trim())
  const names = new Set(kept.map((dim) => dim.name.trim()))
  return {
    kind: d.kind,
    displayName: d.displayName.trim() || d.name,
    dims: kept.map((dim) => {
      const name = dim.name.trim()
      const labelDim = dim.labelDim !== name && names.has(dim.labelDim) ? dim.labelDim : ''
      return {
        name,
        displayName: dim.displayName.trim() || name,
        ...(labelDim ? { labelDim } : {}),
      }
    }),
    values: { count: d.countField, ms: d.msField },
    retention: {
      hotDetailMinutes: d.hotDetailMinutes,
      hotTotalsMinutes: d.hotTotalsMinutes,
      durableDays: d.durableDays,
      detailDays: d.detailDays,
      ...(d.logDays.trim() !== '' ? { logDays: Number(d.logDays) } : {}),
      ...(d.archiveDays.trim() !== '' ? { archiveDays: Number(d.archiveDays) } : {}),
      ...(d.kind === 'info' ? { versions: d.versions } : {}),
    },
    ...(d.kind === 'info'
      ? { packetType: d.packetType.trim(), merge: d.merge, noStatus: d.noStatus }
      : {}),
    ...(d.kind === 'timeseries' && d.packetType.trim() ? { packetType: d.packetType.trim() } : {}),
    ...(d.kind === 'timeseries' && d.colors.length > 0
      ? { colors: Object.fromEntries(d.colors.map(([value, color]) => [value.trim(), color])) }
      : {}),
    keepPairs: d.keepPairs.filter(
      ([a, b]) =>
        a !== b && [a, b].every((n) => d.dims.some((dim) => dim.include && dim.name === n)),
    ),
    maxKeys: d.maxKeys,
    ttlSeconds: d.ttlSeconds,
    groupTop: d.groupTop,
  }
}

/** An i18n key naming what is wrong, or null when the draft can be saved. */
export function problemOf(d: SignalDraft): string | null {
  if (!NAME.test(d.name)) return 'designer.badName'
  if (d.kind === 'info') {
    if (!d.packetType.trim()) return 'designer.noPacketType'
    return d.versions >= 1 && d.durableDays >= 1 ? null : 'designer.badInfoRetention'
  }
  if (d.kind === 'log') {
    const days = [d.logDays, d.archiveDays].filter((v) => v.trim() !== '')
    return days.every((v) => /^\d+$/.test(v.trim())) ? null : 'designer.badLogDays'
  }
  const names = d.dims.filter((dim) => dim.include).map((dim) => dim.name.trim())
  if (names.some((n) => !n)) return 'designer.emptyDim'
  if (new Set(names).size !== names.length) return 'designer.duplicateDim'
  if (d.hotTotalsMinutes < d.hotDetailMinutes) return 'designer.badRetention'
  if (d.colors.some(([value, color]) => !value.trim() || !COLOR.test(color)))
    return 'designer.badColor'
  return null
}

/** What the main chart would have drawn from the quarantined packets. */
export function previewPoints(c: Candidate, countField: string, msField: string): Point[] {
  return c.minutes.map((m) => ({
    minute: m.minute,
    count: countField ? (m.sums[countField] ?? 0) : m.packets,
    avgMs: msField && m.packets ? (m.sums[msField] ?? 0) / m.packets : 0,
    minMs: 0,
    maxMs: 0,
  }))
}
