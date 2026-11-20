// The only ECharts entry point: tree-shaken through echarts/core, so the chunk holds just these charts.
import { BarChart, HeatmapChart, LineChart } from 'echarts/charts'
import type { BarSeriesOption, HeatmapSeriesOption, LineSeriesOption } from 'echarts/charts'
import {
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  MarkPointComponent,
  TooltipComponent,
  VisualMapComponent,
} from 'echarts/components'
import type {
  DataZoomComponentOption,
  GridComponentOption,
  LegendComponentOption,
  MarkPointComponentOption,
  TooltipComponentOption,
  VisualMapComponentOption,
} from 'echarts/components'
import * as echarts from 'echarts/core'
import type { ComposeOption } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'

echarts.use([
  BarChart,
  LineChart,
  HeatmapChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  VisualMapComponent,
  DataZoomComponent,
  MarkPointComponent,
  CanvasRenderer,
])

export type ChartOption = ComposeOption<
  | BarSeriesOption
  | LineSeriesOption
  | HeatmapSeriesOption
  | GridComponentOption
  | TooltipComponentOption
  | LegendComponentOption
  | VisualMapComponentOption
  | DataZoomComponentOption
  | MarkPointComponentOption
>

/** ChartClick is the part of an ECharts click event the page uses. */
export interface ChartClick {
  dataIndex: number
  seriesType?: string
  value?: unknown
}

export function initChart(el: HTMLElement) {
  return echarts.init(el, null, { renderer: 'canvas' })
}
