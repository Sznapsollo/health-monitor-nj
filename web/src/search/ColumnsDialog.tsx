import ArrowDownwardIcon from '@mui/icons-material/ArrowDownward'
import ArrowUpwardIcon from '@mui/icons-material/ArrowUpward'
import DeleteIcon from '@mui/icons-material/Delete'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import Chip from '@mui/material/Chip'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import FormControlLabel from '@mui/material/FormControlLabel'
import IconButton from '@mui/material/IconButton'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DEFAULT_COLUMNS, fieldColumn, fieldOf, move, type SearchColumn } from './columns'

interface Props {
  columns: SearchColumn[]
  /** Fields the current results carry that are not columns yet. */
  found: string[]
  onChange: (columns: SearchColumn[]) => void
  onClose: () => void
}

export function ColumnsDialog({ columns, found, onChange, onClose }: Props) {
  const { t } = useTranslation()
  const [typed, setTyped] = useState('')

  const addField = (name: string) => {
    const id = fieldColumn(name.trim())
    if (!name.trim() || columns.some((c) => c.id === id)) return
    onChange([...columns, { id, visible: true }])
  }
  const label = (id: string) => fieldOf(id) ?? t(`search.column.${id}`)

  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>{t('search.columnsTitle')}</DialogTitle>
      <DialogContent>
        <Stack spacing={0.5}>
          {columns.map((c, i) => (
            <Stack key={c.id} direction="row" alignItems="center" spacing={1}>
              <FormControlLabel
                sx={{ flex: 1, mr: 0 }}
                control={
                  <Checkbox
                    size="small"
                    checked={c.visible}
                    onChange={(e) =>
                      onChange(
                        columns.map((x) =>
                          x.id === c.id ? { ...x, visible: e.target.checked } : x,
                        ),
                      )
                    }
                  />
                }
                label={label(c.id)}
              />
              <IconButton
                size="small"
                disabled={i === 0}
                aria-label={t('search.moveUp', { name: label(c.id) })}
                onClick={() => onChange(move(columns, c.id, -1))}
              >
                <ArrowUpwardIcon fontSize="small" />
              </IconButton>
              <IconButton
                size="small"
                disabled={i === columns.length - 1}
                aria-label={t('search.moveDown', { name: label(c.id) })}
                onClick={() => onChange(move(columns, c.id, 1))}
              >
                <ArrowDownwardIcon fontSize="small" />
              </IconButton>
              <IconButton
                size="small"
                sx={{ visibility: fieldOf(c.id) ? 'visible' : 'hidden' }}
                aria-label={t('search.removeColumn', { name: label(c.id) })}
                onClick={() => onChange(columns.filter((x) => x.id !== c.id))}
              >
                <DeleteIcon fontSize="small" />
              </IconButton>
            </Stack>
          ))}
        </Stack>

        {found.length > 0 ? (
          <Stack spacing={1} sx={{ mt: 2 }}>
            <Typography variant="subtitle2">{t('search.fieldsFound')}</Typography>
            <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
              {found.map((name) => (
                <Chip key={name} size="small" label={name} onClick={() => addField(name)} />
              ))}
            </Stack>
          </Stack>
        ) : null}

        <Stack direction="row" spacing={1} alignItems="flex-start" sx={{ mt: 2 }}>
          <TextField
            size="small"
            label={t('search.addField')}
            helperText={t('search.addFieldHelp')}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                addField(typed)
                setTyped('')
              }
            }}
          />
          <Button
            disabled={!typed.trim()}
            onClick={() => {
              addField(typed)
              setTyped('')
            }}
          >
            {t('search.add')}
          </Button>
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={() => onChange(DEFAULT_COLUMNS)}>{t('search.resetColumns')}</Button>
        <Button onClick={onClose}>{t('common.close')}</Button>
      </DialogActions>
    </Dialog>
  )
}
