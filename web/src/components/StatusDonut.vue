<template>
  <Chart :option="option" height="300px" />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { EChartsOption } from 'echarts'
import Chart from '@/components/Chart.vue'
import type { Totals } from '@/api/types'
import { formatNumber } from '@/api/client'
import { CHART_COLORS, statusTotal } from '@/utils/chartTheme'

const props = defineProps<{ totals?: Totals | null }>()

const counts = computed(() => ({
  '2xx': props.totals?.status_2xx ?? 0,
  '3xx': props.totals?.status_3xx ?? 0,
  '4xx': props.totals?.status_4xx ?? 0,
  '5xx': props.totals?.status_5xx ?? 0,
}))

const total = computed(() => statusTotal(props.totals))

function percent(value: number): string {
  return total.value > 0 ? `${((value / total.value) * 100).toFixed(1)}%` : '0%'
}

const option = computed<EChartsOption>(() => {
  const data = [
    { name: '2xx', value: counts.value['2xx'], itemStyle: { color: CHART_COLORS.success2xx } },
    { name: '3xx', value: counts.value['3xx'], itemStyle: { color: CHART_COLORS.status3xx } },
    { name: '4xx', value: counts.value['4xx'], itemStyle: { color: CHART_COLORS.status4xx } },
    { name: '5xx', value: counts.value['5xx'], itemStyle: { color: CHART_COLORS.status5xx } },
  ]
  return {
    tooltip: {
      trigger: 'item',
      confine: true,
      backgroundColor: '#fff',
      borderColor: '#E8EDF5',
      textStyle: { color: '#334155', fontSize: 12 },
      extraCssText: 'box-shadow: 0 8px 24px rgba(15, 23, 42, 0.12); border-radius: 8px;',
      formatter: (params: unknown) => {
        const p = params as { name?: string; value?: number }
        return `${p.name ?? ''}：${formatNumber(Number(p.value ?? 0))}（${percent(Number(p.value ?? 0))}）`
      },
    },
    title: {
      text: formatNumber(total.value),
      subtext: '总请求',
      left: 'center',
      top: '36%',
      textStyle: { color: '#0F172A', fontSize: 24, fontWeight: 700 },
      subtextStyle: { color: '#94A3B8', fontSize: 12 },
    },
    legend: {
      bottom: 0,
      itemGap: 18,
      icon: 'roundRect',
      textStyle: { color: CHART_COLORS.text, fontSize: 12 },
      formatter: (name: string) => {
        const item = data.find((d) => d.name === name)
        return `${name}  ${formatNumber(item?.value ?? 0)} (${percent(item?.value ?? 0)})`
      },
    },
    series: [
      {
        type: 'pie',
        radius: ['52%', '72%'],
        center: ['50%', '43%'],
        avoidLabelOverlap: true,
        label: { show: false },
        labelLine: { show: false },
        itemStyle: { borderColor: '#fff', borderWidth: 3 },
        emphasis: { scaleSize: 4 },
        data,
      },
    ],
  }
})
</script>
