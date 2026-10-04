import { afterEach, describe, expect, it, vi } from 'vitest'

import { everyVisible } from './everyVisible'

function setHidden(hidden: boolean) {
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden })
  document.dispatchEvent(new Event('visibilitychange'))
}

describe('everyVisible', () => {
  afterEach(() => {
    setHidden(false)
    vi.useRealTimers()
  })

  it('polls while visible, pauses while hidden and catches up on return', () => {
    vi.useFakeTimers()
    const fn = vi.fn()
    const stop = everyVisible(fn, 1000)

    vi.advanceTimersByTime(2000)
    expect(fn).toHaveBeenCalledTimes(2)

    setHidden(true)
    vi.advanceTimersByTime(5000)
    expect(fn).toHaveBeenCalledTimes(2)

    setHidden(false)
    expect(fn).toHaveBeenCalledTimes(3)

    stop()
    vi.advanceTimersByTime(5000)
    setHidden(true)
    setHidden(false)
    expect(fn).toHaveBeenCalledTimes(3)
  })
})
