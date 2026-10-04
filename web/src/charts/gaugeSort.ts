import type { SignalSpec } from '../api/catalogue'
import type { GaugePoint } from './BarGauge'

export const GAUGE_SORTS = ['label', 'labelDesc', 'value', 'valueDesc'] as const
export type GaugeSort = (typeof GAUGE_SORTS)[number]

const isGaugeSort = (v: unknown): v is GaugeSort => GAUGE_SORTS.includes(v as GaugeSort)

/** A panel's own order wins over the signal's barGauge view option; label A→Z otherwise. */
export function gaugeSortOf(spec?: SignalSpec, override?: string): GaugeSort {
  if (isGaugeSort(override)) return override
  const option = spec?.views?.find((v) => v.type === 'barGauge')?.options?.sort
  return isGaugeSort(option) ? option : 'label'
}

export function sortPoints(points: GaugePoint[], sort: GaugeSort): GaugePoint[] {
  const byLabel = (a: GaugePoint, b: GaugePoint) => a.label.localeCompare(b.label)
  const compare = {
    label: byLabel,
    labelDesc: (a: GaugePoint, b: GaugePoint) => byLabel(b, a),
    value: (a: GaugePoint, b: GaugePoint) => a.value - b.value || byLabel(a, b),
    valueDesc: (a: GaugePoint, b: GaugePoint) => b.value - a.value || byLabel(a, b),
  }[sort]
  return [...points].sort(compare)
}
