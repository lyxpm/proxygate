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
        <span class="updated-at">更新于 {{ updatedLabel }}</span>
      </div>
      <div class="toolbar-right">
        <el-radio-group v-model="range" size="small" @change="loadAll">
          <el-radio-button value="1h">1 小时</el-radio-button>
          <el-radio-button value="6h">6 小时</el-radio-button>
          <el-radio-button value="24h">24 小时</el-radio-button>
          <el-radio-button value="7d">7 天</el-radio-button>
          <el-radio-button value="30d">30 天</el-radio-button>
        </el-radio-group>
        <el-button :icon="Refresh" circle size="small" :loading="loading" :disabled="loading" @click="loadAll" />
      </div>
    </div>

    <el-alert v-if="error" type="error" :closable="false" show-icon :title="error" class="page-error" />

    <div class="stat-grid">
      <MetricCard
        label="站点"
        :value="formatNumber(overview?.sites.total ?? 0)"
        :helper="`${overview?.enabled_sites ?? 0} 启用 · ${overview?.sites.healthy ?? 0} 健康`"
      />
      <MetricCard
        label="应用服务"
        :value="`${overview?.applications.nodes_healthy ?? 0} / ${overview?.applications.nodes_total ?? 0}`"
        :helper="`${overview?.applications.groups ?? 0} 个应用服务`"
      />
      <MetricCard
        label="SSL 证书"
        :value="formatNumber(overview?.certificates.total ?? 0)"
        :tone="(overview?.certificates.expiring ?? 0) > 0 ? 'warning' : 'default'"
        :helper="(overview?.certificates.expiring ?? 0) > 0 ? `${overview?.certificates.expiring} 张 30 天内到期` : '暂无即将到期证书'"
      />
      <MetricCard
        label="今日请求"
        :value="formatNumber(overview?.today.requests ?? 0)"
        :helper="`平均 ${overview?.today.avg_ms ?? 0} ms · P95 ${overview?.today.p95_ms ?? 0} ms`"
      />
      <MetricCard label="今日流量" :value="`↓ ${formatBytes(overview?.today.inbound_bytes ?? 0)}`" :helper="`↑ ${formatBytes(overview?.today.outbound_bytes ?? 0)} 出站`" />
    </div>

    <div class="dashboard-analytics">
      <ChartCard
        title="请求趋势"
        :subtitle="`${rangeLabel(range)} · 共 ${formatNumber(rangeRequests)} 次请求`"
        :loading="loading"
        :empty="rangeEmpty"
        empty-text="所选时间范围内暂无请求"
      >
        <TrendChart :points="series" metric="requests" :range="range" height="300px" />
      </ChartCard>

      <ChartCard title="HTTP 状态码分布" :subtitle="rangeLabel(range)" :loading="loading" :empty="statusEmpty" empty-text="所选时间范围内暂无请求">
        <StatusDonut :totals="statusTotals" />
      </ChartCard>
    </div>

    <div class="dashboard-analytics">
      <ChartCard
        title="流量趋势"
        :subtitle="`${rangeLabel(range)} · 入站 + 出站`"
        :loading="loading"
        :empty="rangeEmpty"
        empty-text="所选时间范围内暂无流量"
      >
        <TrendChart :points="series" metric="traffic" :range="range" height="300px" />
      </ChartCard>

      <ChartCard title="站点排行" :subtitle="`${rangeLabel(range)} · 按请求数排序`" :loading="loading" :empty="!ranking.length" empty-text="所选时间范围内暂无站点流量">
        <SiteRanking :items="ranking" :limit="8" />
      </ChartCard>
    </div>

    <div class="card dashboard-events">
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
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import MetricCard from '@/components/MetricCard.vue'
import ChartCard from '@/components/ChartCard.vue'
import TrendChart from '@/components/TrendChart.vue'
import StatusDonut from '@/components/StatusDonut.vue'
import SiteRanking from '@/components/SiteRanking.vue'
import { api, formatBytes, formatNumber, formatTime } from '@/api/client'
import { categoryLabel, levelLabel, levelTagType, messageLabel } from '@/utils/labels'
import type { DashboardOverview, SiteTrafficStat, Totals, TrafficPoint } from '@/api/types'
import { rangeLabel, rangeRequests as sumRangeRequests, statusTotal, type RangeKey } from '@/utils/chartTheme'
import { useSystemStore } from '@/stores/system'

const system = useSystemStore()
const overview = ref<DashboardOverview>()
const engine = computed(() => system.info?.engine)
const series = ref<TrafficPoint[]>([])
const statusTotals = ref<Totals>()
const ranking = ref<SiteTrafficStat[]>([])
const range = ref<RangeKey>('24h')
const loading = ref(false)
const error = ref('')
const lastUpdated = ref(0)
let timer: number | undefined
let requestSeq = 0

const rangeRequests = computed(() => sumRangeRequests(series.value))
const rangeEmpty = computed(() => !loading.value && rangeRequests.value === 0)
const statusEmpty = computed(() => !loading.value && statusTotal(statusTotals.value) === 0)
const updatedLabel = computed(() => (lastUpdated.value ? new Date(lastUpdated.value).toLocaleTimeString('zh-CN', { hour12: false }) : '尚未更新'))

async function loadAll() {
  const seq = ++requestSeq
  loading.value = true
  error.value = ''
  const results = await Promise.allSettled([
    api.get<DashboardOverview>('/dashboard/overview'),
    api.get<{ series: TrafficPoint[] }>(`/dashboard/traffic?range=${range.value}`),
    api.get<{ totals: Totals }>(`/statistics/status-codes?range=${range.value}`),
    api.get<{ items: SiteTrafficStat[] }>(`/statistics/sites?range=${range.value}`),
  ])
  if (seq !== requestSeq) return

  const failures: string[] = []
  if (results[0].status === 'fulfilled') overview.value = results[0].value
  else failures.push('概览')
  if (results[1].status === 'fulfilled') series.value = results[1].value.series ?? []
  else failures.push('趋势')
  if (results[2].status === 'fulfilled') statusTotals.value = results[2].value.totals
  else failures.push('状态码')
  if (results[3].status === 'fulfilled') ranking.value = results[3].value.items ?? []
  else failures.push('站点排行')

  if (failures.length) {
    error.value = `${failures.join('、')}数据加载失败，已保留上一次成功数据`
    ElMessage.error(error.value)
  } else {
    lastUpdated.value = Date.now()
  }
  loading.value = false
}

onMounted(() => {
  loadAll()
  void system.fetchInfo()
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
