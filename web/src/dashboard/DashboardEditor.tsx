import AddIcon from '@mui/icons-material/Add'
import ArrowBackIcon from '@mui/icons-material/ArrowBack'
import ArrowDownwardIcon from '@mui/icons-material/ArrowDownward'
import ArrowForwardIcon from '@mui/icons-material/ArrowForward'
import ArrowUpwardIcon from '@mui/icons-material/ArrowUpward'
import DeleteIcon from '@mui/icons-material/Delete'
import DragIndicatorIcon from '@mui/icons-material/DragIndicator'
import EditIcon from '@mui/icons-material/Edit'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import FormControlLabel from '@mui/material/FormControlLabel'
import IconButton from '@mui/material/IconButton'
import Paper from '@mui/material/Paper'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useState, type DragEvent } from 'react'
import { useTranslation } from 'react-i18next'

import type { SignalSpec } from '../api/catalogue'
import { saveDashboard, type Column, type Dashboard, type Panel, type Row } from '../api/dashboards'
import { columnTemplate } from './layout'
import { PanelDialog } from './PanelDialog'

interface Props {
  dashboard: Dashboard
  platform: string
  specs: SignalSpec[]
  onSaved: (dashboard: Dashboard) => void
  onCancel: () => void
}

type Slot = { row: number; column: number; index: number }

type Dragged =
  | { kind: 'row'; row: number }
  | { kind: 'column'; row: number; column: number }
  | { kind: 'panel'; slot: Slot }

function move<T>(list: T[], from: number, to: number): T[] {
  if (to < 0 || to >= list.length) return list
  const out = [...list]
  const [item] = out.splice(from, 1)
  out.splice(to, 0, item as T)
  return out
}

function cloneRows(rows: Row[]): Row[] {
  return rows.map((r) => ({ columns: r.columns.map((c) => ({ ...c, panels: [...c.panels] })) }))
}

export function DashboardEditor({ dashboard, platform, specs, onSaved, onCancel }: Props) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<Dashboard>(() => structuredClone(dashboard))
  const [editing, setEditing] = useState<Slot | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [dragged, setDragged] = useState<Dragged | null>(null)
  const [over, setOver] = useState<string | null>(null)

  const setRows = (rows: Row[]) => setDraft((d) => ({ ...d, rows }))
  const patchRow = (r: number, fn: (row: Row) => Row) =>
    setRows(draft.rows.map((row, k) => (k === r ? fn(row) : row)))
  const patchColumn = (r: number, i: number, fn: (c: Column) => Column) =>
    patchRow(r, (row) => ({ columns: row.columns.map((c, k) => (k === i ? fn(c) : c)) }))

  const movePanel = (from: Slot, to: Slot) => {
    const rows = cloneRows(draft.rows)
    const source = rows[from.row]?.columns[from.column]
    const target = rows[to.row]?.columns[to.column]
    if (!source || !target) return
    if (source === target) {
      source.panels = move(source.panels, from.index, to.index)
    } else {
      const [panel] = source.panels.splice(from.index, 1)
      if (panel) target.panels.splice(Math.min(to.index, target.panels.length), 0, panel)
    }
    setRows(rows)
  }

  const moveColumn = (
    from: { row: number; column: number },
    to: { row: number; column: number },
  ) => {
    if (from.row === to.row) {
      patchRow(from.row, (row) => ({
        columns: move(row.columns, from.column, Math.min(to.column, row.columns.length - 1)),
      }))
      return
    }
    const rows = cloneRows(draft.rows)
    const [column] = rows[from.row]?.columns.splice(from.column, 1) ?? []
    const target = rows[to.row]
    if (!column || !target) return
    target.columns.splice(Math.min(to.column, target.columns.length), 0, column)
    setRows(rows)
  }

  // Native drag and drop: each box accepts what it knows how to place and lets
  // the rest bubble to the box around it.
  const startDrag = (e: DragEvent, what: Dragged, box: HTMLElement | null) => {
    e.dataTransfer?.setData('text/plain', what.kind)
    if (box) e.dataTransfer?.setDragImage?.(box, 16, 16)
    setDragged(what)
  }
  const endDrag = () => {
    setDragged(null)
    setOver(null)
  }
  const dropZone = (key: string, accepts: (d: Dragged) => boolean, drop: (d: Dragged) => void) => ({
    onDragOver: (e: DragEvent) => {
      if (!dragged || !accepts(dragged)) return
      e.preventDefault()
      e.stopPropagation()
      if (over !== key) setOver(key)
    },
    onDrop: (e: DragEvent) => {
      if (!dragged || !accepts(dragged)) return
      e.preventDefault()
      e.stopPropagation()
      drop(dragged)
      endDrag()
    },
  })
  const outline = (key: string) =>
    over === key ? { outline: '2px dashed', outlineColor: 'primary.main' } : {}
  const handle = (label: string, onStart: (e: DragEvent) => void) => (
    <Box
      component="span"
      draggable
      onDragStart={onStart}
      onDragEnd={endDrag}
      aria-label={label}
      title={label}
      sx={{ display: 'inline-flex', cursor: 'grab', color: 'text.secondary' }}
    >
      <DragIndicatorIcon fontSize="small" />
    </Box>
  )

  const savePanel = (panel: Panel) => {
    if (!editing) return
    patchColumn(editing.row, editing.column, (c) => {
      const panels = [...c.panels]
      if (editing.index < 0) panels.push(panel)
      else panels[editing.index] = panel
      return { ...c, panels }
    })
    setEditing(null)
  }

  const save = async () => {
    setBusy(true)
    setError(null)
    try {
      onSaved(await saveDashboard(platform, draft))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'unknown error')
    } finally {
      setBusy(false)
    }
  }

  const summary = (panel: Panel) => {
    const spec = specs.find((s) => s.name === panel.signal)
    const parts = [panel.title || spec?.displayName || t(`dashboard.${panel.type}`)]
    if (panel.group) parts.push(panel.group + (panel.sub ? ` / ${panel.sub}` : ''))
    if (panel.levels?.length) parts.push(panel.levels.join(', '))
    return parts.join(' · ')
  }

  const valid =
    draft.rows.length > 0 &&
    draft.rows.every((r) => r.columns.length > 0 && r.columns.every((c) => c.panels.length > 0))
  const editingPanel: Panel =
    (editing &&
      editing.index >= 0 &&
      draft.rows[editing.row]?.columns[editing.column]?.panels[editing.index]) ||
    ({ type: 'chart' } as Panel)

  return (
    <Stack spacing={2}>
      <Stack direction="row" spacing={2} alignItems="center" flexWrap="wrap" useFlexGap>
        <TextField
          size="small"
          label={t('dashboard.name')}
          value={draft.name}
          onChange={(e) => setDraft({ ...draft, name: e.target.value })}
        />
        <FormControlLabel
          label={t('dashboard.default')}
          control={
            <Checkbox
              size="small"
              checked={Boolean(draft.default)}
              onChange={(e) => setDraft({ ...draft, default: e.target.checked || undefined })}
            />
          }
        />
        <Button
          startIcon={<AddIcon />}
          onClick={() => setRows([...draft.rows, { columns: [{ width: 1, panels: [] }] }])}
        >
          {t('dashboard.addRow')}
        </Button>
        <Box sx={{ flexGrow: 1 }} />
        <Button onClick={onCancel} disabled={busy}>
          {t('common.cancel')}
        </Button>
        <Button variant="contained" onClick={() => void save()} disabled={busy || !valid}>
          {t('dashboard.save')}
        </Button>
      </Stack>
      {error ? <Alert severity="error">{error}</Alert> : null}
      {!valid ? <Alert severity="info">{t('dashboard.needsPanels')}</Alert> : null}

      {draft.rows.map((row, r) => (
        <Paper
          key={r}
          id={`row-${r}`}
          variant="outlined"
          sx={{ p: 1, ...outline(`row-${r}`) }}
          {...dropZone(
            `row-${r}`,
            (d) => d.kind === 'row' || d.kind === 'column',
            (d) => {
              if (d.kind === 'row') setRows(move(draft.rows, d.row, r))
              if (d.kind === 'column') moveColumn(d, { row: r, column: row.columns.length })
            },
          )}
        >
          <Stack direction="row" spacing={1} alignItems="center" sx={{ mb: 1 }}>
            {handle(t('dashboard.dragRow'), (e) =>
              startDrag(e, { kind: 'row', row: r }, document.getElementById(`row-${r}`)),
            )}
            <Typography variant="subtitle2">{t('dashboard.row', { n: r + 1 })}</Typography>
            <IconButton
              size="small"
              aria-label={t('dashboard.rowUp')}
              disabled={r === 0}
              onClick={() => setRows(move(draft.rows, r, r - 1))}
            >
              <ArrowUpwardIcon fontSize="small" />
            </IconButton>
            <IconButton
              size="small"
              aria-label={t('dashboard.rowDown')}
              disabled={r === draft.rows.length - 1}
              onClick={() => setRows(move(draft.rows, r, r + 1))}
            >
              <ArrowDownwardIcon fontSize="small" />
            </IconButton>
            <Button
              size="small"
              startIcon={<AddIcon />}
              onClick={() =>
                patchRow(r, (row) => ({ columns: [...row.columns, { width: 1, panels: [] }] }))
              }
            >
              {t('dashboard.addColumn')}
            </Button>
            <Box sx={{ flexGrow: 1 }} />
            <IconButton
              size="small"
              aria-label={t('dashboard.removeRow')}
              onClick={() => setRows(draft.rows.filter((_, k) => k !== r))}
            >
              <DeleteIcon fontSize="small" />
            </IconButton>
          </Stack>
          <Box
            sx={{
              display: 'grid',
              gap: 2,
              gridTemplateColumns: { xs: 'minmax(0, 1fr)', md: columnTemplate(row.columns) },
              alignItems: 'start',
            }}
          >
            {row.columns.map((column, i) => (
              <Paper
                key={i}
                id={`column-${r}-${i}`}
                variant="outlined"
                sx={{ p: 1, ...outline(`column-${r}-${i}`) }}
                {...dropZone(
                  `column-${r}-${i}`,
                  (d) => d.kind === 'column' || d.kind === 'panel',
                  (d) => {
                    if (d.kind === 'column') moveColumn(d, { row: r, column: i })
                    if (d.kind === 'panel') {
                      movePanel(d.slot, { row: r, column: i, index: column.panels.length })
                    }
                  },
                )}
              >
                <Stack direction="row" spacing={1} alignItems="center" sx={{ mb: 1 }}>
                  {handle(t('dashboard.dragColumn'), (e) =>
                    startDrag(
                      e,
                      { kind: 'column', row: r, column: i },
                      document.getElementById(`column-${r}-${i}`),
                    ),
                  )}
                  <Typography variant="body2">
                    {t('dashboard.width', { width: column.width ?? 1 })}
                  </Typography>
                  <IconButton
                    size="small"
                    aria-label={t('dashboard.narrower')}
                    disabled={(column.width ?? 1) <= 1}
                    onClick={() => patchColumn(r, i, (c) => ({ ...c, width: (c.width ?? 1) - 1 }))}
                  >
                    <ArrowBackIcon fontSize="small" />
                  </IconButton>
                  <IconButton
                    size="small"
                    aria-label={t('dashboard.wider')}
                    onClick={() => patchColumn(r, i, (c) => ({ ...c, width: (c.width ?? 1) + 1 }))}
                  >
                    <ArrowForwardIcon fontSize="small" />
                  </IconButton>
                  <Box sx={{ flexGrow: 1 }} />
                  <IconButton
                    size="small"
                    aria-label={t('dashboard.addPanel')}
                    onClick={() => setEditing({ row: r, column: i, index: -1 })}
                  >
                    <AddIcon fontSize="small" />
                  </IconButton>
                  <IconButton
                    size="small"
                    aria-label={t('dashboard.removeColumn')}
                    onClick={() =>
                      patchRow(r, (row) => ({ columns: row.columns.filter((_, k) => k !== i) }))
                    }
                  >
                    <DeleteIcon fontSize="small" />
                  </IconButton>
                </Stack>
                <Stack spacing={1}>
                  {column.panels.map((panel, j) => {
                    const here = { row: r, column: i, index: j }
                    const key = `panel-${r}-${i}-${j}`
                    return (
                      <Paper
                        key={j}
                        id={key}
                        sx={{ p: 1, ...outline(key) }}
                        {...dropZone(
                          key,
                          (d) => d.kind === 'panel',
                          (d) => d.kind === 'panel' && movePanel(d.slot, here),
                        )}
                      >
                        <Stack direction="row" spacing={0.5} alignItems="center">
                          {handle(t('dashboard.dragPanel'), (e) =>
                            startDrag(
                              e,
                              { kind: 'panel', slot: here },
                              document.getElementById(key),
                            ),
                          )}
                          <Typography variant="body2" sx={{ flexGrow: 1 }} noWrap>
                            {summary(panel)}
                          </Typography>
                          <IconButton
                            size="small"
                            aria-label={t('dashboard.up')}
                            disabled={j === 0}
                            onClick={() => movePanel(here, { ...here, index: j - 1 })}
                          >
                            <ArrowUpwardIcon fontSize="small" />
                          </IconButton>
                          <IconButton
                            size="small"
                            aria-label={t('dashboard.down')}
                            disabled={j === column.panels.length - 1}
                            onClick={() => movePanel(here, { ...here, index: j + 1 })}
                          >
                            <ArrowDownwardIcon fontSize="small" />
                          </IconButton>
                          <IconButton
                            size="small"
                            aria-label={t('dashboard.left')}
                            disabled={i === 0}
                            onClick={() => movePanel(here, { ...here, column: i - 1 })}
                          >
                            <ArrowBackIcon fontSize="small" />
                          </IconButton>
                          <IconButton
                            size="small"
                            aria-label={t('dashboard.right')}
                            disabled={i === row.columns.length - 1}
                            onClick={() => movePanel(here, { ...here, column: i + 1 })}
                          >
                            <ArrowForwardIcon fontSize="small" />
                          </IconButton>
                          <IconButton
                            size="small"
                            aria-label={t('dashboard.editPanel')}
                            onClick={() => setEditing(here)}
                          >
                            <EditIcon fontSize="small" />
                          </IconButton>
                          <IconButton
                            size="small"
                            aria-label={t('dashboard.removePanel')}
                            onClick={() =>
                              patchColumn(r, i, (c) => ({
                                ...c,
                                panels: c.panels.filter((_, k) => k !== j),
                              }))
                            }
                          >
                            <DeleteIcon fontSize="small" />
                          </IconButton>
                        </Stack>
                      </Paper>
                    )
                  })}
                </Stack>
              </Paper>
            ))}
          </Box>
        </Paper>
      ))}

      <PanelDialog
        open={editing !== null}
        panel={editingPanel}
        specs={specs}
        onClose={() => setEditing(null)}
        onSave={savePanel}
      />
    </Stack>
  )
}
