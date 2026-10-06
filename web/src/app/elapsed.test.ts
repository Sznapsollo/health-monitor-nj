import { describe, expect, it } from 'vitest'

import { formatElapsed } from './elapsed'

describe('formatElapsed', () => {
  it('shows seconds only under a minute', () => {
    expect(formatElapsed(0)).toBe('0s')
    expect(formatElapsed(59)).toBe('59s')
  })

  it('shows minutes and seconds under an hour', () => {
    expect(formatElapsed(60)).toBe('1m 0s')
    expect(formatElapsed(3599)).toBe('59m 59s')
  })

  it('shows hours, minutes and seconds from an hour up', () => {
    expect(formatElapsed(3600)).toBe('1h 0m 0s')
    expect(formatElapsed(90061)).toBe('25h 1m 1s')
  })

  it('rounds fractions and clamps negatives', () => {
    expect(formatElapsed(59.6)).toBe('1m 0s')
    expect(formatElapsed(-5)).toBe('0s')
  })
})
