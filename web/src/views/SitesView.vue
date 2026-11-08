<template>
  <div class="page">
    <div class="page-toolbar">
      <div class="toolbar-left">
        <el-input v-model="query" placeholder="搜索站点名称或域名" clearable style="width: 260px" :prefix-icon="Search" @input="page = 1" />
        <el-select v-model="enabledFilter" style="width: 130px" @change="page = 1">
          <el-option label="全部状态" value="all" />
          <el-option label="已启用" value="true" />
          <el-option label="已禁用" value="false" />
        </el-select>
      </div>
      <div class="toolbar-right">
        <el-button :icon="Refresh" @click="load">刷新</el-button>
        <el-button :icon="Upload" @click="openImport">导入站点</el-button>
        <el-button type="primary" :icon="Plus" @click="openEditor()">新建站点</el-button>
      </div>
    </div>

    <div class="card">
      <el-table v-loading="loading" :data="paged" style="width: 100%">
        <el-table-column label="站点" min-width="190">
          <template #default="{ row }">
            <div style="font-weight: 600">{{ row.name }}</div>
            <div class="muted tiny" style="margin-top: 2px">#{{ row.id }}</div>
          </template>
        </el-table-column>
        <el-table-column label="域名" min-width="240">
          <template #default="{ row }">
            <el-tag v-for="d in row.domains" :key="d" size="small" effect="plain" style="margin: 2px 4px 2px 0">{{ d }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="协议 / 端口" width="130">
          <template #default="{ row }">
            <el-tag :type="row.protocol === 'https' ? 'success' : 'info'" size="small" effect="plain">{{ row.protocol.toUpperCase() }}</el-tag>
            <span class="code" style="margin-left: 6px">:{{ row.listen_port }}</span>
          </template>
        </el-table-column>
        <el-table-column label="应用服务" min-width="140">
          <template #default="{ row }">
            <span>{{ row.application_name || '-' }}</span>
            <div v-if="row.application_id" class="tiny" :style="{ color: applicationHealthy(row.application_id) ? '#10b981' : '#ef4444' }">
              {{ applicationHealthy(row.application_id) ? '应用服务健康' : '应用服务异常' }}
            </div>
          </template>
        </el-table-column>
        <el-table-column label="证书" min-width="150">
          <template #default="{ row }">
            <span v-if="row.certificate_id">{{ row.certificate_name }}</span>
            <span v-else class="muted">-</span>
            <div v-if="row.certificate_expires_at" class="muted tiny">到期 {{ formatTime(row.certificate_expires_at) }}</div>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <span class="status-dot" :class="row.enabled ? (applicationHealthy(row.application_id) ? 'ok' : 'warn') : ''"></span>
            <span>{{ row.enabled ? '已启用' : '已禁用' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="300">
          <template #default="{ row }">
            <el-button link type="primary" size="small" @click="openEditor(row.id)">编辑</el-button>
            <el-button link type="primary" size="small" @click="toggle(row)">{{ row.enabled ? '禁用' : '启用' }}</el-button>
            <el-button link type="info" size="small" @click="viewLogs(row)">日志</el-button>
            <el-button link type="info" size="small" @click="copySite(row)">复制</el-button>
            <el-button link type="info" size="small" @click="exportSite(row)">导出</el-button>
            <el-button link type="danger" size="small" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="pagination">
        <el-pagination
          v-model:current-page="page"
          :page-size="pageSize"
          :total="filtered.length"
          layout="total, prev, pager, next"
          background
        />
      </div>
    </div>

    <el-dialog v-model="importDialog" title="导入站点配置" width="640px" destroy-on-close>
      <el-form label-position="top">
        <el-form-item label="配置文件">
          <el-upload :auto-upload="false" :show-file-list="false" accept="application/json,.json" :on-change="onImportFile">
            <el-button :icon="Upload">选择 JSON 文件</el-button>
          </el-upload>
        </el-form-item>
        <el-form-item label="站点名称（可选，覆盖导入文件中的名称）">
          <el-input v-model="importName" placeholder="留空使用配置文件中的名称" />
        </el-form-item>
        <el-form-item label="配置 JSON">
          <el-input v-model="importJSON" type="textarea" :rows="10" placeholder="粘贴导出的站点 JSON" />
        </el-form-item>
        <el-alert type="info" :closable="false" show-icon title="应用服务与证书会优先按 ID 匹配，找不到时按名称匹配；同名站点需先改名。" />
      </el-form>
      <template #footer>
        <el-button @click="importDialog = false">取消</el-button>
        <el-button type="primary" :loading="importing" @click="submitImport">导入</el-button>
      </template>
    </el-dialog>

    <SiteEditorDialog
      v-model="editorOpen"
      :site-id="editingId"
      :template="editorTemplate"
      @saved="load"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, type UploadFile } from 'element-plus'
import { Plus, Refresh, Search, Upload } from '@element-plus/icons-vue'
import { api, formatTime } from '@/api/client'
import type { Page, Site } from '@/api/types'
import SiteEditorDialog from './SiteEditView.vue'

const router = useRouter()
const editorOpen = ref(false)
const editingId = ref(0)
const editorTemplate = ref<Record<string, any> | null>(null)
const sites = ref<Site[]>([])
const loading = ref(false)
const query = ref('')
const enabledFilter = ref('all')
const page = ref(1)
const pageSize = 20
const healthyApplications = ref<Set<number>>(new Set())
const importDialog = ref(false)
const importJSON = ref('')
const importName = ref('')
const importing = ref(false)

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return sites.value.filter((s) => {
    if (enabledFilter.value === 'true' && !s.enabled) return false
    if (enabledFilter.value === 'false' && s.enabled) return false
    if (!q) return true
    return s.name.toLowerCase().includes(q) || s.domains.some((d) => d.toLowerCase().includes(q))
  })
})

const paged = computed(() => filtered.value.slice((page.value - 1) * pageSize, page.value * pageSize))

function applicationHealthy(id: number): boolean {
  return healthyApplications.value.has(id)
}

async function load() {
  loading.value = true
  try {
    const data = await api.get<Page<Site>>('/sites?page=1&page_size=500')
    sites.value = data.items ?? []
    const stats = await api.get<{ applications: { id: number; nodes: { healthy: boolean }[] }[] }>('/statistics/applications')
    const set = new Set<number>()
    for (const up of stats.applications ?? []) {
      if (up.nodes.some((n) => n.healthy)) set.add(up.id)
    }
    healthyApplications.value = set
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '加载站点失败')
  } finally {
    loading.value = false
  }
}

async function toggle(row: Site) {
  try {
    await api.post(`/sites/${row.id}/${row.enabled ? 'disable' : 'enable'}`)
    ElMessage.success(row.enabled ? '站点已禁用' : '站点已启用')
    await load()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '操作失败')
  }
}

async function remove(row: Site) {
  try {
    await ElMessageBox.confirm(`确认删除站点 “${row.name}”？该操作不可撤销。`, '删除站点', { type: 'warning' })
    await api.del(`/sites/${row.id}`)
    ElMessage.success('站点已删除')
    await load()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '删除失败')
  }
}

function openEditor(id = 0) {
  editingId.value = id
  editorTemplate.value = null
  editorOpen.value = true
}

function viewLogs(row: Site) {
  router.push({ path: '/logs', query: { site_id: String(row.id) } })
}

function openImport() {
  importJSON.value = ''
  importName.value = ''
  importDialog.value = true
}

async function onImportFile(file: UploadFile) {
  if (!file.raw) return
  importJSON.value = await file.raw.text()
}

async function submitImport() {
  if (!importJSON.value.trim()) return ElMessage.warning('请选择或粘贴站点配置 JSON')
  let payload: Record<string, unknown>
  try {
    payload = JSON.parse(importJSON.value) as Record<string, unknown>
  } catch {
    return ElMessage.error('JSON 格式不正确')
  }
  if (importName.value.trim()) payload.name = importName.value.trim()
  importing.value = true
  try {
    await api.post('/sites/import', payload)
    ElMessage.success('站点已导入')
    importDialog.value = false
    await load()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '导入失败')
  } finally {
    importing.value = false
  }
}

async function copySite(row: Site) {
  try {
    const data = await api.get<unknown>(`/sites/${row.id}/export`)
    editorTemplate.value = data as Record<string, any>
    editingId.value = 0
    editorOpen.value = true
    ElMessage.info('已载入配置，请修改域名后保存')
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '复制失败')
  }
}

async function exportSite(row: Site) {
  try {
    const data = await api.get<unknown>(`/sites/${row.id}/export`)
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `site-${row.name.replace(/[^\w.-]+/g, '-')}.json`
    a.click()
    URL.revokeObjectURL(url)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '导出失败')
  }
}

onMounted(load)
</script>
