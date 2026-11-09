<template>
  <div ref="el" class="chart" :style="{ height }"></div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { BarChart, LineChart, PieChart } from 'echarts/charts'
import { GridComponent, LegendComponent, TitleComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { ECharts, EChartsOption } from 'echarts'

echarts.use([BarChart, LineChart, PieChart, GridComponent, LegendComponent, TitleComponent, TooltipComponent, CanvasRenderer])

const props = withDefaults(defineProps<{ option: EChartsOption; height?: string }>(), {
  height: '300px',
})

const el = ref<HTMLDivElement>()
let chart: ECharts | null = null
let observer: ResizeObserver | null = null
let lastOption = ''

function render() {
  if (!chart || !props.option) return
  const next = JSON.stringify(props.option)
  if (next === lastOption) return
  lastOption = next
  chart.setOption(props.option, true)
}

onMounted(() => {
  if (!el.value) return
  chart = echarts.init(el.value)
  render()
  observer = new ResizeObserver(() => chart?.resize())
  observer.observe(el.value)
})

watch(() => props.option, render, { deep: true })

onBeforeUnmount(() => {
  observer?.disconnect()
  chart?.dispose()
})
</script>
