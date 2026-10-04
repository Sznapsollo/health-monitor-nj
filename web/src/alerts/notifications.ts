import type { Alert, Level } from '../api/alerts'

const SETTINGS_KEY = 'hm.notifications'

export interface NotificationSettings {
  sound: boolean
  browser: boolean
  /** Only these levels interrupt; the rest stay in the list. */
  levels: Level[]
}

export const defaultSettings: NotificationSettings = {
  sound: false,
  browser: false,
  levels: ['ERROR', 'WARN'],
}

export function loadSettings(): NotificationSettings {
  try {
    const raw = globalThis.localStorage?.getItem(SETTINGS_KEY)
    if (!raw) return defaultSettings
    const parsed = JSON.parse(raw) as Partial<NotificationSettings>
    return {
      sound: Boolean(parsed.sound),
      browser: Boolean(parsed.browser),
      levels: Array.isArray(parsed.levels) ? (parsed.levels as Level[]) : defaultSettings.levels,
    }
  } catch {
    return defaultSettings
  }
}

export function saveSettings(settings: NotificationSettings): void {
  try {
    globalThis.localStorage?.setItem(SETTINGS_KEY, JSON.stringify(settings))
  } catch {
    // A remembered preference is not worth an error.
  }
}

/** Whether this alert should interrupt the viewer at all. */
export function shouldNotify(
  alert: Alert,
  settings: NotificationSettings,
  isNew: boolean,
): boolean {
  if (!isNew) return false
  // A silenced alert is recorded and shown, but never makes a sound: that is
  // the whole point of silencing it.
  if (alert.silenced) return false
  return settings.levels.includes(alert.level)
}

/**
 * A short two-tone chime built with the Web Audio API. The old UI shipped
 * thirteen sound files; one generated tone avoids the assets entirely and
 * cannot fail to load.
 */
export function playChime(level: Level): void {
  try {
    const Ctor =
      globalThis.AudioContext ??
      (globalThis as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
    if (!Ctor) return
    const ctx = new Ctor()
    const gain = ctx.createGain()
    gain.gain.value = 0.06
    gain.connect(ctx.destination)

    const notes = level === 'ERROR' ? [660, 440] : [880, 660]
    notes.forEach((frequency, i) => {
      const osc = ctx.createOscillator()
      osc.type = 'sine'
      osc.frequency.value = frequency
      osc.connect(gain)
      const at = ctx.currentTime + i * 0.18
      osc.start(at)
      osc.stop(at + 0.16)
    })
    globalThis.setTimeout(() => void ctx.close(), 800)
  } catch {
    // No audio device, or a browser that refuses without a gesture.
  }
}

/** Asks for permission only when the viewer switches notifications on. */
export async function requestPermission(): Promise<boolean> {
  const api = globalThis.Notification
  if (!api) return false
  if (api.permission === 'granted') return true
  if (api.permission === 'denied') return false
  try {
    return (await api.requestPermission()) === 'granted'
  } catch {
    return false
  }
}

export function showNotification(alert: Alert): void {
  const api = globalThis.Notification
  if (!api || api.permission !== 'granted') return
  try {
    new api(`${alert.level}: ${alert.category ?? 'alert'}`, {
      body: alert.message,
      // The tag collapses repeats of the same alert into one notification.
      tag: alert.groupKey || alert.id,
    })
  } catch {
    // Some browsers refuse to construct notifications outside a worker.
  }
}
