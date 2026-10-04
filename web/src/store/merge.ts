import type { Group, Point, SignalView } from '../ws/types'

/**
 * Merges one changed minute into the view a tab is already showing. The server
 * sends only what changed, so this is where the chart's history actually
 * lives — and where it is trimmed, so a tab left open for a week shows the
 * same thing as a fresh one.
 *
 * Anything the update leaves as it was keeps its identity, so a chart whose
 * data did not change is not redrawn.
 */
export function mergeView(current: SignalView | undefined, update: SignalView): SignalView {
  if (!current) return update

  const total = mergePoints(current.total, update.total, update.from)
  const groups = mergeGroups(current.groups, update.groups, update.groupsFrom ?? update.from)
  if (
    total === current.total &&
    groups === current.groups &&
    current.from === update.from &&
    current.to === update.to &&
    current.detailFrom === update.detailFrom &&
    current.partial === update.partial &&
    current.group === update.group &&
    current.sub === update.sub &&
    current.groupsFrom === update.groupsFrom &&
    sameCounts(current.capped, update.capped)
  ) {
    return current
  }
  return {
    ...current,
    from: update.from,
    to: update.to,
    detailFrom: update.detailFrom,
    capped: update.capped,
    partial: update.partial,
    group: update.group,
    sub: update.sub,
    groupsFrom: update.groupsFrom,
    total,
    groups,
  }
}

/**
 * Upserts by minute, keeps ascending order, drops anything before `from`.
 * Both sides are treated as possibly absent: a server that marshals an empty
 * list as null must not be able to freeze the page. The current array comes
 * back unchanged when the update adds nothing new.
 */
export function mergePoints(
  current: Point[] | null | undefined,
  update: Point[] | null | undefined,
  from: number,
): Point[] {
  const have = current ?? []
  let start = 0
  while (start < have.length && have[start]!.minute < from) start++
  const incoming = (update ?? []).filter((p) => p.minute >= from)
  if (incoming.length === 0) return start === 0 ? have : have.slice(start)

  const last = have.length > start ? have[have.length - 1]!.minute : -Infinity
  if (
    incoming.every((p, i) => p.minute >= last && (i === 0 || p.minute > incoming[i - 1]!.minute))
  ) {
    const first = incoming[0]!
    if (
      first.minute === last &&
      incoming.length === 1 &&
      start === 0 &&
      samePoint(have[have.length - 1]!, first)
    ) {
      return have
    }
    const out = have.slice(start)
    for (const p of incoming) {
      if (out.length > 0 && out[out.length - 1]!.minute === p.minute) out[out.length - 1] = p
      else out.push(p)
    }
    return out
  }

  const byMinute = new Map<number, Point>()
  for (const p of have) {
    if (p.minute >= from) byMinute.set(p.minute, p)
  }
  for (const p of incoming) byMinute.set(p.minute, p)
  return [...byMinute.values()].sort((a, b) => a.minute - b.minute)
}

function mergeGroups(
  current: Group[] | null | undefined,
  update: Group[] | null | undefined,
  from: number,
): Group[] | undefined {
  if (!update) return current ?? undefined
  if (!current) return update

  const byValue = new Map(current.map((g) => [g.value, g]))
  let unchanged = current.length === update.length
  const out: Group[] = []
  for (const [i, g] of update.entries()) {
    const have = byValue.get(g.value)
    if (!have) {
      unchanged = false
      out.push(g)
      continue
    }
    const points = mergePoints(have.points, g.points, from)
    const groups = mergeGroups(have.groups, g.groups, from)
    if (
      points === have.points &&
      groups === have.groups &&
      have.count === g.count &&
      have.avgMs === g.avgMs &&
      have.label === g.label
    ) {
      out.push(have)
      if (current[i] !== have) unchanged = false
    } else {
      unchanged = false
      out.push({ ...g, points, groups })
    }
  }
  // The server ranks the groups over the whole window, so its order is the
  // truth and a value it no longer lists has dropped out of the top N.
  return unchanged ? current : out
}

function samePoint(a: Point, b: Point): boolean {
  return a.count === b.count && a.avgMs === b.avgMs && a.minMs === b.minMs && a.maxMs === b.maxMs
}

function sameCounts(a?: Record<string, number>, b?: Record<string, number>): boolean {
  if (a === b) return true
  if (!a || !b) return false
  const keys = Object.keys(a)
  return keys.length === Object.keys(b).length && keys.every((k) => a[k] === b[k])
}
