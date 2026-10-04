import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useMonitorStore } from './useMonitorStore'

/** A socket that records what happened to it. */
class RecordingSocket {
  static opened = 0
  static closed = 0
  readyState = 0
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((e: MessageEvent) => void) | null = null

  constructor() {
    RecordingSocket.opened += 1
  }
  send = vi.fn()
  close = vi.fn(() => {
    RecordingSocket.closed += 1
  })
}

describe('connect and disconnect', () => {
  beforeEach(() => {
    RecordingSocket.opened = 0
    RecordingSocket.closed = 0
    vi.stubGlobal('WebSocket', RecordingSocket)
    vi.useFakeTimers()
  })

  it('survives StrictMode mounting twice without dropping the socket', () => {
    const { connect, disconnect } = useMonitorStore.getState()

    // What React does in development: mount, unmount, mount again.
    connect()
    disconnect()
    connect()
    vi.advanceTimersByTime(1000)

    expect(RecordingSocket.opened).toBe(1)
    expect(RecordingSocket.closed).toBe(0)

    // The real unmount closes it.
    disconnect()
    vi.advanceTimersByTime(1000)
    expect(RecordingSocket.closed).toBe(1)

    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('keeps one socket for several viewers of the same page', () => {
    const { connect, disconnect } = useMonitorStore.getState()
    connect()
    connect()
    vi.advanceTimersByTime(1000)
    expect(RecordingSocket.opened).toBe(1)

    // One of them going away leaves the other connected.
    disconnect()
    vi.advanceTimersByTime(1000)
    expect(RecordingSocket.closed).toBe(0)

    disconnect()
    vi.advanceTimersByTime(1000)
    expect(RecordingSocket.closed).toBe(1)

    vi.useRealTimers()
    vi.unstubAllGlobals()
  })
})
