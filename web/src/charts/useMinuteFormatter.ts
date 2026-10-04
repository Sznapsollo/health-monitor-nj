import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

/**
 * Minute keys are plain integers on the wire; the browser is the only place
 * that knows which time zone the viewer is in.
 */
export function useMinuteFormatter(): (minute: number) => string {
  const { i18n } = useTranslation()
  return useMemo(() => {
    const format = new Intl.DateTimeFormat(i18n.language, {
      hour: '2-digit',
      minute: '2-digit',
    })
    return (minute: number) => format.format(new Date(minute * 60_000))
  }, [i18n.language])
}
