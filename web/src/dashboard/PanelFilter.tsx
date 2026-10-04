import TextField from '@mui/material/TextField'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { useMonitorStore } from '../store/useMonitorStore'

const TYPING_MS = 400

interface Props {
  viewId: string
  saved: string
}

/** A filter for one panel that lasts until the page is left and is seen by nobody else. */
export function PanelFilter({ viewId, saved }: Props) {
  const { t } = useTranslation()
  const typed = useMonitorStore((s) => s.panelFilters[viewId])
  const setPanelFilter = useMonitorStore((s) => s.setPanelFilter)
  const [text, setText] = useState(typed ?? saved)
  const [shownSaved, setShownSaved] = useState(saved)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  if (saved !== shownSaved) {
    setShownSaved(saved)
    setText(saved)
  }

  const savedBefore = useRef(saved)
  useEffect(() => {
    if (savedBefore.current === saved) return
    savedBefore.current = saved
    globalThis.clearTimeout(timer.current)
    setPanelFilter(viewId, null)
  }, [saved, viewId, setPanelFilter])

  useEffect(() => () => globalThis.clearTimeout(timer.current), [])

  const type = (value: string) => {
    setText(value)
    globalThis.clearTimeout(timer.current)
    timer.current = globalThis.setTimeout(
      () => setPanelFilter(viewId, value.trim() === '' || value === saved ? null : value),
      TYPING_MS,
    )
  }

  return (
    <TextField
      size="small"
      variant="standard"
      placeholder={t('dashboard.filterHere')}
      title={t('dashboard.filterHereHelp')}
      value={text}
      onChange={(e) => type(e.target.value)}
      onBlur={() => text.trim() === '' && setText(saved)}
      slotProps={{ htmlInput: { 'aria-label': t('dashboard.filterHere') } }}
      sx={{ width: 140, mr: 1 }}
    />
  )
}
