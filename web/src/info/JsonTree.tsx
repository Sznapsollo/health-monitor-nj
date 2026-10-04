import ChevronRightIcon from '@mui/icons-material/ChevronRight'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import Box from '@mui/material/Box'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { contains, entriesOf, epochOf, isBranch } from './json'

interface Props {
  value: unknown
  /** Shows only what contains this text, opened up; empty shows everything. */
  search: string
}

const OPEN_DEPTH = 2

export function JsonTree({ value, search }: Props) {
  const needle = search.trim().toLowerCase()
  return (
    <Box sx={{ fontFamily: 'monospace', fontSize: 13, lineHeight: 1.7, overflowX: 'auto' }}>
      <Node name={null} value={value} depth={0} needle={needle} />
    </Box>
  )
}

function Node({
  name,
  value,
  depth,
  needle,
}: {
  name: string | null
  value: unknown
  depth: number
  needle: string
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(depth < OPEN_DEPTH)
  if (!contains(name, value, needle)) return null

  const label =
    name === null ? null : (
      <Box component="span" sx={{ color: 'primary.main', '&::after': { content: '": "' } }}>
        <Marked text={name} needle={needle} />
      </Box>
    )

  if (!isBranch(value)) {
    return (
      <Box sx={{ pl: depth === 0 ? 0 : 3, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
        {label}
        <Leaf value={value} needle={needle} />
      </Box>
    )
  }

  const children = entriesOf(value)
  const shown = open || needle !== ''
  const summary = Array.isArray(value)
    ? t('info.items', { count: children.length })
    : t('info.fields', { count: children.length })
  return (
    <Box sx={{ pl: depth === 0 ? 0 : 1 }}>
      <Box
        component="button"
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={shown}
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          border: 0,
          p: 0,
          background: 'none',
          color: 'inherit',
          font: 'inherit',
          cursor: 'pointer',
        }}
      >
        {shown ? <ExpandMoreIcon fontSize="small" /> : <ChevronRightIcon fontSize="small" />}
        {label}
        <Box component="span" sx={{ color: 'text.secondary' }}>
          {summary}
        </Box>
      </Box>
      {shown
        ? children.map(([k, v]) => (
            <Box key={k} sx={{ pl: 2 }}>
              <Node name={k} value={v} depth={depth + 1} needle={needle} />
            </Box>
          ))
        : null}
    </Box>
  )
}

function Leaf({ value, needle }: { value: unknown; needle: string }) {
  const { t } = useTranslation()
  if (value === null)
    return (
      <Box component="span" sx={{ color: 'text.disabled' }}>
        {t('info.null')}
      </Box>
    )
  const text = String(value)
  const color = typeof value === 'string' ? 'success.main' : 'warning.main'
  const when = epochOf(value)
  return (
    <>
      <Box component="span" sx={{ color }}>
        <Marked text={text} needle={needle} />
      </Box>
      {when ? (
        <Box component="span" sx={{ color: 'text.secondary', ml: 1 }}>
          {t('info.epoch', { when })}
        </Box>
      ) : null}
    </>
  )
}

function Marked({ text, needle }: { text: string; needle: string }): ReactNode {
  if (!needle) return text
  const at = text.toLowerCase().indexOf(needle)
  if (at < 0) return text
  return (
    <>
      {text.slice(0, at)}
      <Box component="mark" sx={{ bgcolor: 'warning.light', color: 'inherit', px: 0.25 }}>
        {text.slice(at, at + needle.length)}
      </Box>
      {text.slice(at + needle.length)}
    </>
  )
}
