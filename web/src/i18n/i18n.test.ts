import { describe, expect, it } from 'vitest'

import en from './en.json'
import pl from './pl.json'

/** Plural suffixes i18next appends; a locale may have more of them than English. */
const PLURAL_SUFFIXES = ['_zero', '_one', '_two', '_few', '_many', '_other']

function keys(obj: unknown, prefix = ''): string[] {
  if (typeof obj !== 'object' || obj === null) return [prefix]
  return Object.entries(obj).flatMap(([k, v]) => keys(v, prefix ? `${prefix}.${k}` : k))
}

function baseKeys(source: unknown): Set<string> {
  return new Set(
    keys(source).map((k) => {
      const suffix = PLURAL_SUFFIXES.find((s) => k.endsWith(s))
      return suffix ? k.slice(0, -suffix.length) : k
    }),
  )
}

describe('locales', () => {
  it('Polish covers every English key', () => {
    const missing = [...baseKeys(en)].filter((k) => !baseKeys(pl).has(k))
    expect(missing).toEqual([])
  })

  it('English covers every Polish key', () => {
    const extra = [...baseKeys(pl)].filter((k) => !baseKeys(en).has(k))
    expect(extra).toEqual([])
  })

  it('has no empty strings', () => {
    for (const [name, locale] of Object.entries({ en, pl })) {
      const empties = keys(locale).filter((k) => {
        const value = k.split('.').reduce<unknown>((acc, part) => {
          return typeof acc === 'object' && acc !== null
            ? (acc as Record<string, unknown>)[part]
            : undefined
        }, locale)
        return typeof value === 'string' && value.trim() === ''
      })
      expect(empties, name).toEqual([])
    }
  })
})
