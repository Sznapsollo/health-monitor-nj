/**
 * setInterval for polling: ticks are skipped while the tab is hidden, and one
 * runs as soon as it is shown again, so a background tab costs the server
 * nothing and a returning viewer is not shown stale numbers.
 */
export function everyVisible(fn: () => void, ms: number): () => void {
  const hidden = () => typeof document !== 'undefined' && document.hidden
  const handle = globalThis.setInterval(() => {
    if (!hidden()) fn()
  }, ms)
  const onChange = () => {
    if (!hidden()) fn()
  }
  document.addEventListener('visibilitychange', onChange)
  return () => {
    globalThis.clearInterval(handle)
    document.removeEventListener('visibilitychange', onChange)
  }
}
