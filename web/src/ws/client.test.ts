import { describe, expect, it, vi } from 'vitest'

import { HmSocket } from './client'
import type { ClientMessage } from './types'

class FakeSocket {
  static readonly OPEN = 1
  readyState = 0
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((e: MessageEvent) => void) | null = null
  sent: ClientMessage[] = []
  send = (raw: string) => {
    this.sent.push(JSON.parse(raw) as ClientMessage)
  }
  close = vi.fn()
  open() {
    this.readyState = 1
    this.onopen?.()
  }
  drop() {
    this.readyState = 3
    this.onclose?.()
  }
}

function harness() {
  const sockets: FakeSocket[] = []
  const timers: { fn: () => void; ms: number }[] = []
  const client = new HmSocket({
    url: 'ws://test/ws',
    onMessage: () => {},
    onState: () => {},
    create: () => {
      const s = new FakeSocket()
      sockets.push(s)
      return s as unknown as WebSocket
    },
    setTimeout: (fn, ms) => timers.push({ fn, ms }) - 1,
    clearTimeout: () => {},
  })
  vi.stubGlobal('WebSocket', FakeSocket)
  const reconnect = () => {
    const short = timers.filter((t) => t.ms < 30_000)
    timers.length = 0
    short.forEach((t) => t.fn())
  }
  return { client, sockets, timers, reconnect }
}

describe('HmSocket', () => {
  it('tells a reconnected server who this is and what it wants', () => {
    const { client, sockets, reconnect } = harness()
    client.connect()
    client.send({ t: 'hello', name: 'anna', platform: 'p', criteria: [{ signal: 'requests' }] })
    sockets[0]!.open()
    expect(sockets[0]!.sent.map((m) => m.t)).toEqual(['hello'])

    client.send({ t: 'criteria', platform: 'p', criteria: [{ signal: 'jobs' }] })

    sockets[0]!.drop()
    reconnect()
    expect(sockets).toHaveLength(2)
    sockets[1]!.open()

    expect(sockets[1]!.sent).toEqual([
      { t: 'hello', name: 'anna', platform: 'p', criteria: [{ signal: 'jobs' }], batch: true },
    ])
    vi.unstubAllGlobals()
  })

  it('says hello first when criteria changed while it was down', () => {
    const { client, sockets, reconnect } = harness()
    client.connect()
    client.send({ t: 'hello', name: 'anna', platform: 'p', criteria: [], tab: 't1' })
    sockets[0]!.open()
    sockets[0]!.drop()
    client.send({ t: 'criteria', platform: 'p', criteria: [{ signal: 'requests' }] })
    reconnect()
    sockets[1]!.open()
    expect(sockets[1]!.sent).toEqual([
      {
        t: 'hello',
        name: 'anna',
        platform: 'p',
        criteria: [{ signal: 'requests' }],
        tab: 't1',
        batch: true,
      },
    ])
    vi.unstubAllGlobals()
  })

  it('gives up on a socket that stops answering and reconnects', () => {
    const { client, sockets, timers, reconnect } = harness()
    client.connect()
    sockets[0]!.open()
    const tick = () => {
      const ping = timers.findIndex((t) => t.ms === 30_000)
      timers.splice(ping, 1)[0]!.fn()
    }
    tick()
    tick()
    expect(sockets[0]!.sent.map((m) => m.t)).toEqual(['ping', 'ping'])
    expect(sockets[0]!.close).not.toHaveBeenCalled()
    tick()
    expect(sockets[0]!.close).toHaveBeenCalled()
    reconnect()
    expect(sockets).toHaveLength(2)
    vi.unstubAllGlobals()
  })

  it('keeps a socket that answers its pings', () => {
    const { client, sockets, timers } = harness()
    client.connect()
    sockets[0]!.open()
    for (let i = 0; i < 5; i++) {
      const ping = timers.findIndex((t) => t.ms === 30_000)
      timers.splice(ping, 1)[0]!.fn()
      sockets[0]!.onmessage?.({ data: '{"t":"pong"}' } as MessageEvent)
    }
    expect(sockets[0]!.close).not.toHaveBeenCalled()
    expect(sockets).toHaveLength(1)
    vi.unstubAllGlobals()
  })
})
