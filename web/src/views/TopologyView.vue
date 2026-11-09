<template>
  <div class="page">
    <div class="page-toolbar">
      <div class="toolbar-left">
        <el-tag type="info" effect="plain">配置驱动拓扑</el-tag>
        <span class="muted tiny">用户端 → 代理引擎 → 站点 → 应用服务 → 后端节点</span>
      </div>
      <div class="toolbar-right">
        <el-button :icon="Refresh" @click="load()">刷新</el-button>
      </div>
    </div>

    <div class="topo-stats">
      <div class="topo-stat"><span>站点</span><b>{{ siteCount }}</b></div>
      <div class="topo-stat"><span>应用服务</span><b>{{ poolCount }}</b></div>
      <div class="topo-stat"><span>健康节点</span><b>{{ healthyNodes }}<i>/{{ nodeCount }}</i></b></div>
      <div class="topo-stat"><span>QPS</span><b>{{ totalQPS }}</b></div>
    </div>

    <div class="topology-layout">
      <div class="topo-card" v-loading="loading">
        <div class="topo-card-head">
          <div class="topo-legend">
            <span class="legend-item"><i class="legend-line solid"></i>配置连接</span>
            <span class="legend-item"><i class="legend-line dashed"></i>实时流量</span>
          </div>
          <div class="topo-controls">
            <el-radio-group v-model="layout" size="small">
              <el-radio-button value="vertical">竖向</el-radio-button>
              <el-radio-button value="horizontal">横向</el-radio-button>
            </el-radio-group>
            <el-button size="small" :icon="Grid" @click="alignNodes">对齐</el-button>
            <el-switch v-model="showTraffic" size="small" active-text="流量走势" />
          </div>
        </div>
        <div class="topo-wrap">
          <VueFlow
            v-if="flowNodes.length"
            :key="`${layout}-${alignVersion}`"
            :nodes="flowNodes"
            :edges="flowEdges as any"
            :fit-view-on-init="true"
            :min-zoom="0.2"
            :max-zoom="1.6"
            @node-click="onNodeClick"
            @node-drag-stop="onNodeDragStop"
          >
            <template #node-pm="{ data }">
              <div class="topo-node" :class="[`kind-${data.kind}`, data.bad ? 'bad' : '']">
                <span class="node-status" :class="data.bad ? 'bad' : 'ok'"></span>
                <Handle id="top" type="target" :position="Position.Top" />
                <Handle id="bottom" type="source" :position="Position.Bottom" />
                <Handle id="left" type="target" :position="Position.Left" />
                <Handle id="right" type="source" :position="Position.Right" />
                <div class="node-kind">{{ kindLabel(String(data.kind)) }}</div>
                <div class="node-title">{{ data.label }}</div>
                <div class="node-sub">{{ data.sub }}</div>
              </div>
            </template>
            <Background :gap="20" :size="1.2" pattern-color="#dbe4f0" />
            <Controls />
          </VueFlow>
        </div>
      </div>

      <div class="card topo-detail">
        <div class="card-body">
          <div class="card-title"><span>节点详情</span></div>
          <template v-if="selected">
            <div class="detail-head">
              <div>
                <div class="detail-name">{{ selected.data.label }}</div>
                <div class="muted tiny">{{ kindLabel(s(selected.data, 'kind')) }}</div>
              </div>
              <el-tag
                :type="selected.data.bad ? 'danger' : 'success'"
                size="small"
                effect="plain"
              >{{ selected.data.bad ? '异常' : '运行中' }}</el-tag>
            </div>

            <template v-if="selected.data.kind !== 'certificate'">
              <div class="detail-stats">
                <div class="detail-stat"><span>QPS</span><b>{{ metric(selected.data, 'qps') }}</b></div>
                <div class="detail-stat"><span>进行中</span><b>{{ metric(selected.data, 'active') }}</b></div>
                <div class="detail-stat"><span>累计请求</span><b>{{ formatNumber(Number(selected.data.total_requests ?? 0)) }}</b></div>
                <div class="detail-stat"><span>活动 IP</span><b>{{ metric(selected.data, 'active_ips') }}</b></div>
              </div>
              <div class="spark-title">近 60 秒每秒请求数</div>
              <Chart v-if="hasRequestTraffic" :option="requestSpark" height="84px" />
              <div v-else class="spark-empty">暂无流量</div>
              <div class="spark-title">近 60 秒每秒流量</div>
              <Chart v-if="hasByteTraffic" :option="trafficSpark" height="84px" />
              <div v-else class="spark-empty">暂无流量</div>
            </template>

            <dl class="summary-grid" style="grid-template-columns: 82px 1fr; margin-top: 16px">
              <dt>类型</dt><dd>{{ kindLabel(s(selected.data, 'kind')) }}</dd>
              <dt>名称</dt><dd>{{ selected.data.label }}</dd>
              <template v-if="selected.data.kind === 'site'">
                <dt>域名</dt><dd>{{ list(selected.data, 'domains') }}</dd>
                <dt>协议</dt><dd>{{ selected.data.protocol }} :{{ selected.data.port }}</dd>
                <dt>状态</dt><dd>{{ selected.data.enabled ? '已启用' : '已禁用' }} · {{ selected.data.healthy ? '应用服务健康' : '应用服务异常' }}</dd>
              </template>
              <template v-if="selected.data.kind === 'application'">
                <dt>策略</dt><dd>{{ strategyLabel(s(selected.data, 'strategy')) }}</dd>
                <dt>节点</dt><dd>{{ selected.data.healthy }} / {{ selected.data.nodes }} 健康</dd>
              </template>
              <template v-if="selected.data.kind === 'application_node'">
                <dt>地址</dt><dd>{{ selected.data.address }}</dd>
                <dt>权重</dt><dd>{{ selected.data.weight }} · 优先级 {{ selected.data.priority }}</dd>
                <dt>状态</dt><dd>{{ selected.data.healthy ? '健康' : '异常' }}</dd>
              </template>
              <template v-if="selected.data.kind === 'certificate'">
                <dt>域名</dt><dd>{{ list(selected.data, 'domains') }}</dd>
                <dt>状态</dt><dd>{{ selected.data.status }}</dd>
                <dt>到期</dt><dd>{{ formatTime(s(selected.data, 'expires_at')) }}</dd>
              </template>
              <template v-if="selected.data.kind === 'proxy'">
                <dt>运行</dt><dd>{{ selected.data.running ? '运行中' : '未运行' }}</dd>
                <dt>连接</dt><dd>{{ selected.data.connections }}</dd>
              </template>
            </dl>
          </template>
          <el-empty v-else description="点击拓扑节点查看详情" :image-size="80" />
          <el-alert
            style="margin-top: 14px"
            type="info"
            :closable="false"
            show-icon
            title="节点可拖动，滚轮缩放；线宽随 QPS 变化。"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { EChartsOption } from 'echarts'
import { ElMessage } from 'element-plus'
import { Refresh, Grid } from '@element-plus/icons-vue'
import { VueFlow, Handle, Position, MarkerType } from '@vue-flow/core'
import { Background } from '@vue-flow/background'
import { Controls } from '@vue-flow/controls'
import '@vue-flow/core/dist/style.css'
import '@vue-flow/core/dist/theme-default.css'
import '@vue-flow/controls/dist/style.css'
import Chart from '@/components/Chart.vue'
import { api, formatBytes, formatNumber, formatTime } from '@/api/client'

interface TopoNode {
  id: string
  type: string
  label: string
  data: Record<string, unknown>
}
interface TopoEdge {
  id: string
  source: string
  target: string
  type?: string
  animated?: boolean
  qps?: number
}
interface FlowNode {
  id: string
  type: string
  position: { x: number; y: number }
  data: Record<string, unknown>
}
interface FlowEdge {
  id: string
  source: string
  target: string
  sourceHandle?: string
  targetHandle?: string
  animated?: boolean
  type?: string
  label?: string
  pathOptions?: any
  style?: any
  markerEnd?: any
  qps?: number
}

const loading = ref(false)
const nodes = ref<TopoNode[]>([])
const edges = ref<TopoEdge[]>([])
const selected = ref<FlowNode>()
const showTraffic = ref(true)
const layout = ref<'vertical' | 'horizontal'>('vertical')
const alignVersion = ref(0)
const pinned = ref<Record<string, { x: number; y: number }>>({})
watch(layout, () => {
  pinned.value = {}
})

const kindLabel = (kind: string) =>
  ({ internet: '用户端', proxy: '代理引擎', site: '站点', application: '应用服务', application_node: '后端节点', certificate: '证书' }[kind] || kind)
const strategyLabel = (strategy: string) =>
  ({ round_robin: '轮询', weighted_round_robin: '加权轮询', failover: '主备切换', ip_hash: 'IP Hash', least_connections: '最少连接' }[strategy] || strategy || '-')
const s = (data: Record<string, unknown>, key: string) => (data[key] == null ? '' : String(data[key]))
const list = (data: Record<string, unknown>, key: string) => (Array.isArray(data[key]) ? (data[key] as string[]).join(', ') : '')
const metric = (data: Record<string, unknown>, key: string) => {
  const n = Number(data[key] ?? 0)
  return Number.isInteger(n) ? n : n.toFixed(2)
}
const seriesValues = (data: Record<string, unknown>, key: string): number[] => {
  const raw = data[key]
  if (!Array.isArray(raw)) return Array(60).fill(0)
  return raw.map((v) => Number(v ?? 0))
}

function sparkOption(values: number[], color: string, bytes = false): EChartsOption {
  const max = Math.max(1, ...(values.length ? values : [0])) * 1.15
  return {
    animation: false,
    grid: { left: 0, right: 0, top: 8, bottom: 6 },
    tooltip: {
      trigger: 'axis',
      valueFormatter: (v) => (bytes ? formatBytes(Number(v)) : `${v}/s`),
    },
    xAxis: { type: 'category', show: false, boundaryGap: false, data: values.map((_, i) => i) },
    yAxis: { type: 'value', show: false, min: 0, max },
    series: [
      {
        type: 'line',
        data: values,
        smooth: true,
        showSymbol: false,
        lineStyle: { color, width: 2 },
        areaStyle: { color, opacity: 0.1 },
      },
    ],
  }
}

const requestSpark = computed<EChartsOption>(() =>
  sparkOption(selected.value ? seriesValues(selected.value.data, 'requests_series') : [], '#0ea5a4'),
)
const trafficSpark = computed<EChartsOption>(() =>
  sparkOption(selected.value ? seriesValues(selected.value.data, 'bytes_series') : [], '#6366f1', true),
)
const hasRequestTraffic = computed(() =>
  selected.value ? seriesValues(selected.value.data, 'requests_series').some((v) => v > 0) : false,
)
const hasByteTraffic = computed(() =>
  selected.value ? seriesValues(selected.value.data, 'bytes_series').some((v) => v > 0) : false,
)

const siteCount = computed(() => nodes.value.filter((n) => n.type === 'site').length)
const poolCount = computed(() => nodes.value.filter((n) => n.type === 'application').length)
const nodeCount = computed(() => nodes.value.filter((n) => n.type === 'application_node').length)
const healthyNodes = computed(() => nodes.value.filter((n) => n.type === 'application_node' && n.data.healthy === true).length)
const totalQPS = computed(() => {
  const total = nodes.value.filter((n) => n.type === 'site').reduce((sum, n) => sum + Number(n.data.qps ?? 0), 0)
  return Math.round(total * 100) / 100
})

const flowNodes = computed<FlowNode[]>(() => {
  const sites = nodes.value.filter((n) => n.type === 'site')
  const pools = nodes.value.filter((n) => n.type === 'application')
  const poolNodes = nodes.value.filter((n) => n.type === 'application_node')
  const certs = nodes.value.filter((n) => n.type === 'certificate')
  const positions = new Map<string, { x: number; y: number }>()
  const isVertical = layout.value === 'vertical'

  const sitePool = new Map<string, string>()
  const poolChildren = new Map<string, string[]>()
  for (const e of edges.value) {
    if (e.source.startsWith('site-') && e.target.startsWith('application-')) sitePool.set(e.source, e.target)
    if (e.source.startsWith('application-') && e.target.startsWith('node-')) {
      const list = poolChildren.get(e.source) || []
      list.push(e.target)
      poolChildren.set(e.source, list)
    }
  }

  if (isVertical) {
    const mainWidth = Math.max(780, sites.length * 240, pools.length * 240, poolNodes.length * 190)
    const center = mainWidth / 2
    const centeredX = (i: number, count: number, spacing: number) => center + (i - (count - 1) / 2) * spacing
    const siteY = 320
    const poolY = 500
    const nodeY = 680
    positions.set('internet', { x: center, y: 0 })
    positions.set('proxy', { x: center, y: 150 })
    sites.forEach((n, i) => positions.set(n.id, { x: centeredX(i, sites.length, 240), y: siteY }))

    const poolDesired = pools.map((p) => {
      const linked = sites.filter((s) => sitePool.get(s.id) === p.id)
      const xs = linked.map((s) => positions.get(s.id)?.x).filter((x): x is number => x !== undefined)
      return { pool: p, desired: xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : center }
    })
    poolDesired.sort((a, b) => a.desired - b.desired)
    let cursor = -Infinity
    for (const item of poolDesired) {
      const x = Math.max(item.desired, cursor + 240)
      positions.set(item.pool.id, { x, y: poolY })
      cursor = x
    }
    for (const p of pools) {
      const linked = poolChildren.get(p.id) || []
      const px = positions.get(p.id)?.x ?? center
      linked.forEach((id, i) => positions.set(id, { x: px + (i - (linked.length - 1) / 2) * 190, y: nodeY }))
    }
    poolNodes.forEach((n) => {
      if (!positions.has(n.id)) positions.set(n.id, { x: center, y: nodeY })
    })
    certs.forEach((n, i) => positions.set(n.id, { x: mainWidth + 160, y: siteY + i * 110 }))
  } else {
    const mainHeight = Math.max(560, sites.length * 150, pools.length * 150, poolNodes.length * 92)
    const centerY = mainHeight / 2
    const centeredY = (i: number, count: number, spacing: number) => centerY + (i - (count - 1) / 2) * spacing
    positions.set('internet', { x: 0, y: centerY })
    positions.set('proxy', { x: 240, y: centerY })
    sites.forEach((n, i) => positions.set(n.id, { x: 500, y: centeredY(i, sites.length, 150) }))

    const poolDesired = pools.map((p) => {
      const linked = sites.filter((s) => sitePool.get(s.id) === p.id)
      const ys = linked.map((s) => positions.get(s.id)?.y).filter((y): y is number => y !== undefined)
      return { pool: p, desired: ys.length ? ys.reduce((a, b) => a + b, 0) / ys.length : centerY }
    })
    poolDesired.sort((a, b) => a.desired - b.desired)
    let cursor = -Infinity
    for (const item of poolDesired) {
      const y = Math.max(item.desired, cursor + 150)
      positions.set(item.pool.id, { x: 760, y })
      cursor = y
    }
    for (const p of pools) {
      const linked = poolChildren.get(p.id) || []
      const py = positions.get(p.id)?.y ?? centerY
      linked.forEach((id, i) => positions.set(id, { x: 1020, y: py + (i - (linked.length - 1) / 2) * 92 }))
    }
    poolNodes.forEach((n) => {
      if (!positions.has(n.id)) positions.set(n.id, { x: 1020, y: centerY })
    })
    certs.forEach((n, i) => positions.set(n.id, { x: 1240, y: centeredY(i, certs.length, 110) }))
  }

  return nodes.value.map((n) => {
    const bad = n.data.bad === true || n.data.healthy === false || n.data.status === 'error'
    const sub =
      n.type === 'internet'
        ? `公网入口 · ${n.data.qps ?? 0}/s`
        : n.type === 'proxy'
          ? `运行中 · ${n.data.connections ?? 0} 连接 · ${n.data.qps ?? 0}/s`
          : n.type === 'site'
            ? `:${n.data.port} · ${n.data.qps ?? 0}/s`
            : n.type === 'application'
              ? `${n.data.healthy}/${n.data.nodes} 节点 · ${n.data.qps ?? 0}/s`
              : n.type === 'application_node'
                ? `权重 ${n.data.weight} · ${n.data.qps ?? 0}/s`
                : `${n.data.status || ''}`
    return {
      id: n.id,
      type: 'pm',
      position: pinned.value[n.id] || positions.get(n.id) || { x: 0, y: 0 },
      data: { ...n.data, label: n.label, sub, kind: n.data.kind || n.type, bad },
    }
  })
})

const flowEdges = computed<FlowEdge[]>(() => {
  const out: FlowEdge[] = []
  const isVertical = layout.value === 'vertical'
  const mainHandles = isVertical
    ? { sourceHandle: 'bottom', targetHandle: 'top' }
    : { sourceHandle: 'right', targetHandle: 'left' }
  const branches = new Map<string, number>()
  for (const e of edges.value) {
    if (e.target.startsWith('cert-')) continue
    branches.set(e.source, (branches.get(e.source) || 0) + 1)
  }
  for (const e of edges.value) {
    const cert = e.source.startsWith('site-') && e.target.startsWith('cert-')
    if (cert) {
      out.push({
        id: e.id,
        source: e.source,
        target: e.target,
        sourceHandle: 'right',
        targetHandle: 'left',
        type: 'straight',
        style: { stroke: '#8b5cf6', strokeDasharray: '3 5', strokeWidth: 1.6 },
        markerEnd: MarkerType.ArrowClosed,
      })
      continue
    }
    const curved = (branches.get(e.source) || 0) > 1
    const qps = Number(e.qps ?? 0)
    const flowWidth = Math.min(12, 1.2 + Math.log10(1 + Math.max(qps, 0)) * 6)
    const base = {
      id: e.id,
      source: e.source,
      target: e.target,
      ...mainHandles,
      type: curved ? 'default' : 'straight',
      pathOptions: curved ? { curvature: 0.32 } : undefined,
      markerEnd: MarkerType.ArrowClosed,
    }
    out.push({
      ...base,
      style: { stroke: '#94a3b8', strokeWidth: 2.2 },
    })
    if (e.animated && showTraffic.value) {
      out.push({
        ...base,
        id: `${e.id}-traffic`,
        animated: true,
        style: { stroke: '#0ea5a4', strokeDasharray: '5 9', strokeWidth: flowWidth, opacity: 0.95 },
      })
    }
  }
  return out
})

function alignNodes() {
  pinned.value = {}
  alignVersion.value++
}

function onNodeDragStop(event: { node: FlowNode }) {
  pinned.value[event.node.id] = { x: event.node.position.x, y: event.node.position.y }
}

function onNodeClick(event: { node: FlowNode }) {
  selected.value = event.node
}

async function load(silent = false) {
  if (!silent) loading.value = true
  const selectedID = selected.value?.id
  try {
    const data = await api.get<{ nodes: TopoNode[]; edges: TopoEdge[] }>('/topology')
    nodes.value = data.nodes ?? []
    edges.value = data.edges ?? []
    if (selectedID) {
      await nextTick()
      selected.value = flowNodes.value.find((n) => n.id === selectedID) || selected.value
    } else {
      await nextTick()
      selected.value = flowNodes.value.find((n) => n.data.kind === 'site') || flowNodes.value[0]
    }
  } catch (err) {
    if (!silent) ElMessage.error(err instanceof Error ? err.message : '加载拓扑失败')
  } finally {
    if (!silent) loading.value = false
  }
}

let timer: number | undefined
onMounted(async () => {
  await load()
  timer = window.setInterval(() => {
    if (document.visibilityState === 'visible') load(true)
  }, 10000)
  document.addEventListener('visibilitychange', onVisibilityChange)
})
onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
  document.removeEventListener('visibilitychange', onVisibilityChange)
})

function onVisibilityChange() {
  if (document.visibilityState === 'visible') load(true)
}
</script>
