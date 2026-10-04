export function displayToken(): string | null {
  return new URLSearchParams(globalThis.location?.search ?? '').get('token')
}

// A wall display has no cookie; its token travels as a bearer header.
export function apiFetch(input: string, init?: RequestInit): Promise<Response> {
  const token = displayToken()
  if (!token) return globalThis.fetch(input, init)
  const headers = new Headers(init?.headers)
  headers.set('Authorization', `Bearer ${token}`)
  return globalThis.fetch(input, { ...init, headers })
}

/**
 * Reads a JSON answer, or says plainly why there is none. An endpoint the
 * server does not know falls through to the SPA, which answers 200 with
 * index.html — a bare JSON.parse then blames the syntax rather than the
 * stale build that caused it.
 */
export async function readJSON<T>(what: string, res: Response): Promise<T> {
  if (!res.ok) throw new Error(`${what} responded ${res.status}`)
  // Only a declared type that is not JSON is wrong; a response that says
  // nothing about itself is parsed as it always was.
  const type = res.headers?.get('content-type') ?? ''
  if (type !== '' && !type.includes('json')) {
    throw new Error(
      `${what} answered with a page, not data — the server is probably an older build`,
    )
  }
  return (await res.json()) as T
}
