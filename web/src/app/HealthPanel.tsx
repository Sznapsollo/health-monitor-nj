import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import Stack from '@mui/material/Stack'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableHead from '@mui/material/TableHead'
import TableRow from '@mui/material/TableRow'
import Typography from '@mui/material/Typography'
import { useEffect, useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Busy } from './Busy'
import type { Issue } from '../api/issues'
import { useHealthStore } from '../store/useHealthStore'
import { everyVisible } from './everyVisible'

/** What the monitor itself had trouble with, one row per kind of problem. */
export function HealthPanel() {
  const { t, i18n } = useTranslation()
  const { issues, unknown, load: loadIssues, mark, error: issuesError, loaded } = useHealthStore()

  useEffect(() => {
    void loadIssues()
    return everyVisible(() => void loadIssues(), 10_000)
  }, [loadIssues])

  const time = useMemo(
    () => new Intl.DateTimeFormat(i18n.language, { dateStyle: 'short', timeStyle: 'medium' }),
    [i18n.language],
  )

  const drops = issues.find((is) => is.key === 'counter:intake.kernelDrops' && !is.known)

  return (
    <Stack spacing={3}>
      {drops ? (
        <Alert severity="warning">{t('health.kernelDrops', { count: drops.count })}</Alert>
      ) : null}
      {issuesError ? <Alert severity="error">{issuesError}</Alert> : null}
      {loaded ? null : <Busy />}

      <Box>
        <Stack direction="row" spacing={2} alignItems="center" sx={{ mb: 1 }}>
          <Typography variant="subtitle1">{t('health.issues')}</Typography>
          {unknown > 0 ? (
            <Button
              size="small"
              variant="outlined"
              onClick={() =>
                void mark(
                  issues.filter((is) => !is.known).map((is) => is.key),
                  true,
                )
              }
            >
              {t('health.markAllKnown', { count: unknown })}
            </Button>
          ) : null}
        </Stack>
        {issues.length === 0 ? (
          <Typography variant="body2" color="text.secondary">
            {t('health.noIssues')}
          </Typography>
        ) : (
          <Box sx={{ overflowX: 'auto' }}>
            <Table size="small">
              <TableHead>
                <TableRow>
                  <TableCell>{t('health.state')}</TableCell>
                  <TableCell>{t('health.problem')}</TableCell>
                  <TableCell>{t('health.type')}</TableCell>
                  <TableCell>{t('health.signal')}</TableCell>
                  <TableCell>{t('health.platform')}</TableCell>
                  <TableCell align="right">{t('health.count')}</TableCell>
                  <TableCell>{t('health.lastSeen')}</TableCell>
                  <TableCell>{t('health.lastSource')}</TableCell>
                  <TableCell />
                </TableRow>
              </TableHead>
              <TableBody>
                {issues.map((is) => (
                  <IssueRow
                    key={is.key}
                    issue={is}
                    time={time}
                    onMark={(known) => void mark([is.key], known)}
                  />
                ))}
              </TableBody>
            </Table>
          </Box>
        )}
      </Box>
    </Stack>
  )
}

function IssueRow({
  issue,
  time,
  onMark,
}: {
  issue: Issue
  time: Intl.DateTimeFormat
  onMark: (known: boolean) => void
}) {
  const { t } = useTranslation()
  const problem = t(`health.reasons.${issue.reason.replace('.', '_')}`, {
    defaultValue: issue.reason,
  })
  const knownTitle = issue.knownBy
    ? t('health.knownBy', {
        name: issue.knownBy,
        when: issue.knownAt ? time.format(new Date(issue.knownAt)) : '',
      })
    : undefined
  return (
    <TableRow sx={{ opacity: issue.known ? 0.6 : 1 }}>
      <TableCell>
        <Chip
          size="small"
          color={issue.known ? 'default' : 'error'}
          label={issue.known ? t('health.known') : t('health.new')}
          title={knownTitle}
        />
      </TableCell>
      <TableCell>{problem}</TableCell>
      <TableCell>{issue.type ?? ''}</TableCell>
      <TableCell>{issue.signal ?? ''}</TableCell>
      <TableCell>{issue.platform ?? ''}</TableCell>
      <TableCell align="right">
        {issue.count.toLocaleString()}
        {!issue.known && issue.new !== issue.count
          ? ` (${t('health.newSince', { count: issue.new })})`
          : ''}
      </TableCell>
      <TableCell>{issue.last ? time.format(new Date(issue.last)) : ''}</TableCell>
      <TableCell>{issue.lastSource ?? ''}</TableCell>
      <TableCell>
        <Button size="small" onClick={() => onMark(!issue.known)}>
          {issue.known ? t('health.markNew') : t('health.markKnown')}
        </Button>
      </TableCell>
    </TableRow>
  )
}
