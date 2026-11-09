<template>
  <div class="page">
    <div class="page-toolbar">
      <div class="toolbar-left">
        <el-select v-model="range" style="width: 140px" @change="load">
          <el-option v-for="item in RANGE_OPTIONS" :key="item.value" :label="item.label" :value="item.value" />
        </el-select>
        <el-select v-model="siteId" style="width: 200px" clearable placeholder="全部站点" @change="load">
          <el-option v-for="s in sites" :key="s.id" :label="s.name" :value="String(s.id)" />
        </el-select>
        <span class="updated-at">更新于 {{ updatedLabel }}</span>
      </div>
      <div class="toolbar-right">
        <el-button :icon="Refresh" :loading="loading" :disabled="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <div class="stat-grid">
      <MetricCard
        label="总请求数"
        :value="formatNumber(totals?.requests ?? 0)"
        :helper="`${averageRequestRate(totals?.requests ?? 0, range)} · 平均 ${totals?.avg_ms ?? 0} ms`"
      />
      <MetricCard label="入站流量" :value="formatBytes(totals?.inbound_bytes ?? 0)" helper="请求头 + 请求体" />
      <MetricCard label="出站流量" :value="formatBytes(totals?.outbound_bytes ?? 0)" helper="响应头 + 响应体" />
      <MetricCard
        label="响应时间"
        :value="`${totals?.avg_ms ?? 0} ms`"
        :helper="`P95 ${totals?.p95_ms ?? 0} ms · P99 ${totals?.p99_ms ?? 0} ms`"
      />
      <MetricCard
        label="5xx 错误"
        :value="formatNumber(totals?.status_5xx ?? 0)"
        :tone="(totals?.status_5xx ?? 0) > 0 ? 'danger' : 'default'"
        :helper="`4xx ${formatNumber(totals?.status_4xx ?? 0)}`"
      />
    </div>

    <ChartCard
      title="请求与流量趋势"
      :subtitle="`${rangeLabel(range)}${siteId ? ` · ${selectedSiteName}` : ' · 全部站点'}`"
      :loading="loading"
      :empty="chartEmpty"
      :error="error"
      empty-text="所选时间范围内暂无流量数据"
      @retry="load"
    >
      <template #actions>
        <div class="metric-switch" role="tablist" aria-label="趋势指标">
          <button
            v-for="option in METRIC_OPTIONS"
            :key="option.value"
            type="button"
            role="tab"
            class="metric-switch-button"
            :class="[`is-${option.value}`, { active: metric === option.value }]"
            :aria-selected="metric === option.value"
            @click="metric = option.value"
          >
            <span class="metric-switch-dot" aria-hidden="true"></span>
            {{ option.label }}
          </button>
        </div>
      </template>

      <div class="metric-summary">
        <span v-for="item in summary" :key="item.label" class="metric-summary-item">
          <span class="metric-summary-label">{{ item.label }}</span>
          <b>{{ item.value }}</b>
        </span>
      </div>
      <TrendChart :points="series" :metric="metric" :range="range" height="340px" />
    </ChartCard>

    <div v-if="siteId && (topIPs.length || topPaths.length)" class="grid-2-even">
      <div class="card">
        <div class="card-body">
          <div class="card-title"><span>访问 IP TOP 10</span></div>
          <el-table :data="topIPs" size="small" style="margin-top: 10px">
            <el-table-column type="index" width="50" />
            <el-table-column prop="value" label="客户端 IP" min-width="180" show-overflow-tooltip />
            <el-table-column prop="count" label="请求数" width="110">
              <template #default="{ row }">{{ formatNumber(row.count) }}</template>
            </el-table-column>
          </el-table>
          <div v-if="!topIPs.length" class="empty" style="padding: 24px 0">暂无数据</div>
        </div>
      </div>
      <div class="card">
        <div class="card-body">
          <div class="card-title"><span>热门 URL TOP 10</span></div>
          <el-table :data="topPaths" size="small" style="margin-top: 10px">
            <el-table-column type="index" width="50" />
            <el-table-column prop="value" label="路径" min-width="220" show-overflow-tooltip />
            <el-table-column prop="count" label="请求数" width="110">
              <template #default="{ row }">{{ formatNumber(row.count) }}</template>
            </el-table-column>
          </el-table>
          <div v-if="!topPaths.length" class="empty" style="padding: 24px 0">暂无数据</div>
        </div>
      </div>
    </div>

    <div v-if="siteStats.length" class="card stats-details">
      <div class="card-body">
        <el-collapse>
          <el-collapse-item title="站点流量明细" name="details">
            <el-table :data="siteStats" size="small" style="margin-top: 4px">
              <el-table-column prop="site_name" label="站点" min-width="150" show-overflow-tooltip />
              <el-table-column label="域名" min-width="200" show-overflow-tooltip>
                <template #default="{ row }">{{ (row.domains || []).join(', ') }}</template>
              </el-table-column>
              <el-table-column prop="requests" label="请求数" width="100" align="right">
                <template #default="{ row }">{{ formatNumber(row.requests) }}</template>
              </el-table-column>
              <el-table-column label="入站" width="100" align="right">
                <template #default="{ row }">{{ formatBytes(row.inbound_bytes) }}</template>
              </el-table-column>
              <el-table-column label="出站" width="100" align="right">
                <template #default="{ row }">{{ formatBytes(row.outbound_bytes) }}</template>
              </el-table-column>
              <el-table-column prop="avg_ms" label="平均耗时" width="100" align="right">
                <template #default="{ row }">{{ row.avg_ms }} ms</template>
              </el-table-column>
              <el-table-column prop="status_4xx" label="4xx" width="70" align="right" />
              <el-table-column prop="status_5xx" label="5xx" width="70" align="right" />
            </el-table>
          </el-collapse-item>
        </el-collapse>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import MetricCard from '@/components/MetricCard.vue'
import ChartCard from '@/components/ChartCard.vue'
import TrendChart from '@/components/TrendChart.vue'
import { api, formatBytes, formatNumber } from '@/api/client'
import type { Page, Site, SiteTrafficStat, Totals, TrafficPoint } from '@/api/types'
import {
  RANGE_OPTIONS,
  averageRequestRate,
  formatMetricValue,
  metricPeak,
  rangeLabel,
  type MetricKey,
  type RangeKey,
} from '@/utils/chartTheme'

interface TopItem {
  value: string
  count: number
}

const METRIC_OPTIONS: { value: MetricKey; label: string }[] = [
  { value: 'requests', label: '请求数' },
  { value: 'traffic', label: '流量' },
  { value: 'latency', label: '响应时间' },
]

const route = useRoute()
const range = ref<RangeKey>('24h')
const siteId = ref(typeof route.query.site_id === 'string' ? route.query.site_id : '')
const metric = ref<MetricKey>('requests')
const totals = ref<Totals>()
const series = ref<TrafficPoint[]>([])
const siteStats = ref<SiteTrafficStat[]>([])
const sites = ref<Site[]>([])
const topIPs = ref<TopItem[]>([])
const topPaths = ref<TopItem[]>([])
const loading = ref(false)
const error = ref('')
const lastUpdated = ref(0)
let requestSeq = 0

const selectedSiteName = computed(() => sites.value.find((s) => String(s.id) === siteId.value)?.name ?? '站点')
const updatedLabel = computed(() => (lastUpdated.value ? new Date(lastUpdated.value).toLocaleTimeString('zh-CN', { hour12: false }) : '尚未更新'))
const chartEmpty = computed(() => !loading.value && (totals.value?.requests ?? 0) === 0)

const summary = computed(() => {
  if (!series.value.length) return []
  if (metric.value === 'requests') {
    return [
      { label: '总请求', value: formatNumber(totals.value?.requests ?? 0) },
      { label: '峰值', value: `${formatNumber(metricPeak(series.value, 'requests'))} 次` },
    ]
  }
  if (metric.value === 'traffic') {
    return [
      { label: '入站', value: formatBytes(totals.value?.inbound_bytes ?? 0) },
      { label: '出站', value: formatBytes(totals.value?.outbound_bytes ?? 0) },
      { label: '峰值', value: formatMetricValue(metricPeak(series.value, 'traffic'), 'traffic') },
    ]
  }
  return [
    { label: '平均', value: `${totals.value?.avg_ms ?? 0} ms` },
    { label: 'P95', value: `${totals.value?.p95_ms ?? 0} ms` },
    { label: 'P99', value: `${totals.value?.p99_ms ?? 0} ms` },
  ]
})

function emptyTotals(): Totals {
  return {
    requests: 0,
    inbound_bytes: 0,
    outbound_bytes: 0,
    status_2xx: 0,
    status_3xx: 0,
    status_4xx: 0,
    status_5xx: 0,
    avg_ms: 0,
    max_ms: 0,
    p95_ms: 0,
    p99_ms: 0,
  }
}

async function load() {
  const seq = ++requestSeq
  loading.value = true
  error.value = ''
  try {
    const [overview, ranking] = await Promise.all([
      api.get<{ totals: Totals; series: TrafficPoint[] }>(`/statistics/overview?range=${range.value}`),
      api.get<{ items: SiteTrafficStat[] }>(`/statistics/sites?range=${range.value}`),
    ])
    if (seq !== requestSeq) return

    let nextTotals = overview.totals
    let nextSeries = overview.series ?? []
    const nextSiteStats = ranking.items ?? []

    if (siteId.value) {
      const row = nextSiteStats.find((s) => String(s.site_id) === siteId.value)
      nextTotals = row
        ? {
            ...emptyTotals(),
            requests: row.requests,
            inbound_bytes: row.inbound_bytes,
            outbound_bytes: row.outbound_bytes,
            status_2xx: row.status_2xx,
            status_3xx: row.status_3xx,
            status_4xx: row.status_4xx,
            status_5xx: row.status_5xx,
            avg_ms: row.avg_ms,
            p95_ms: row.p95_ms ?? 0,
            p99_ms: row.p99_ms ?? 0,
          }
        : emptyTotals()
      const [traffic, detail] = await Promise.all([
        api.get<{ series: TrafficPoint[] }>(`/statistics/traffic?range=${range.value}&site_id=${siteId.value}`),
        api.get<{ top_ips: TopItem[]; top_paths: TopItem[] }>(`/sites/${siteId.value}/statistics?range=${range.value}`),
      ])
      if (seq !== requestSeq) return
      nextSeries = traffic.series ?? []
      topIPs.value = detail.top_ips ?? []
      topPaths.value = detail.top_paths ?? []
    } else {
      topIPs.value = []
      topPaths.value = []
    }

    if (seq !== requestSeq) return
    totals.value = nextTotals
    series.value = nextSeries
    siteStats.value = nextSiteStats
    lastUpdated.value = Date.now()
  } catch (err) {
    if (seq === requestSeq) {
      error.value = err instanceof Error ? err.message : '加载统计数据失败'
      ElMessage.error(error.value)
    }
  } finally {
    if (seq === requestSeq) loading.value = false
  }
}

async function loadSites() {
  try {
    const data = await api.get<Page<Site>>('/sites?page=1&page_size=500')
    sites.value = data.items ?? []
  } catch {
    sites.value = []
  }
}

watch(
  () => route.query.site_id,
  (value) => {
    const next = typeof value === 'string' ? value : ''
    if (next !== siteId.value) {
      siteId.value = next
      load()
    }
  },
)

onMounted(async () => {
  await loadSites()
  await load()
})
</script>
