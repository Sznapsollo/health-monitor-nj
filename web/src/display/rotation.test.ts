import { describe, expect, it } from 'vitest'

import { MIN_ROTATE_SECONDS, rotationSeconds } from './rotation'

describe('rotationSeconds', () => {
  it('takes a plain number of seconds', () => {
    expect(rotationSeconds('30')).toBe(30)
  })

  it('does not rotate without a usable number', () => {
    for (const raw of [null, '', 'abc', '0', '-5', 'Infinity', 'NaN']) {
      expect(rotationSeconds(raw)).toBe(0)
    }
  })

  it('never rotates faster than the minimum', () => {
    expect(rotationSeconds('0.001')).toBe(MIN_ROTATE_SECONDS)
    expect(rotationSeconds('1')).toBe(MIN_ROTATE_SECONDS)
  })
})
