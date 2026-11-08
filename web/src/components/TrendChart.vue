<template>
  <Chart :option="option" :height="height" />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { EChartsOption } from 'echarts'
import Chart from '@/components/Chart.vue'
import type { TrafficPoint } from '@/api/types'
import { formatBytes, formatNumber } from '@/api/client'
import {
  CHART_COLORS,
  axisInterval,
  formatChartTime,
  formatFullTime,
  type MetricKey,
} from '@/utils/chartTheme'

const props = withDefaults(
  defineProps<{
    points: TrafficPoint[]
    metric: MetricKey
    range: string
    height?: string
  }>(),
  { height: '320px' },
)

const labels = computed(() => props.points.map((p) => formatChartTime(p.time, props.range)))

function tooltipFormatter(params: unknown): string {
  const list = Array.isArray(params) ? params : [params]
  const first = list[0] as { dataIndex?: number } | undefined
  const index = Number(first?.dataIndex ?? 0)
  const p = props.points[index]
  if (!p) return ''
  const rows = [
    `<div class="chart-tooltip-time">${formatFullTime(p.time)}</div>`,
    `<div class="chart-tooltip-row"><span class="chart-tooltip-label">请求数</span><b>${formatNumber(p.requests)}</b></div>`,
    `<div class="chart-tooltip-row"><span class="chart-tooltip-label">入站流量</span><b>${formatBytes(p.inbound_bytes)}</b></div>`,
    `<div class="chart-tooltip-row"><span class="chart-tooltip-label">出站流量</span><b>${formatBytes(p.outbound_bytes)}</b></div>`,
  ]
  if (props.metric === 'latency') {
    rows.push(`<div class="chart-tooltip-row"><span class="chart-tooltip-label">平均响应</span><b>${formatNumber(p.avg_ms)} ms</b></div>`)
  }
  return `<div class="chart-tooltip">${rows.join('')}</div>`
}

const yAxis = computed(() => {
  if (props.metric === 'traffic') {
    return {
      type: 'value' as const,
      splitLine: { lineStyle: { color: CHART_COLORS.grid } },
      axisLabel: { color: CHART_COLORS.axis, formatter: (v: number) => formatBytes(v) },
    }
  }
  return {
    type: 'value' as const,
    splitLine: { lineStyle: { color: CHART_COLORS.grid } },
    axisLabel: {
      color: CHART_COLORS.axis,
      formatter: (v: number) => (props.metric === 'latency' ? `${v} ms` : formatNumber(v)),
    },
  }
})

const series = computed(() => {
  if (props.metric === 'requests') {
    return [
      {
        name: '请求数',
        type: 'bar' as const,
        data: props.points.map((p) => p.requests),
        barMaxWidth: 18,
        itemStyle: { color: CHART_COLORS.requests, borderRadius: [4, 4, 0, 0] },
        emphasis: { focus: 'series' as const },
      },
    ]
  }
  if (props.metric === 'latency') {
    return [
      {
        name: '平均响应',
        type: 'line' as const,
        smooth: true,
        showSymbol: false,
        data: props.points.map((p) => p.avg_ms),
        lineStyle: { width: 2, color: CHART_COLORS.average },
        itemStyle: { color: CHART_COLORS.average },
        emphasis: { focus: 'series' as const },
      },
    ]
  }
  return [
    {
      name: '入站流量',
      type: 'line' as const,
      smooth: true,
      showSymbol: false,
      data: props.points.map((p) => p.inbound_bytes),
      lineStyle: { width: 2, color: CHART_COLORS.inbound },
      itemStyle: { color: CHART_COLORS.inbound },
      areaStyle: { color: CHART_COLORS.inbound, opacity: 0.08 },
      emphasis: { focus: 'series' as const },
    },
    {
      name: '出站流量',
      type: 'line' as const,
      smooth: true,
      showSymbol: false,
      data: props.points.map((p) => p.outbound_bytes),
      lineStyle: { width: 2, color: CHART_COLORS.outbound },
      itemStyle: { color: CHART_COLORS.outbound },
      areaStyle: { color: CHART_COLORS.outbound, opacity: 0.06 },
      emphasis: { focus: 'series' as const },
    },
  ]
})

const option = computed<EChartsOption>(() => {
  const isTraffic = props.metric === 'traffic'
  const isLatency = props.metric === 'latency'
  const legendData = isTraffic ? ['入站流量', '出站流量'] : isLatency ? ['平均响应'] : []
  return {
    animationDuration: 250,
    animationDurationUpdate: 250,
    tooltip: {
      trigger: 'axis',
      confine: true,
      formatter: tooltipFormatter,
      backgroundColor: '#fff',
      borderColor: '#E8EDF5',
      textStyle: { color: '#334155', fontSize: 12 },
      extraCssText: 'box-shadow: 0 8px 24px rgba(15, 23, 42, 0.12); border-radius: 8px;',
      axisPointer: {
        type: 'cross',
        lineStyle: { color: '#CBD5E1', type: 'dashed' },
        crossStyle: { color: '#CBD5E1', type: 'dashed' },
      },
    },
    axisPointer: {
      type: 'cross',
      lineStyle: { color: '#CBD5E1', type: 'dashed' },
      crossStyle: { color: '#CBD5E1', type: 'dashed' },
    },
    legend: legendData.length
      ? {
          data: legendData,
          bottom: 0,
          itemGap: 22,
          textStyle: { color: CHART_COLORS.text, fontSize: 12 },
          icon: 'roundRect',
        }
      : { show: false },
    grid: { left: 8, right: 18, top: 18, bottom: legendData.length ? 48 : 30, containLabel: true },
    xAxis: {
      type: 'category',
      boundaryGap: !isLatency && !isTraffic,
      data: labels.value,
      axisLine: { lineStyle: { color: '#E8EDF5' } },
      axisTick: { show: false },
      axisLabel: {
        color: CHART_COLORS.axis,
        interval: axisInterval(labels.value.length),
        hideOverlap: true,
        fontSize: 11,
      },
    },
    yAxis: yAxis.value,
    series: series.value,
  }
})
</script>
