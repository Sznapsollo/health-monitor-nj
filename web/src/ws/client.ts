import type { ClientMessage, ServerMessage } from './types'

export type ConnectionState = 'connecting' | 'open' | 'closed'

export interface ClientOptions {
  url?: string
  onMessage: (msg: ServerMessage) => void
  onState: (state: ConnectionState) => void
  /** Injected in tests; defaults to the browser's WebSocket. */
  create?: (url: string) => WebSocket
  /** Injected in tests so back-off does not make them slow. */
  setTimeout?: (fn: () => void, ms: number) => number
  clearTimeout?: (handle: number) => void
}

const PING_INTERVAL_MS = 30_000
const SILENT_PINGS_MAX = 2
const BACKOFF_START_MS = 500
const BACKOFF_MAX_MS = 30_000

function defaultUrl(): string {
  const protocol = globalThis.location?.protocol === 'https:' ? 'wss:' : 'ws:'
  // A wall display authenticates with a token in the URL, because it has no
  // cookie and nobody to type a password.
  const token = new URLSearchParams(globalThis.location?.search ?? '').get('token')
  const query = token ? `?token=${encodeURIComponent(token)}` : ''
  return `${protocol}//${globalThis.location?.host ?? 'localhost'}/ws${query}`
}

/**
 * One socket per tab, reconnecting with exponential back-off. A wall display
 * is expected to hold this open for weeks, so every failure path
 * has to end in another attempt rather than a dead page.
 */
export class HmSocket {
  private socket: WebSocket | null = null
  private closed = false
  private backoff = BACKOFF_START_MS
  private pingHandle: number | null = null
  private silentPings = 0
  private retryHandle: number | null = null
  private queued: ClientMessage[] = []
  // Replayed on every open: each socket is a new session to the server.
  private identity: ClientMessage | null = null

  private readonly create: (url: string) => WebSocket
  private readonly setTimer: (fn: () => void, ms: number) => number
  private readonly clearTimer: (handle: number) => void

  constructor(private readonly options: ClientOptions) {
    this.create = options.create ?? ((url) => new WebSocket(url))
    this.setTimer =
      options.setTimeout ?? ((fn, ms) => globalThis.setTimeout(fn, ms) as unknown as number)
    this.clearTimer = options.clearTimeout ?? ((h) => globalThis.clearTimeout(h))
  }

  connect(): void {
    if (this.closed) return
    this.options.onState('connecting')
    const socket = this.create(this.options.url ?? defaultUrl())
    this.socket = socket

    socket.onopen = () => {
      this.backoff = BACKOFF_START_MS
      this.options.onState('open')
      const queued = this.queued.filter((m) => m.t !== 'hello' && m.t !== 'criteria')
      this.queued = []
      if (this.identity) this.send(this.identity)
      for (const msg of queued) this.send(msg)
      this.silentPings = 0
      this.startPing()
    }
    socket.onmessage = (event: MessageEvent) => {
      this.silentPings = 0
      let msg: ServerMessage
      try {
        msg = JSON.parse(String(event.data)) as ServerMessage
      } catch {
        // A message we cannot parse is not worth killing the session over.
        return
      }
      // Deliberately not wrapped: an error applying a message used to be
      // swallowed here, which left the socket "Live", the page frozen and
      // the console empty — the hardest possible thing to diagnose.
      this.options.onMessage(msg)
    }
    socket.onclose = () => this.retry()
    socket.onerror = () => socket.close()
  }

  /** send delivers now if the socket is open, otherwise on the next open. */
  send(msg: ClientMessage): void {
    if (msg.t === 'hello' || msg.t === 'criteria') {
      msg = { ...msg, batch: true }
      this.identity = { ...this.identity, ...msg, t: 'hello' }
    }
    if (this.socket && this.socket.readyState === WebSocket.OPEN) {
      this.socket.send(JSON.stringify(msg))
      return
    }
    // Only the latest criteria matter; a queue of stale ones helps nobody.
    this.queued = [...this.queued.filter((m) => m.t !== msg.t), msg]
  }

  close(): void {
    this.closed = true
    this.stopPing()
    if (this.retryHandle !== null) this.clearTimer(this.retryHandle)
    this.socket?.close()
    this.options.onState('closed')
  }

  private retry(): void {
    this.stopPing()
    this.socket = null
    if (this.closed) return
    this.options.onState('closed')
    const wait = this.backoff + Math.floor(Math.random() * this.backoff * 0.25)
    this.backoff = Math.min(this.backoff * 2, BACKOFF_MAX_MS)
    this.retryHandle = this.setTimer(() => this.connect(), wait)
  }

  private startPing(): void {
    this.stopPing()
    this.pingHandle = this.setTimer(() => {
      this.pingHandle = null
      if (this.silentPings >= SILENT_PINGS_MAX) {
        this.abandon()
        return
      }
      this.silentPings++
      this.send({ t: 'ping' })
      this.startPing()
    }, PING_INTERVAL_MS)
  }

  // A half-open socket may never fire onclose, so this one is cut loose before closing.
  private abandon(): void {
    const socket = this.socket
    if (!socket) return
    socket.onopen = null
    socket.onmessage = null
    socket.onclose = null
    socket.onerror = null
    socket.close()
    this.retry()
  }

  private stopPing(): void {
    if (this.pingHandle !== null) {
      this.clearTimer(this.pingHandle)
      this.pingHandle = null
    }
  }
}
