import { useMemo, useState } from 'react'

import type { Group } from '../ws/types'
import { groupSeries, type SeriesSpec, type SeriesStyle } from './options'

export interface GroupTile {
  group: Group
  series: SeriesSpec[]
}

interface Built {
  colors?: Record<string, string>
  subStyle: SeriesStyle
  restName: string
  series: SeriesSpec[]
}

/**
 * The series of each group tile. A group the last update left unchanged keeps
 * its series object, so its chart is not redrawn.
 */
export function useGroupTiles(
  groups: Group[],
  subStyle: SeriesStyle,
  restName: string,
  colors?: Record<string, string>,
): GroupTile[] {
  const [built] = useState(() => new WeakMap<Group, Built>())
  return useMemo(
    () =>
      groups.map((group) => {
        const have = built.get(group)
        if (
          have &&
          have.subStyle === subStyle &&
          have.restName === restName &&
          have.colors === colors
        ) {
          return { group, series: have.series }
        }
        const series = groupSeries(group, 'count', 'column', subStyle, restName, colors)
        built.set(group, { subStyle, restName, colors, series })
        return { group, series }
      }),
    [groups, built, subStyle, restName, colors],
  )
}
