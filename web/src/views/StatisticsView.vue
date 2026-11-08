<template>
  <div class="page">
    <div class="page-toolbar">
      <div class="toolbar-left">
        <el-select v-model="range" style="width: 140px" @change="load">
          <el-option label="最近 1 小时" value="1h" />
          <el-option label="最近 6 小时" value="6h" />
          <el-option label="最近 24 小时" value="24h" />
          <el-option label="最近 7 天" value="7d" />
          <el-option label="最近 30 天" value="30d" />
        </el-select>
        <el-select v-model="siteId" style="width: 200px" clearable placeholder="全部站点" @change="load">
          <el-option v-for="s in sites" :key="s.id" :label="s.name" :value="String(s.id)" />
        </el-select>
      </div>
      <div class="toolbar-right">
        <el-button :icon="Refresh" @click="load">刷新</el-button>
      </div>
    </div>

    <div class="stat-grid">
      <div class="stat-card">
        <div class="stat-label">总请求数</div>
        <div class="stat-value">{{ formatNumber(totals?.requests ?? 0) }}</div>
        <div class="stat-foot">平均 {{ totals?.avg_ms ?? 0 }} ms</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">入站流量</div>
        <div class="stat-value" style="font-size: 22px">{{ formatBytes(totals?.inbound_bytes ?? 0) }}</div>
        <div class="stat-foot">请求头 + 请求体</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">出站流量</div>
        <div class="stat-value" style="font-size: 22px">{{ formatBytes(totals?.outbound_bytes ?? 0) }}</div>
        <div class="stat-foot">响应头 + 响应体</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">响应时间</div>
        <div class="stat-value">{{ totals?.avg_ms ?? 0 }}<span class="muted" style="font-size: 14px"> ms</span></div>
        <div class="stat-foot">P95 {{ totals?.p95_ms ?? 0 }} ms · P99 {{ totals?.p99_ms ?? 0 }} ms</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">5xx 错误</div>
        <div class="stat-value" :style="{ color: (totals?.status_5xx ?? 0) > 0 ? '#ef4444' : undefined }">{{ formatNumber(totals?.status_5xx ?? 0) }}</div>
        <div class="stat-foot">4xx {{ formatNumber(totals?.status_4xx ?? 0) }}</div>
      </div>
    </div>

    <div class="card">
      <div class="card-body">
        <div class="card-title"><span>请求与流量趋势</span></div>
        <Chart :option="seriesOption" height="340px" />
      </div>
    </div>

    <div class="grid-2-even">
      <div class="card">
        <div class="card-body">
          <div class="card-title"><span>状态码分布</span></div>
          <Chart :option="statusOption" height="280px" />
        </div>
      </div>
      <div class="card">
        <div class="card-body">
          <div class="card-title"><span>站点排行</span></div>
          <el-table :data="siteStats.slice(0, 8)" size="small" style="margin-top: 10px">
            <el-table-column prop="site_name" label="站点" min-width="120" />
            <el-table-column prop="requests" label="请求" width="100">
              <template #default="{ row }">{{ formatNumber(row.requests) }}</template>
            </el-table-column>
            <el-table-column label="流量" width="100">
              <template #default="{ row }">{{ formatBytes(row.outbound_bytes) }}</template>
            </el-table-column>
          </el-table>
          <div v-if="!siteStats.length" class="empty" style="padding: 24px 0">暂无数据</div>
        </div>
      </div>
    </div>

    <div v-if="siteId" class="grid-2-even">
      <div class="card">
        <div class="card-body">
          <div class="card-title"><span>访问 IP TOP 10</span></div>
          <el-table :data="topIPs" size="small" style="margin-top: 10px">
            <el-table-column type="index" width="50" />
            <el-table-column prop="value" label="客户端 IP" min-width="180" />
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

    <div class="card">
      <div class="card-body">
        <div class="card-title"><span>站点流量明细</span></div>
        <el-table :data="siteStats" size="small" style="margin-top: 10px">
          <el-table-column prop="site_name" label="站点" min-width="150" />
          <el-table-column label="域名" min-width="200">
            <template #default="{ row }">{{ (row.domains || []).join(', ') }}</template>
          </el-table-column>
          <el-table-column prop="requests" label="请求数" width="110">
            <template #default="{ row }">{{ formatNumber(row.requests) }}</template>
          </el-table-column>
          <el-table-column label="入站" width="110">
            <template #default="{ row }">{{ formatBytes(row.inbound_bytes) }}</template>
          </el-table-column>
          <el-table-column label="出站" width="110">
            <template #default="{ row }">{{ formatBytes(row.outbound_bytes) }}</template>
          </el-table-column>
          <el-table-column prop="avg_ms" label="平均耗时" width="100">
            <template #default="{ row }">{{ row.avg_ms }} ms</template>
          </el-table-column>
          <el-table-column prop="status_4xx" label="4xx" width="80" />
          <el-table-column prop="status_5xx" label="5xx" width="80" />
        </el-table>
        <div v-if="!siteStats.length" class="empty">所选时间范围内暂无流量数据</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import type { EChartsOption } from 'echarts'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import Chart from '@/components/Chart.vue'
import { api, formatBytes, formatNumber } from '@/api/client'
import type { Page, Site, Totals, TrafficPoint } from '@/api/types'

interface SiteStat {
  site_id: number
  site_name: string
  domains: string[]
  requests: number
  inbound_bytes: number
  outbound_bytes: number
  avg_ms: number
  status_2xx: number
  status_3xx: number
  status_4xx: number
  status_5xx: number
}

interface TopItem {
  value: string
  count: number
}

const range = ref('24h')
const siteId = ref('')
const totals = ref<Totals>()
const series = ref<TrafficPoint[]>([])
const siteStats = ref<SiteStat[]>([])
const sites = ref<Site[]>([])
const topIPs = ref<TopItem[]>([])
const topPaths = ref<TopItem[]>([])

const seriesOption = computed<EChartsOption>(() => ({
  tooltip: { trigger: 'axis' },
  legend: { data: ['请求数', '入站流量', '出站流量'], bottom: 0, itemGap: 22, textStyle: { color: '#64748b' } },
  grid: { left: 10, right: 20, top: 24, bottom: 46, containLabel: true },
  xAxis: {
    type: 'category',
    data: series.value.map((p) => new Date(p.time).toLocaleString('zh-CN', { hour12: false, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })),
    axisLabel: { color: '#94a3b8' },
  },
  yAxis: [
    { type: 'value', splitLine: { lineStyle: { color: '#eef2f7' } }, axisLabel: { color: '#94a3b8' } },
    { type: 'value', splitLine: { show: false }, axisLabel: { color: '#94a3b8', formatter: (v: number) => formatBytes(v) } },
  ],
  series: [
    { name: '请求数', type: 'bar', barMaxWidth: 18, data: series.value.map((p) => p.requests), itemStyle: { color: '#0ea5a4', borderRadius: [4, 4, 0, 0] } },
    { name: '入站流量', type: 'line', yAxisIndex: 1, smooth: true, data: series.value.map((p) => p.inbound_bytes), itemStyle: { color: '#6366f1' } },
    { name: '出站流量', type: 'line', yAxisIndex: 1, smooth: true, data: series.value.map((p) => p.outbound_bytes), itemStyle: { color: '#f59e0b' } },
  ],
}))

const statusOption = computed<EChartsOption>(() => ({
  tooltip: { trigger: 'item' },
  legend: { bottom: 0 },
  series: [
    {
      type: 'pie',
      radius: ['45%', '70%'],
      center: ['50%', '45%'],
      data: [
        { name: '2xx', value: totals.value?.status_2xx ?? 0, itemStyle: { color: '#10b981' } },
        { name: '3xx', value: totals.value?.status_3xx ?? 0, itemStyle: { color: '#0ea5a4' } },
        { name: '4xx', value: totals.value?.status_4xx ?? 0, itemStyle: { color: '#f59e0b' } },
        { name: '5xx', value: totals.value?.status_5xx ?? 0, itemStyle: { color: '#ef4444' } },
      ],
    },
  ],
}))

async function load() {
  try {
    const [overview, ranking] = await Promise.all([
      api.get<{ totals: Totals; series: TrafficPoint[] }>(`/statistics/overview?range=${range.value}${siteId.value ? `&site_id=${siteId.value}` : ''}`),
      api.get<{ items: SiteStat[] }>(`/statistics/sites?range=${range.value}`),
    ])
    totals.value = overview.totals
    series.value = overview.series ?? []
    siteStats.value = ranking.items ?? []
    if (siteId.value) {
      // The overview endpoint intentionally aggregates all sites; when a site is
      // selected, use the matching ranking row for the headline totals.
      const row = siteStats.value.find((s) => String(s.site_id) === siteId.value)
      if (row) {
        totals.value = {
          ...(totals.value as Totals),
          requests: row.requests,
          inbound_bytes: row.inbound_bytes,
          outbound_bytes: row.outbound_bytes,
          avg_ms: row.avg_ms,
          status_2xx: row.status_2xx,
          status_3xx: row.status_3xx,
          status_4xx: row.status_4xx,
          status_5xx: row.status_5xx,
        }
      } else {
        totals.value = {
          ...(totals.value as Totals),
          requests: 0,
          inbound_bytes: 0,
          outbound_bytes: 0,
          avg_ms: 0,
          status_2xx: 0,
          status_3xx: 0,
          status_4xx: 0,
          status_5xx: 0,
        }
      }
      const traffic = await api.get<{ series: TrafficPoint[] }>(`/statistics/traffic?range=${range.value}&site_id=${siteId.value}`)
      series.value = traffic.series ?? []
      const siteData = await api.get<{ top_ips: TopItem[]; top_paths: TopItem[] }>(`/sites/${siteId.value}/statistics?range=${range.value}`)
      topIPs.value = siteData.top_ips ?? []
      topPaths.value = siteData.top_paths ?? []
    } else {
      topIPs.value = []
      topPaths.value = []
    }
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '加载统计失败')
  }
}

onMounted(async () => {
  try {
    const data = await api.get<Page<Site>>('/sites?page=1&page_size=500')
    sites.value = data.items ?? []
  } catch {
    sites.value = []
  }
  await load()
})
</script>
