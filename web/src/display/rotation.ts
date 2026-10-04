export const MIN_ROTATE_SECONDS = 5

/** Reads `?rotate=`: 0 for no rotation, otherwise at least MIN_ROTATE_SECONDS. */
export function rotationSeconds(raw: string | null): number {
  if (raw === null || raw.trim() === '') return 0
  const seconds = Number(raw)
  if (!Number.isFinite(seconds) || seconds <= 0) return 0
  return Math.max(seconds, MIN_ROTATE_SECONDS)
}
