import type { TFunction } from 'i18next'

import type { Alert } from '../api/alerts'

const MATCH_PREFIX = 'match:contains="'
const CATEGORY_PREFIX = 'category:'

export function matchTarget(text: string): string {
  return `${MATCH_PREFIX}${text.replace(/"/g, '')}"`
}

export function categoryTarget(category: string): string {
  return `${CATEGORY_PREFIX}${category}`
}

/** The message text the bell starts from; a slow request is trimmed to its URL. */
export function silenceMessageOf(a: Alert): string {
  const url = typeof a.data?.url === 'string' ? a.data.url : ''
  return a.category === 'latency' && url ? `${url} took ` : a.message
}

/** The text a message silence looks for, or null for any other target. */
export function messageOf(target: string): string | null {
  return target.startsWith(MATCH_PREFIX) && target.endsWith('"')
    ? target.slice(MATCH_PREFIX.length, -1)
    : null
}

/** The category a category silence covers, or null for any other target. */
export function categoryOf(target: string): string | null {
  return target.startsWith(CATEGORY_PREFIX) ? target.slice(CATEGORY_PREFIX.length) : null
}

/** A silence's target in words, for the Maintenance list. */
export function describeTarget(target: string, t: TFunction): string {
  const text = messageOf(target)
  if (text !== null) return t('silence.targets.match', { text })
  const category = categoryOf(target)
  if (category !== null) return t('silence.targets.category', { category })
  return target
}
