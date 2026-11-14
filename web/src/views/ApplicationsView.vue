<template>
  <div class="page">
    <div class="page-toolbar">
      <div class="toolbar-left">
        <el-input v-model="query" placeholder="搜索应用服务" clearable style="width: 240px" :prefix-icon="Search" />
      </div>
      <div class="toolbar-right">
        <el-button :icon="Refresh" @click="load">刷新</el-button>
        <el-button type="primary" :icon="Plus" @click="openCreate">新建应用服务</el-button>
      </div>
    </div>

    <div class="card">
      <el-table v-loading="loading" :data="filtered">
        <el-table-column label="应用服务" min-width="170">
          <template #default="{ row, $index }">
            <div style="font-weight: 600">{{ row.name }}</div>
            <div class="muted tiny">#{{ $index + 1 }}</div>
          </template>
        </el-table-column>
        <el-table-column label="策略" width="130">
          <template #default="{ row }">
            <el-tag size="small" effect="plain">{{ strategyLabel(row.strategy) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="后端节点" min-width="240">
          <template #default="{ row }">
            <div v-for="n in row.nodes.slice(0, 3)" :key="n.id" class="tiny" style="margin: 2px 0">
              <span class="status-dot" :class="n.healthy ? 'ok' : 'err'"></span>
              <span class="code">{{ n.address }}</span>
              <span class="muted"> · 权重 {{ n.weight }} · 优先级 {{ n.priority }}</span>
            </div>
            <span v-if="row.nodes.length > 3" class="muted tiny">还有 {{ row.nodes.length - 3 }} 个节点</span>
            <span v-if="!row.nodes.length" class="muted">暂无节点</span>
          </template>
        </el-table-column>
        <el-table-column label="健康检查" width="150">
          <template #default="{ row }">
            <el-tag v-if="row.health_check_enabled" size="small" type="success" effect="plain">
              {{ row.health_check_type.toUpperCase() }} · {{ row.health_check_interval }}s
            </el-tag>
            <el-tag v-else size="small" type="info" effect="plain">未启用</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="启用" width="90">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'info'" size="small" effect="plain">{{ row.enabled ? '是' : '否' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="140" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" size="small" @click="openEdit(row)">编辑</el-button>
            <el-button link type="danger" size="small" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="dialog" class="app-dialog" align-center draggable :close-on-click-modal="false" :title="editing ? '编辑应用服务' : '新建应用服务'" width="720px" destroy-on-close>
      <el-form label-position="top">
        <div v-if="editingCode" class="app-code">{{ editingCode }}</div>
        <el-row :gutter="16">
          <el-col :span="12">
            <el-form-item label="名称" required>
              <el-input v-model="form.name" placeholder="例如：web-pool" />
            </el-form-item>
          </el-col>
          <el-col :span="6">
            <el-form-item label="负载均衡策略">
              <el-select v-model="form.strategy" style="width: 100%">
                <el-option label="轮询" value="round_robin" />
                <el-option label="加权轮询" value="weighted_round_robin" />
                <el-option label="主备切换" value="failover" />
                <el-option label="IP Hash" value="ip_hash" />
                <el-option label="最少连接" value="least_connections" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="6">
            <el-form-item label="启用">
              <el-switch v-model="form.enabled" />
            </el-form-item>
          </el-col>
        </el-row>
        <el-divider content-position="left">健康检查</el-divider>
        <el-row :gutter="16">
          <el-col :span="6">
            <el-form-item label="启用检查"><el-switch v-model="form.health_check_enabled" /></el-form-item>
          </el-col>
          <el-col :span="6">
            <el-form-item label="类型">
              <el-select v-model="form.health_check_type" :disabled="!form.health_check_enabled">
                <el-option label="HTTP" value="http" />
                <el-option label="TCP" value="tcp" />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="检查路径">
              <el-input v-model="form.health_check_path" :disabled="!form.health_check_enabled || form.health_check_type === 'tcp'" placeholder="/health" />
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="16">
          <el-col :span="6">
            <el-form-item label="间隔（秒）"><el-input-number v-model="form.health_check_interval" :min="1" :max="3600" style="width: 100%" /></el-form-item>
          </el-col>
          <el-col :span="6">
            <el-form-item label="超时（秒）"><el-input-number v-model="form.health_check_timeout" :min="1" :max="60" style="width: 100%" /></el-form-item>
          </el-col>
          <el-col :span="6">
            <el-form-item label="恢复阈值"><el-input-number v-model="form.healthy_threshold" :min="1" :max="10" style="width: 100%" /></el-form-item>
          </el-col>
          <el-col :span="6">
            <el-form-item label="摘除阈值"><el-input-number v-model="form.unhealthy_threshold" :min="1" :max="20" style="width: 100%" /></el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="16">
          <el-col :span="12">
            <el-form-item label="被动失败次数"><el-input-number v-model="form.passive_failure_limit" :min="1" :max="20" style="width: 100%" /></el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="被动冷却（秒）"><el-input-number v-model="form.passive_cooldown" :min="5" :max="3600" style="width: 100%" /></el-form-item>
          </el-col>
        </el-row>
        <el-divider content-position="left">后端节点</el-divider>
          <div class="initial-node-grid">
            <span class="muted tiny">地址</span>
            <span class="muted tiny">权重</span>
            <span class="muted tiny">优先级</span>
            <span class="muted tiny">启用</span>
            <span class="muted tiny">TLS</span>
            <span></span>
            <template v-for="(n, i) in newNodes" :key="n.id || `new-${i}`">
              <el-input v-model="n.address" placeholder="10.0.0.10:8080" />
              <el-input-number v-model="n.weight" :min="1" :max="1000" controls-position="right" style="width: 100%" />
              <el-input-number v-model="n.priority" :min="0" :max="100" controls-position="right" style="width: 100%" />
              <el-checkbox v-model="n.enabled" />
              <el-checkbox v-model="n.tls_verify" />
              <el-button :icon="Delete" text type="danger" :disabled="newNodes.length === 1" @click="newNodes.splice(i, 1)" />
            </template>
          </div>
          <el-button text type="primary" :icon="Plus" style="margin-top: 8px" @click="newNodes.push({ address: '', weight: 1, priority: 0, enabled: true, tls_verify: true })">
            添加节点
          </el-button>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="submit">保存</el-button>
      </template>
    </el-dialog>


  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete, Plus, Refresh, Search } from '@element-plus/icons-vue'
import { api } from '@/api/client'
import type { Page, Application, ApplicationInput } from '@/api/types'

const applications = ref<Application[]>([])
const loading = ref(false)
const query = ref('')
const dialog = ref(false)
const editing = ref(false)
const editingId = ref('')
const editingCode = ref('')
const saving = ref(false)
const newNodes = ref<Array<{ id?: number; address: string; weight: number; priority: number; enabled?: boolean; tls_verify?: boolean }>>([
  { address: '', weight: 1, priority: 0, enabled: true, tls_verify: true },
])

const blankForm = (): ApplicationInput => ({
  name: '',
  strategy: 'round_robin',
  enabled: true,
  health_check_enabled: true,
  health_check_type: 'http',
  health_check_path: '/',
  health_check_interval: 10,
  health_check_timeout: 3,
  healthy_threshold: 2,
  unhealthy_threshold: 3,
  passive_failure_limit: 3,
  passive_cooldown: 30,
})
const form = reactive<ApplicationInput>(blankForm())

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  return q ? applications.value.filter((u) => u.name.toLowerCase().includes(q)) : applications.value
})

function strategyLabel(strategy: string) {
  return ({ round_robin: '轮询', weighted_round_robin: '加权轮询', failover: '主备切换', ip_hash: 'IP Hash', least_connections: '最少连接' } as Record<string, string>)[strategy] || strategy
}

async function load() {
  loading.value = true
  try {
    const data = await api.get<Page<Application>>('/applications?page=1&page_size=500')
    applications.value = data.items ?? []
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '加载应用服务失败')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = false
  editingId.value = ''
  editingCode.value = ''
  Object.assign(form, blankForm())
  newNodes.value = [{ address: '', weight: 1, priority: 0, enabled: true, tls_verify: true }]
  dialog.value = true
}

function openEdit(row: Application) {
  editing.value = true
  editingId.value = row.code
  editingCode.value = row.code
  Object.assign(form, {
    name: row.name,
    strategy: row.strategy,
    enabled: row.enabled,
    health_check_enabled: row.health_check_enabled,
    health_check_type: row.health_check_type,
    health_check_path: row.health_check_path,
    health_check_interval: row.health_check_interval,
    health_check_timeout: row.health_check_timeout,
    healthy_threshold: row.healthy_threshold,
    unhealthy_threshold: row.unhealthy_threshold,
    passive_failure_limit: row.passive_failure_limit,
    passive_cooldown: row.passive_cooldown,
  })
  newNodes.value = row.nodes.map((n) => ({
    id: n.id,
    address: n.address,
    weight: n.weight,
    priority: n.priority,
    enabled: n.enabled,
    tls_verify: n.tls_verify,
  }))
  dialog.value = true
}

async function submit() {
  if (!form.name.trim()) return ElMessage.warning('请填写名称')
  const nodes = newNodes.value
    .filter((n) => n.address.trim())
    .map((n) => ({
      id: n.id,
      address: n.address.trim(),
      weight: n.weight,
      priority: n.priority,
      enabled: n.enabled ?? true,
      tls_verify: n.tls_verify ?? true,
    }))
  if (!nodes.length) return ElMessage.warning('请填写至少一个节点地址')
  saving.value = true
  try {
    if (editing.value) {
      await api.put(`/applications/${editingId.value}`, { ...form, nodes })
      ElMessage.success('应用服务已保存')
    } else {
      await api.post('/applications', { ...form, nodes: nodes.map(({ id, ...node }) => node) })
      ElMessage.success('应用服务已创建')
    }
    dialog.value = false
    await load()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '保存失败')
  } finally {
    saving.value = false
  }
}

async function remove(row: Application) {
  try {
    await ElMessageBox.confirm(`确认删除应用服务 “${row.name}”？请先解除关联站点。`, '删除应用服务', { type: 'warning' })
    await api.del(`/applications/${row.code}`)
    ElMessage.success('已删除')
    await load()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '删除失败')
  }
}

onMounted(load)
</script>
