import Alert from '@mui/material/Alert'
import Stack from '@mui/material/Stack'
import { useTranslation } from 'react-i18next'
import { useShallow } from 'zustand/react/shallow'

import { SavedCriteriaBar } from '../criteria/SavedCriteriaBar'
import type { SignalSpec } from '../api/catalogue'
import { useMonitorStore } from '../store/useMonitorStore'
import { ChartPicker } from './ChartPicker'
import { GaugesSection } from './GaugesSection'
import { SignalSection } from './SignalSection'
import { TileControls } from './TileControls'

export function ChartsPage({ signals }: { signals: SignalSpec[] }) {
  const { t } = useTranslation()
  const { catalogue, platform, criteria, views, visible, applyCriteria, setVisible, moveVisible } =
    useMonitorStore(
      useShallow((s) => ({
        catalogue: s.catalogue,
        platform: s.platform,
        criteria: s.criteria,
        views: s.views,
        visible: s.visible,
        applyCriteria: s.applyCriteria,
        setVisible: s.setVisible,
        moveVisible: s.moveVisible,
      })),
    )
  // Gauges are not timeseries, so they are not in `signals`; their display
  // names come from the whole catalogue.
  const allSignals = catalogue?.platforms.find((p) => p.name === platform)?.signals ?? []

  // Only the picked charts are drawn — and only they are subscribed to, so
  // the rest cost nothing on either end.
  // In the order picked, which is the order the arrows change.
  const inOrder = (from: SignalSpec[]) =>
    visible.flatMap((name) => from.filter((s) => s.name === name))
  const shown = inOrder(signals)
  const offered = allSignals.filter((s) => s.kind === 'timeseries' || s.kind === 'gauge')
  const gauges = inOrder(allSignals.filter((s) => s.kind === 'gauge'))

  return (
    <Stack spacing={4}>
      <Stack
        direction="row"
        spacing={2}
        alignItems="center"
        justifyContent="space-between"
        flexWrap="wrap"
        useFlexGap
      >
        <SavedCriteriaBar
          platform={platform}
          criteria={Object.values(criteria)}
          visible={visible}
          onApply={applyCriteria}
        />
        <Stack direction="row" spacing={1} alignItems="center">
          <ChartPicker specs={offered} visible={visible} onChange={setVisible} />
          <TileControls />
        </Stack>
      </Stack>

      {offered.length > 0 && shown.length === 0 && gauges.length === 0 ? (
        <Alert severity="info">{t('picker.empty')}</Alert>
      ) : null}

      {shown.map((spec, i) => (
        <SignalSection
          key={spec.name}
          spec={spec}
          criteria={criteria[spec.name] ?? { signal: spec.name }}
          view={views[spec.name]}
          onMove={shown.length > 1 ? moveVisible : undefined}
          first={i === 0}
          last={i === shown.length - 1}
        />
      ))}

      {gauges.length > 0 ? (
        <GaugesSection platform={platform} specs={gauges} onMove={moveVisible} />
      ) : null}
    </Stack>
  )
}
