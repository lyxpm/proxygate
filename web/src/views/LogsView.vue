<template>
  <div class="page">
    <div class="page-toolbar">
      <div class="toolbar-left">
        <el-radio-group v-model="tab" @change="onTabChange">
          <el-radio-button value="access">访问日志</el-radio-button>
          <el-radio-button value="system">系统日志</el-radio-button>
          <el-radio-button value="audit">操作审计</el-radio-button>
        </el-radio-group>
      </div>
      <div class="toolbar-right">
        <template v-if="tab === 'access'">
          <el-switch v-model="live" active-text="实时" @change="toggleLive" />
          <el-button :type="paused ? 'warning' : 'default'" size="small" @click="paused = !paused">{{ paused ? '继续' : '暂停' }}</el-button>
          <el-button :icon="Download" size="small" @click="download">下载今日日志</el-button>
        </template>
        <el-button :icon="Refresh" size="small" @click="load">刷新</el-button>
      </div>
    </div>

    <template v-if="tab === 'access'">
      <div class="card">
        <div class="card-body" style="padding-bottom: 8px">
          <div class="toolbar-left">
            <el-date-picker v-model="filters.date" type="date" value-format="YYYY-MM-DD" placeholder="按日期查询历史文件" clearable style="width: 190px" @change="loadAccess(1)" />
            <el-input v-model="filters.q" placeholder="URL / 路径" clearable style="width: 220px" @keyup.enter="loadAccess(1)" />
            <el-input v-model="filters.ip" placeholder="客户端 IP" clearable style="width: 170px" @keyup.enter="loadAccess(1)" />
            <el-select v-model="filters.status" placeholder="状态码" clearable style="width: 130px" @change="loadAccess(1)">
              <el-option v-for="s in [200, 301, 302, 400, 401, 403, 404, 499, 500, 502, 503, 504]" :key="s" :label="String(s)" :value="String(s)" />
            </el-select>
            <el-select v-model="filters.site_id" placeholder="站点" clearable style="width: 180px" @change="loadAccess(1)">
              <el-option v-for="s in sites" :key="s.id" :label="s.name" :value="String(s.id)" />
            </el-select>
            <el-button type="primary" @click="loadAccess(1)">查询</el-button>
          </div>
        </div>
        <el-table
          :data="access"
          size="small"
          class="log-table"
          height="calc(100vh - 330px)"
          @row-click="showDetail"
          :row-class-name="rowClass"
        >
          <el-table-column label="时间" width="170">
            <template #default="{ row }">{{ formatTime(row.time) }}</template>
          </el-table-column>
          <el-table-column label="状态" width="80">
            <template #default="{ row }">
              <el-tag :type="statusType(row.status)" size="small" effect="plain">{{ row.status }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="method" label="方法" width="75" />
          <el-table-column label="站点" width="130" show-overflow-tooltip>
            <template #default="{ row }">{{ row.site_name || '-' }}</template>
          </el-table-column>
          <el-table-column label="主机" width="150" show-overflow-tooltip>
            <template #default="{ row }">{{ row.domain }}</template>
          </el-table-column>
          <el-table-column prop="path" label="路径" min-width="240" show-overflow-tooltip />
          <el-table-column prop="client_ip" label="客户端" width="130" />
          <el-table-column label="耗时" width="90">
            <template #default="{ row }">{{ row.duration_ms }} ms</template>
          </el-table-column>
          <el-table-column prop="application" label="后端节点" width="150" show-overflow-tooltip />
        </el-table>
        <div class="pagination">
          <el-pagination v-model:current-page="page" :page-size="pageSize" :total="total" layout="total, prev, pager, next" background @current-change="loadAccess" />
        </div>
      </div>
    </template>

    <template v-if="tab === 'system'">
      <div class="card">
        <div class="card-body">
          <div class="toolbar-left" style="margin-bottom: 10px">
            <el-select v-model="systemLevel" placeholder="级别" clearable style="width: 130px" @change="loadSystem(1)">
              <el-option label="信息" value="info" />
              <el-option label="警告" value="warn" />
              <el-option label="错误" value="error" />
            </el-select>
          </div>
          <el-table :data="systemLogs" size="small">
            <el-table-column label="时间" width="180">
              <template #default="{ row }">{{ formatTime(row.CreatedAt) }}</template>
            </el-table-column>
            <el-table-column label="级别" width="90">
              <template #default="{ row }">
                <el-tag :type="levelTagType(row.Level)" size="small" effect="plain">{{ levelLabel(row.Level) }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="分类" width="120">
              <template #default="{ row }">{{ categoryLabel(row.Category) }}</template>
            </el-table-column>
            <el-table-column label="内容" show-overflow-tooltip>
              <template #default="{ row }">{{ messageLabel(row.Message) }}</template>
            </el-table-column>
          </el-table>
          <div class="pagination">
            <el-pagination v-model:current-page="systemPage" :page-size="pageSize" :total="systemTotal" layout="total, prev, pager, next" background @current-change="loadSystem" />
          </div>
        </div>
      </div>
    </template>

    <template v-if="tab === 'audit'">
      <div class="card">
        <div class="card-body">
          <el-table :data="auditLogs" size="small">
            <el-table-column label="时间" width="180">
              <template #default="{ row }">{{ formatTime(row.CreatedAt) }}</template>
            </el-table-column>
            <el-table-column label="操作" width="170">
              <template #default="{ row }">{{ auditActionLabel(row.Action) }}</template>
            </el-table-column>
            <el-table-column label="资源" width="120">
              <template #default="{ row }">{{ resourceLabel(row.ResourceType) }}</template>
            </el-table-column>
            <el-table-column prop="ResourceID" label="资源 ID" width="90" />
            <el-table-column prop="ClientIP" label="客户端" width="130" />
            <el-table-column label="结果" width="90">
              <template #default="{ row }">
                <el-tag :type="row.Result === 'success' ? 'success' : 'danger'" size="small" effect="plain">{{ resultLabel(row.Result) }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="Detail" label="详情" show-overflow-tooltip />
          </el-table>
          <div class="pagination">
            <el-pagination v-model:current-page="auditPage" :page-size="pageSize" :total="auditTotal" layout="total, prev, pager, next" background @current-change="loadAudit" />
          </div>
        </div>
      </div>
    </template>

    <el-drawer v-model="detailDrawer" title="请求详情" size="560px">
      <pre v-if="detail" style="white-space: pre-wrap; word-break: break-all; font-size: 12px; line-height: 1.7; background: #f8fafc; padding: 16px; border-radius: 8px">{{ JSON.stringify(detail, null, 2) }}</pre>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Download, Refresh } from '@element-plus/icons-vue'
import { api, formatTime } from '@/api/client'
import { auditActionLabel, categoryLabel, levelLabel, levelTagType, messageLabel, resourceLabel, resultLabel } from '@/utils/labels'
import type { AccessLog, AuditLog, Page, Site, SystemLog } from '@/api/types'

const route = useRoute()
const tab = ref('access')
const live = ref(false)
const paused = ref(false)
const access = ref<AccessLog[]>([])
const systemLogs = ref<SystemLog[]>([])
const auditLogs = ref<AuditLog[]>([])
const sites = ref<Site[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const systemPage = ref(1)
const systemTotal = ref(0)
const systemLevel = ref('')
const auditPage = ref(1)
const auditTotal = ref(0)
const detailDrawer = ref(false)
const detail = ref<AccessLog>()
const filters = reactive({
  q: '',
  ip: '',
  status: '',
  site_id: (route.query.site_id as string) || '',
  date: '',
})
let es: EventSource | undefined

function statusType(status: number) {
  if (status >= 500) return 'danger'
  if (status >= 400) return 'warning'
  if (status >= 300) return 'info'
  return 'success'
}

function rowClass({ row }: { row: AccessLog }) {
  return row.status >= 500 ? 'log-row-error' : ''
}

async function loadAccess(p = page.value) {
  page.value = p
  const params = new URLSearchParams({ page: String(p), page_size: String(pageSize) })
  if (filters.q) params.set('q', filters.q)
  if (filters.ip) params.set('ip', filters.ip)
  if (filters.status) params.set('status', filters.status)
  if (filters.site_id) params.set('site_id', filters.site_id)
  if (filters.date) params.set('date', filters.date)
  try {
    const data = await api.get<Page<AccessLog>>(`/logs/access?${params}`)
    access.value = data.items ?? []
    total.value = data.total
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '加载访问日志失败')
  }
}

async function loadSystem(p = systemPage.value) {
  systemPage.value = p
  const params = new URLSearchParams({ page: String(p), page_size: String(pageSize) })
  if (systemLevel.value) params.set('level', systemLevel.value)
  try {
    const data = await api.get<Page<SystemLog>>(`/logs/system?${params}`)
    systemLogs.value = data.items ?? []
    systemTotal.value = data.total
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '加载系统日志失败')
  }
}

async function loadAudit(p = auditPage.value) {
  auditPage.value = p
  const params = new URLSearchParams({ page: String(p), page_size: String(pageSize) })
  try {
    const data = await api.get<Page<AuditLog>>(`/logs/audit?${params}`)
    auditLogs.value = data.items ?? []
    auditTotal.value = data.total
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '加载审计日志失败')
  }
}

async function load() {
  if (tab.value === 'access') await loadAccess()
  else if (tab.value === 'system') await loadSystem()
  else await loadAudit()
}

function onTabChange() {
  if (tab.value !== 'access' && live.value) {
    live.value = false
    stopLive()
  }
  load()
}

function toggleLive(value: string | number | boolean) {
  if (value) startLive()
  else stopLive()
}

function startLive() {
  if (es) return
  es = new EventSource('/api/v1/events/stream?types=access')
  es.addEventListener('access', (event) => {
    if (paused.value) return
    try {
      const parsed = JSON.parse((event as MessageEvent).data) as { data: AccessLog }
      access.value = [parsed.data, ...access.value].slice(0, 300)
      total.value++
    } catch {
      // ignore malformed event
    }
  })
  es.onerror = () => {
    // EventSource reconnects automatically; close on explicit logout only.
  }
}

function stopLive() {
  es?.close()
  es = undefined
}

function showDetail(row: AccessLog) {
  detail.value = row
  detailDrawer.value = true
}

function download() {
  const suffix = filters.date ? `?date=${encodeURIComponent(filters.date)}` : ''
  window.open(`/api/v1/logs/access/download${suffix}`, '_blank')
}

onMounted(async () => {
  try {
    const data = await api.get<Page<Site>>('/sites?page=1&page_size=500')
    sites.value = data.items ?? []
  } catch {
    sites.value = []
  }
  await loadAccess(1)
  document.addEventListener('visibilitychange', onVisibilityChange)
})
onBeforeUnmount(() => {
  stopLive()
  document.removeEventListener('visibilitychange', onVisibilityChange)
})

function onVisibilityChange() {
  if (document.visibilityState === 'hidden') {
    stopLive()
  } else if (live.value && tab.value === 'access') {
    startLive()
  }
}
</script>
