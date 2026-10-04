export function isBranch(value: unknown): value is Record<string, unknown> | unknown[] {
  return typeof value === 'object' && value !== null
}

export function entriesOf(value: Record<string, unknown> | unknown[]): [string, unknown][] {
  return Array.isArray(value) ? value.map((v, i) => [String(i), v]) : Object.entries(value)
}

export function contains(name: string | null, value: unknown, needle: string): boolean {
  if (!needle) return true
  if (name?.toLowerCase().includes(needle)) return true
  if (isBranch(value)) return entriesOf(value).some(([k, v]) => contains(k, v, needle))
  return String(value).toLowerCase().includes(needle)
}

/** A number that reads as milliseconds since 1970 in this century, shown as a local time. */
export function epochOf(value: unknown): string | null {
  if (typeof value !== 'number' || !Number.isInteger(value)) return null
  if (value < 946_684_800_000 || value > 4_102_444_800_000) return null
  return new Date(value).toLocaleString()
}
