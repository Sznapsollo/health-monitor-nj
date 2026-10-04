import { BarChart, LineChart } from 'echarts/charts'
import {
  DataZoomInsideComponent,
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from 'echarts/components'
import { init, use as register, type ECharts } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'

// Only what minuteSeriesOption draws; anything else it starts using must be
// registered here too, or ECharts quietly leaves it out.
register([
  BarChart,
  LineChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomInsideComponent,
  CanvasRenderer,
])

export { init, type ECharts }
