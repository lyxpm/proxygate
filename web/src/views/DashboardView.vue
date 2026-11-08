<template>
  <div class="page">
    <div class="page-toolbar">
      <div class="toolbar-left">
        <el-tag v-if="engine" :type="engine.running ? 'success' : 'danger'" effect="light">
          {{ engine.running ? '代理引擎运行中' : '代理引擎未运行' }}
        </el-tag>
        <el-tag v-else type="info" effect="light">引擎状态未知</el-tag>
        <span v-if="engine" class="muted tiny">配置版本 {{ engine.config_version }}</span>
        <span v-if="engine?.last_error" class="muted tiny" style="color: #ef4444">{{ engine.last_error }}</span>
      </div>
      <div class="toolbar-right">
        <el-radio-group v-model="range" size="small" @change="loadTraffic">
          <el-radio-button value="1h">1 小时</el-radio-button>
          <el-radio-button value="6h">6 小时</el-radio-button>
          <el-radio-button value="24h">24 小时</el-radio-button>
          <el-radio-button value="7d">7 天</el-radio-button>
          <el-radio-button value="30d">30 天</el-radio-button>
        </el-radio-group>
        <el-button :icon="Refresh" circle size="small" @click="loadAll" />
      </div>
    </div>

    <div class="stat-grid">
      <div class="stat-card">
        <div class="stat-label">站点</div>
        <div class="stat-value">{{ overview?.sites.total ?? 0 }}</div>
        <div class="stat-foot"><span class="status-dot" :class="(overview?.sites.healthy ?? 0) > 0 ? 'ok' : 'err'"></span>{{ overview?.enabled_sites ?? 0 }} 启用 · {{ overview?.sites.healthy ?? 0 }} 健康</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">应用服务</div>
        <div class="stat-value">{{ overview?.applications.nodes_healthy ?? 0 }}<span class="muted" style="font-size: 15px"> / {{ overview?.applications.nodes_total ?? 0 }}</span></div>
        <div class="stat-foot">{{ overview?.applications.groups ?? 0 }} 个应用服务</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">SSL 证书</div>
        <div class="stat-value">{{ overview?.certificates.total ?? 0 }}</div>
        <div class="stat-foot" :style="{ color: (overview?.certificates.expiring ?? 0) > 0 ? '#f59e0b' : undefined }">
          {{ (overview?.certificates.expiring ?? 0) > 0 ? `${overview?.certificates.expiring} 张 30 天内到期` : '暂无即将到期证书' }}
        </div>
      </div>
      <div class="stat-card">
        <div class="stat-label">今日请求</div>
        <div class="stat-value">{{ formatNumber(overview?.today.requests ?? 0) }}</div>
        <div class="stat-foot">平均 {{ overview?.today.avg_ms ?? 0 }} ms · P95 {{ overview?.today.p95_ms ?? 0 }} ms</div>
      </div>
      <div class="stat-card">
        <div class="stat-label" title="入站=请求头+请求体；出站=响应头+响应体">今日流量</div>
        <div class="stat-value" style="font-size: 20px">
          <span class="muted" style="font-size: 13px">↓</span> {{ formatBytes(overview?.today.inbound_bytes ?? 0) }}
        </div>
        <div class="stat-foot"><span class="muted">↑</span> {{ formatBytes(overview?.today.outbound_bytes ?? 0) }} 出站</div>
      </div>
    </div>

    <div class="grid-2">
      <div class="card">
        <div class="card-body">
          <div class="card-title">
            <span>请求与流量趋势</span>
            <span class="muted tiny">{{ rangeLabel }}</span>
          </div>
          <Chart :option="trafficOption" height="320px" />
        </div>
      </div>
      <div class="card">
        <div class="card-body">
          <div class="card-title"><span>HTTP 状态码分布</span></div>
          <Chart :option="statusOption" height="320px" />
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-body">
        <div class="card-title">
          <span>最近系统事件</span>
          <el-button text type="primary" size="small" @click="$router.push('/logs')">查看全部</el-button>
        </div>
        <el-table :data="overview?.recent_events ?? []" size="small" style="margin-top: 12px">
          <el-table-column prop="CreatedAt" label="时间" width="180">
            <template #default="{ row }">{{ formatTime(row.CreatedAt) }}</template>
          </el-table-column>
          <el-table-column prop="Level" label="级别" width="90">
            <template #default="{ row }">
              <el-tag :type="levelTagType(row.Level)" size="small" effect="plain">{{ levelLabel(row.Level) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="分类" width="110">
            <template #default="{ row }">{{ categoryLabel(row.Category) }}</template>
          </el-table-column>
          <el-table-column label="内容" show-overflow-tooltip>
            <template #default="{ row }">{{ messageLabel(row.Message) }}</template>
          </el-table-column>
        </el-table>
        <div v-if="!overview?.recent_events?.length" class="empty">暂无系统事件</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { EChartsOption } from 'echarts'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import Chart from '@/components/Chart.vue'
import { api, formatBytes, formatNumber, formatTime } from '@/api/client'
import { categoryLabel, levelLabel, levelTagType, messageLabel } from '@/utils/labels'
import type { DashboardOverview, EngineStatus, TrafficPoint } from '@/api/types'

const overview = ref<DashboardOverview>()
const engine = ref<EngineStatus>()
const series = ref<TrafficPoint[]>([])
const range = ref('24h')
let timer: number | undefined

const rangeLabel = computed(
  () => ({ '1h': '最近 1 小时', '6h': '最近 6 小时', '24h': '最近 24 小时', '7d': '最近 7 天', '30d': '最近 30 天' })[range.value],
)

const trafficOption = computed<EChartsOption>(() => {
  const x = series.value.map((p) => new Date(p.time).toLocaleString('zh-CN', { hour12: false, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }))
  return {
    tooltip: { trigger: 'axis' },
    legend: { data: ['请求数', '入站流量', '出站流量'], bottom: 0, itemGap: 22, textStyle: { color: '#64748b' } },
    grid: { left: 24, right: 24, top: 24, bottom: 46, containLabel: true },
    xAxis: { type: 'category', data: x, boundaryGap: false, axisLabel: { color: '#94a3b8', hideOverlap: true } },
    yAxis: [
      { type: 'value', splitLine: { lineStyle: { color: '#eef2f7' } }, axisLabel: { color: '#94a3b8' } },
      { type: 'value', splitLine: { show: false }, axisLabel: { color: '#94a3b8', formatter: (v: number) => formatBytes(v) } },
    ],
    series: [
      { name: '请求数', type: 'line', smooth: true, data: series.value.map((p) => p.requests), itemStyle: { color: '#0ea5a4' }, areaStyle: { opacity: 0.08 } },
      { name: '入站流量', type: 'line', yAxisIndex: 1, smooth: true, data: series.value.map((p) => p.inbound_bytes), itemStyle: { color: '#6366f1' } },
      { name: '出站流量', type: 'line', yAxisIndex: 1, smooth: true, data: series.value.map((p) => p.outbound_bytes), itemStyle: { color: '#f59e0b' } },
    ],
  }
})

const statusOption = computed<EChartsOption>(() => {
  const s = overview.value?.status_codes ?? {}
  return {
    tooltip: { trigger: 'item' },
    legend: { bottom: 0 },
    series: [
      {
        type: 'pie',
        radius: ['48%', '72%'],
        center: ['50%', '45%'],
        label: { formatter: '{b}\n{d}%' },
        data: [
          { name: '2xx', value: s['2xx'] ?? 0, itemStyle: { color: '#10b981' } },
          { name: '3xx', value: s['3xx'] ?? 0, itemStyle: { color: '#0ea5a4' } },
          { name: '4xx', value: s['4xx'] ?? 0, itemStyle: { color: '#f59e0b' } },
          { name: '5xx', value: s['5xx'] ?? 0, itemStyle: { color: '#ef4444' } },
        ],
      },
    ],
  }
})

async function loadOverview() {
  try {
    overview.value = await api.get<DashboardOverview>('/dashboard/overview')
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '加载仪表盘数据失败')
  }
  try {
    const info = await api.get<{ engine: EngineStatus }>('/system/info')
    engine.value = info.engine
  } catch {
    // Engine status is supplementary; keep the dashboard metrics usable.
    engine.value = undefined
  }
}

async function loadTraffic() {
  try {
    const data = await api.get<{ series: TrafficPoint[] }>(`/dashboard/traffic?range=${range.value}`)
    series.value = data.series ?? []
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '加载流量数据失败')
  }
}

async function loadAll() {
  await Promise.all([loadOverview(), loadTraffic()])
}

onMounted(() => {
  loadAll()
  timer = window.setInterval(() => {
    if (document.visibilityState === 'visible') loadAll()
  }, 20_000)
  document.addEventListener('visibilitychange', onVisibilityChange)
})
onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
  document.removeEventListener('visibilitychange', onVisibilityChange)
})

function onVisibilityChange() {
  if (document.visibilityState === 'visible') loadAll()
}
</script>
