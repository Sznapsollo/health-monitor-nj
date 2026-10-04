import ClearIcon from '@mui/icons-material/Clear'
import DeleteIcon from '@mui/icons-material/Delete'
import LinkIcon from '@mui/icons-material/Link'
import SaveIcon from '@mui/icons-material/Save'
import Button from '@mui/material/Button'
import IconButton from '@mui/material/IconButton'
import MenuItem from '@mui/material/MenuItem'
import Select from '@mui/material/Select'
import Snackbar from '@mui/material/Snackbar'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Tooltip from '@mui/material/Tooltip'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { atDefaults, useMonitorStore } from '../store/useMonitorStore'
import type { Criteria } from '../ws/types'
import {
  deleteSaved,
  loadSaved,
  saveNamed,
  searchFromCriteria,
  searchFromView,
  type SavedCriteria,
} from './persistence'

interface Props {
  platform: string
  criteria: Criteria[]
  /** The charts on show, so a set remembers the group as well as the filters. */
  visible?: string[]
  onApply: (criteria: Criteria[], name?: string | null, visible?: string[]) => void
}

/**
 * Named filter sets, as the old UI's "Filtry" panel could save — plus the two
 * things it never had: a way back to the defaults, and a link that says what
 * you are actually looking at.
 */
export function SavedCriteriaBar({ platform, criteria, visible, onApply }: Props) {
  const { t } = useTranslation()
  const activeView = useMonitorStore((s) => s.activeView)
  const resetCriteria = useMonitorStore((s) => s.resetCriteria)
  const catalogue = useMonitorStore((s) => s.catalogue)
  const specs = catalogue?.platforms.find((p) => p.name === platform)?.signals ?? []
  // Nothing to clear when the filters are already what the definitions ask
  // for, so the control is not offered.
  const canClear = activeView !== null || !atDefaults(criteria, specs)
  const [saved, setSaved] = useState<SavedCriteria[]>(() => loadSaved())
  const [name, setName] = useState('')
  const [copied, setCopied] = useState(false)

  const apply = (value: string) => {
    if (value === '') {
      resetCriteria()
      return
    }
    const entry = saved.find((s) => s.name === value)
    if (entry) onApply(entry.criteria, entry.name, entry.visible)
  }

  /**
   * A link to a saved set addresses it by name; without one it describes the
   * filters themselves, so it works in someone else's browser too.
   */
  const copyLink = async () => {
    const search = activeView
      ? searchFromView(activeView, platform)
      : criteria[0]
        ? searchFromCriteria(criteria[0], platform)
        : ''
    const url = `${globalThis.location.origin}${globalThis.location.pathname}${search ? '?' + search : ''}`
    try {
      await globalThis.navigator?.clipboard?.writeText(url)
      setCopied(true)
    } catch {
      // Clipboard access can be refused; the address bar already shows it.
    }
  }

  return (
    <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
      <Select
        size="small"
        displayEmpty
        value={activeView ?? ''}
        onChange={(e) => apply(String(e.target.value))}
        sx={{ minWidth: 170 }}
      >
        <MenuItem value="">{t('saved.none')}</MenuItem>
        {saved.map((s) => (
          <MenuItem key={s.name} value={s.name}>
            {s.name}
          </MenuItem>
        ))}
      </Select>

      <TextField
        size="small"
        label={t('saved.name')}
        value={name}
        onChange={(e) => setName(e.target.value)}
        sx={{ width: 170 }}
      />
      <Button
        size="small"
        startIcon={<SaveIcon />}
        disabled={!name.trim()}
        onClick={() => {
          const entry = { name: name.trim(), platform, criteria, visible }
          setSaved(saveNamed(entry))
          onApply(entry.criteria, entry.name, entry.visible)
          setName('')
        }}
      >
        {t('saved.save')}
      </Button>

      <Tooltip title={t('saved.delete')}>
        <span>
          <IconButton
            size="small"
            disabled={!activeView}
            aria-label={t('saved.delete')}
            onClick={() => {
              if (!activeView) return
              setSaved(deleteSaved(activeView))
              resetCriteria()
            }}
          >
            <DeleteIcon fontSize="small" />
          </IconButton>
        </span>
      </Tooltip>

      {canClear ? (
        <Tooltip title={t('saved.reset')}>
          <IconButton size="small" aria-label={t('saved.reset')} onClick={() => resetCriteria()}>
            <ClearIcon fontSize="small" />
          </IconButton>
        </Tooltip>
      ) : null}

      <Tooltip title={activeView ? t('saved.copySetLink') : t('saved.copyChartLink')}>
        <IconButton size="small" onClick={() => void copyLink()} aria-label={t('saved.copyLink')}>
          <LinkIcon fontSize="small" />
        </IconButton>
      </Tooltip>

      <Snackbar
        open={copied}
        autoHideDuration={3000}
        onClose={() => setCopied(false)}
        message={t('saved.copied')}
      />
    </Stack>
  )
}
