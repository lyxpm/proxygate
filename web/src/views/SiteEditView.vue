<template>
  <el-dialog
    v-model="visible"
    :title="isEdit ? '编辑站点' : '新建站点'"
    width="min(700px, calc(100vw - 32px))"
    align-center
    draggable
    destroy-on-close
    :close-on-click-modal="false"
    :before-close="beforeClose"
    class="site-editor-dialog"
  >
    <template #header>
      <div class="site-dialog-heading">
        <div>
          <div class="site-dialog-title">{{ isEdit ? '编辑站点' : '新建站点' }}</div>
          <div class="site-dialog-subtitle">配置域名、应用服务及代理规则</div>
        </div>
        <div class="site-dialog-head-actions">
          <span class="site-status-label">{{ form.enabled ? '已启用' : '已停用' }}</span>
          <el-switch v-model="form.enabled" />
        </div>
      </div>
    </template>

    <div v-loading="loading" class="site-editor">
      <section class="editor-section">
        <div class="editor-section-title">基本信息</div>
        <el-form label-position="top">
          <div class="site-form-grid">
            <el-form-item label="站点名称" required class="site-name">
              <el-input v-model="form.name" placeholder="主站 / API" maxlength="128" />
            </el-form-item>
            <el-form-item label="协议" required class="site-protocol">
              <el-radio-group v-model="form.protocol" @change="onProtocolChange">
                <el-radio-button value="http">HTTP</el-radio-button>
                <el-radio-button value="https">HTTPS</el-radio-button>
              </el-radio-group>
            </el-form-item>
            <el-form-item label="监听端口" required class="site-port">
              <el-input-number v-model="form.listen_port" :min="1" :max="65535" controls-position="right" style="width: 100%" />
            </el-form-item>
          </div>

          <el-form-item required class="domain-field">
            <template #label>
              <span class="label-with-help">域名
                <el-tooltip content="支持 *.example.com 通配域名与 * 默认站点；输入后按回车或逗号添加。" placement="top">
                  <el-icon class="hint-icon"><InfoFilled /></el-icon>
                </el-tooltip>
              </span>
            </template>
            <div class="domain-tag-input" :class="{ 'has-error': domainError }" @click="focusDomainInput">
              <el-tag
                v-for="domain in domainTags"
                :key="domain"
                closable
                size="small"
                @close="removeDomain(domain)"
                @click.stop="editDomain(domain)"
              >{{ domain }}</el-tag>
              <input
                ref="domainInputRef"
                v-model="domainInput"
                class="domain-tag-field"
                :placeholder="domainTags.length ? '输入域名后回车...' : 'example.com 或 *.example.com'"
                @keydown="onDomainKeydown"
                @paste.prevent="onDomainPaste"
                @blur="commitDomain"
              />
            </div>
            <div v-if="domainError" class="domain-error">{{ domainError }}</div>
          </el-form-item>
        </el-form>
      </section>

      <div class="editor-divider"></div>

      <section class="editor-section">
        <div class="editor-section-title">应用服务</div>
        <el-form label-position="top">
          <el-form-item label="选择应用服务" required>
            <el-select v-model="form.application_id" placeholder="请选择应用服务" style="width: 100%" filterable>
              <el-option
                v-for="app in applications"
                :key="app.id"
                :label="`${app.name}（${app.nodes.length} 个节点）`"
                :value="app.id"
              />
            </el-select>
          </el-form-item>
        </el-form>

        <div v-if="selectedApplication" class="pool-summary">
          <div class="pool-summary-head">
            <div>
              <span class="pool-name">{{ selectedApplication.name }}</span>
              <el-tag size="small" effect="plain" style="margin-left: 8px">{{ strategyLabel(selectedApplication.strategy) }}</el-tag>
            </div>
            <el-button link type="primary" size="small" @click="goTo('/applications')">管理应用服务</el-button>
          </div>
          <div class="tiny muted" style="margin: 4px 0 8px">
            {{ selectedApplication.health_check_enabled ? `健康检查 ${selectedApplication.health_check_type.toUpperCase()} ${selectedApplication.health_check_path}，每 ${selectedApplication.health_check_interval}s` : '未启用健康检查' }}
          </div>
          <el-table :data="selectedApplication.nodes.slice(0, 5)" size="small">
            <el-table-column prop="address" label="节点地址" min-width="220" show-overflow-tooltip />
            <el-table-column prop="weight" label="权重" width="70" />
            <el-table-column prop="priority" label="优先级" width="80" />
            <el-table-column label="状态" width="80">
              <template #default="{ row }">
                <span class="status-dot" :class="row.healthy ? 'ok' : 'err'"></span>{{ row.healthy ? '健康' : '异常' }}
              </template>
            </el-table-column>
          </el-table>
          <div v-if="selectedApplication.nodes.length > 5" class="muted tiny" style="margin-top: 6px">
            仅展示前 5 个节点，共 {{ selectedApplication.nodes.length }} 个
          </div>
        </div>
        <el-alert v-else-if="!applications.length" type="info" :closable="false" show-icon title="还没有可用的应用服务">
          <template #default>
            请先前往 <el-link type="primary" @click="goTo('/applications')">应用服务</el-link> 创建并添加后端节点。
          </template>
        </el-alert>
      </section>

      <div class="editor-divider"></div>

      <section class="editor-section">
        <div class="editor-section-title">SSL 证书</div>
        <template v-if="form.protocol === 'https'">
          <el-form label-position="top">
            <el-form-item label="SSL 证书" required>
              <el-select v-model="form.certificate_id" placeholder="请选择证书" style="width: 100%" filterable>
                <el-option v-for="c in usableCerts" :key="c.id" :label="`${c.name}（${c.domains.join(', ')}）`" :value="c.id" />
              </el-select>
              <div class="form-hint">需覆盖站点全部域名，前往 <el-link type="primary" @click="goTo('/certificates')">SSL 证书</el-link> 申请。</div>
            </el-form-item>
            <el-form-item label="HTTP 自动跳转 HTTPS">
              <el-switch v-model="form.redirect_https" :disabled="form.listen_port !== 443" />
              <span class="form-hint" style="margin-left: 10px">仅监听 443 时可用，将自动监听 80 并跳转。</span>
            </el-form-item>
          </el-form>
        </template>
        <div v-else class="http-cert-placeholder">
          <el-icon :size="20"><Lock /></el-icon>
          <div>HTTP 站点无需证书</div>
        </div>
      </section>

      <div class="editor-divider"></div>

      <el-collapse v-model="advancedOpen">
        <el-collapse-item name="advanced">
          <template #title>
            <div class="advanced-title">
              <span>高级配置</span>
            </div>
          </template>
          <el-form label-position="top" class="advanced-form">
            <div class="advanced-groups">
              <div class="advanced-group">
                <div class="advanced-group-title">超时与请求体</div>
                <div class="advanced-grid">
                  <el-form-item label="请求超时（秒）">
                    <el-input-number v-model="form.proxy_timeout" :min="1" :max="3600" controls-position="right" style="width: 100%" />
                  </el-form-item>
                  <el-form-item label="最大请求体（0 不限制）">
                    <el-input-number v-model="form.max_body_size" :min="0" :max="1073741824" :step="1048576" controls-position="right" style="width: 100%" />
                  </el-form-item>
                  <el-form-item label="Buffer 大小（字节）">
                    <el-input-number v-model="form.options.bufferSize" :min="1024" :max="1048576" :step="1024" controls-position="right" style="width: 100%" />
                  </el-form-item>
                  <el-form-item label="节点 SNI（可选）">
                    <el-input v-model="form.options.upstreamSni" placeholder="默认使用节点地址" />
                  </el-form-item>
                </div>
              </div>

              <div class="advanced-group">
                <div class="advanced-group-title">路径重写</div>
                <div class="advanced-grid">
                  <el-form-item label="剥离路径前缀">
                    <el-input v-model="form.options.stripPrefix" placeholder="例如 /api" />
                  </el-form-item>
                  <el-form-item label="追加路径前缀">
                    <el-input v-model="form.options.addPrefix" placeholder="例如 /v1" />
                  </el-form-item>
                </div>
              </div>

              <div class="advanced-group">
                <div class="advanced-group-title">后端连接</div>
                <div class="advanced-switches">
                  <el-checkbox v-model="form.options.upstreamTlsVerify">校验后端 TLS</el-checkbox>
                  <el-checkbox v-model="form.options.keepAlive">保持长连接</el-checkbox>
                  <el-checkbox v-model="form.options.useUpstreamHost">使用后端 Host 头</el-checkbox>
                  <el-checkbox v-model="form.options.forwardClientIp">传递真实访问 IP</el-checkbox>
                </div>
              </div>

              <div class="advanced-group">
                <div class="advanced-group-title">IP 访问控制</div>
                <div class="advanced-grid">
                  <el-form-item label="模式">
                    <el-select v-model="form.options.ipMode" style="width: 100%">
                      <el-option label="不限制" value="" />
                      <el-option label="仅允许列表" value="allow" />
                      <el-option label="拒绝列表" value="deny" />
                    </el-select>
                  </el-form-item>
                  <el-form-item label="IP / CIDR">
                    <el-input v-model="ipListText" type="textarea" :rows="2" :disabled="!form.options.ipMode" placeholder="127.0.0.1&#10;10.0.0.0/8" />
                  </el-form-item>
                </div>
              </div>

              <div class="advanced-group full">
                <div class="advanced-group-title">自定义请求头</div>
                <div v-for="(h, i) in form.options.requestHeaders" :key="`req-${i}`" class="header-row">
                  <el-input v-model="h.name" placeholder="Header 名称" style="width: 220px" />
                  <el-input v-model="h.value" placeholder="值；留空表示删除该头" />
                  <el-button :icon="Delete" text type="danger" @click="form.options.requestHeaders.splice(i, 1)" />
                </div>
                <el-button text type="primary" :icon="Plus" @click="form.options.requestHeaders.push({ name: '', value: '' })">添加请求头</el-button>
              </div>

              <div class="advanced-group full">
                <div class="advanced-group-title">自定义响应头</div>
                <div v-for="(h, i) in form.options.responseHeaders" :key="`resp-${i}`" class="header-row">
                  <el-input v-model="h.name" placeholder="Header 名称" style="width: 220px" />
                  <el-input v-model="h.value" placeholder="值；留空表示删除该头" />
                  <el-button :icon="Delete" text type="danger" @click="form.options.responseHeaders.splice(i, 1)" />
                </div>
                <el-button text type="primary" :icon="Plus" @click="form.options.responseHeaders.push({ name: '', value: '' })">添加响应头</el-button>
              </div>
            </div>
          </el-form>
        </el-collapse-item>
      </el-collapse>
    </div>

    <template #footer>
      <div class="site-dialog-footer">
        <span class="muted">配置保存后立即生效，无需重启</span>
        <div>
          <el-button @click="closeEditor">取消</el-button>
          <el-button type="primary" :loading="saving" :icon="Check" @click="save">保存并应用</el-button>
        </div>
      </div>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Check, Delete, InfoFilled, Lock, Plus } from '@element-plus/icons-vue'
import { api } from '@/api/client'
import type { Certificate, Page, Site, SiteInput, Application } from '@/api/types'

const props = defineProps<{
  modelValue: boolean
  siteId?: number
  template?: Record<string, any> | null
}>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  saved: []
}>()

const visible = computed({
  get: () => props.modelValue,
  set: (value: boolean) => emit('update:modelValue', value),
})
const siteId = computed(() => props.siteId ?? 0)
const isEdit = computed(() => siteId.value > 0)
const router = useRouter()
const loading = ref(false)
const saving = ref(false)
const applications = ref<Application[]>([])
const certificates = ref<Certificate[]>([])
const advancedOpen = ref<string[]>([])
const domainInput = ref('')
const domainInputRef = ref<HTMLInputElement>()
const domainError = ref('')
const initialSnapshot = ref('')
const dirty = computed(
  () =>
    !saving.value &&
    initialSnapshot.value !== '' &&
    (JSON.stringify(form) !== initialSnapshot.value || domainInput.value.trim() !== ''),
)
const domainTags = computed(() => form.domains.map((d) => d.trim()).filter(Boolean))

const form = reactive<SiteInput>({
  name: '',
  enabled: true,
  listen_port: 80,
  domains: [''],
  protocol: 'http',
  application_id: undefined as unknown as number,
  certificate_id: null,
  redirect_https: false,
  proxy_timeout: 30,
  max_body_size: 0,
  remark: '',
  options: {
    stripPrefix: '',
    addPrefix: '',
    requestHeaders: [],
    responseHeaders: [],
    upstreamSni: '',
    upstreamTlsVerify: true,
    keepAlive: true,
    useUpstreamHost: false,
    forwardClientIp: true,
    bufferSize: 32768,
    ipMode: '',
    ipList: [],
  },
})

function resetForm() {
  Object.assign(form, {
    name: '',
    enabled: true,
    listen_port: 80,
    domains: [''],
    protocol: 'http',
    application_id: undefined,
    certificate_id: null,
    redirect_https: false,
    proxy_timeout: 30,
    max_body_size: 0,
    remark: '',
    options: {
      stripPrefix: '',
      addPrefix: '',
      requestHeaders: [],
      responseHeaders: [],
      upstreamSni: '',
      upstreamTlsVerify: true,
      keepAlive: true,
      useUpstreamHost: false,
      forwardClientIp: true,
      bufferSize: 32768,
      ipMode: '',
      ipList: [],
    },
  })
  domainInput.value = ''
  domainError.value = ''
}

const ipListText = computed({
  get: () => form.options.ipList.join('\n'),
  set: (value: string) => {
    form.options.ipList = value
      .split(/[\n,]/)
      .map((v) => v.trim())
      .filter(Boolean)
  },
})

const selectedApplication = computed(() => applications.value.find((u) => u.id === form.application_id))
const usableCerts = computed(() =>
  certificates.value.filter((c) => c.status === 'valid' || c.id === form.certificate_id),
)

function strategyLabel(strategy: string) {
  return (
    {
      round_robin: '轮询',
      weighted_round_robin: '加权轮询',
      failover: '主备切换',
      ip_hash: 'IP Hash',
      least_connections: '最少连接',
    }[strategy] || strategy
  )
}

function focusDomainInput() {
  domainInputRef.value?.focus()
}

function addDomainValues(raw: string): boolean {
  const values = raw
    .split(/[\n,，]+/)
    .map((v) => v.trim())
    .filter(Boolean)
  if (!values.length) return false
  const next = [...domainTags.value]
  for (const value of values) {
    if (value.includes('://') || value.includes('/') || /\s/.test(value)) {
      domainError.value = '域名不能包含协议、路径或空格'
      return false
    }
    if (next.some((d) => d.toLowerCase() === value.toLowerCase())) {
      domainError.value = `域名 ${value} 已存在`
      return false
    }
    next.push(value)
  }
  form.domains = next
  domainError.value = ''
  return true
}

function commitDomain() {
  const raw = domainInput.value.trim()
  if (!raw) {
    domainInput.value = ''
    domainError.value = ''
    return
  }
  if (addDomainValues(raw)) {
    domainInput.value = ''
  }
}

function onDomainKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' || event.key === ',' || event.key === '，') {
    event.preventDefault()
    commitDomain()
    return
  }
  if (event.key === 'Backspace' && !domainInput.value && domainTags.value.length) {
    event.preventDefault()
    removeDomain(domainTags.value[domainTags.value.length - 1])
  }
  if (event.key === 'Escape') {
    domainInput.value = ''
    domainError.value = ''
  }
}

function onDomainPaste(event: ClipboardEvent) {
  const text = event.clipboardData?.getData('text') || ''
  if (text) {
    domainInput.value = text
    addDomainValues(text)
    domainInput.value = domainError.value ? text : ''
  }
}

function removeDomain(domain: string) {
  form.domains = domainTags.value.filter((d) => d !== domain)
  if (!form.domains.length) form.domains = ['']
  domainError.value = ''
  focusDomainInput()
}

function editDomain(domain: string) {
  removeDomain(domain)
  domainInput.value = domain
  focusDomainInput()
}

function onProtocolChange(value: string | number | boolean) {
  if (value === 'https') {
    form.listen_port = 443
    form.redirect_https = true
  } else {
    form.listen_port = 80
    form.redirect_https = false
    form.certificate_id = null
  }
}

function validate(): boolean {
  commitDomain()
  if (!form.name.trim()) {
    ElMessage.warning('请填写站点名称')
    return false
  }
  if (domainError.value) {
    ElMessage.warning(domainError.value)
    return false
  }
  if (!domainTags.value.length) {
    ElMessage.warning('请至少填写一个域名')
    return false
  }
  if (!form.application_id) {
    ElMessage.warning('请选择应用服务')
    return false
  }
  if (form.protocol === 'https' && !form.certificate_id) {
    ElMessage.warning('HTTPS 站点必须选择 SSL 证书')
    return false
  }
  return true
}

async function load() {
  loading.value = true
  try {
    const [ups, certs] = await Promise.all([
      api.get<Page<Application>>('/applications?page=1&page_size=500'),
      api.get<Page<Certificate>>('/certificates?page=1&page_size=500'),
    ])
    applications.value = ups.items ?? []
    certificates.value = certs.items ?? []
    if (isEdit.value) {
      const site = await api.get<Site>(`/sites/${siteId.value}`)
      Object.assign(form, {
        name: site.name,
        enabled: site.enabled,
        listen_port: site.listen_port,
        domains: site.domains.length ? [...site.domains] : [''],
        protocol: site.protocol,
        application_id: site.application_id,
        certificate_id: site.certificate_id ?? null,
        redirect_https: site.redirect_https,
        proxy_timeout: site.proxy_timeout,
        max_body_size: site.max_body_size,
        remark: site.remark,
        options: {
          ...form.options,
          ...site.options,
          ipMode: site.options.ipMode || '',
          ipList: [...(site.options.ipList ?? [])],
          requestHeaders: [...(site.options.requestHeaders ?? [])],
          responseHeaders: [...(site.options.responseHeaders ?? [])],
        },
      })
      domainInput.value = ''
      domainError.value = ''
      if (site.options.ipMode || site.options.requestHeaders?.length || site.options.responseHeaders?.length) {
        advancedOpen.value = ['advanced']
      }
    } else {
      resetForm()
      if (props.template) applyTemplate(props.template)
    }
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '加载配置失败')
  } finally {
    loading.value = false
    initialSnapshot.value = JSON.stringify(form)
  }
}

function applyTemplate(t: Record<string, any>) {
  form.name = `${String(t.name || '站点')} 副本`
  form.enabled = false
  form.listen_port = Number(t.listen_port) || 80
  form.domains = Array.isArray(t.domains) && t.domains.length ? [...t.domains] : ['']
  form.protocol = t.protocol === 'https' ? 'https' : 'http'
  form.application_id = Number(t.application_id) || 0
  form.certificate_id = t.certificate_id ?? null
  form.redirect_https = Boolean(t.redirect_https)
  form.proxy_timeout = Number(t.proxy_timeout) || 30
  form.max_body_size = Number(t.max_body_size) || 0
  form.remark = String(t.remark || '')
  if (t.options) {
    form.options = {
      ...form.options,
      ...t.options,
      ipMode: t.options.ipMode || '',
      ipList: [...(t.options.ipList ?? [])],
      requestHeaders: [...(t.options.requestHeaders ?? [])],
      responseHeaders: [...(t.options.responseHeaders ?? [])],
    }
  }
  domainInput.value = ''
  domainError.value = ''
}

async function save() {
  if (!validate()) return
  saving.value = true
  const payload: SiteInput = {
    ...form,
    name: form.name.trim(),
    domains: form.domains.map((d) => d.trim()).filter(Boolean),
    options: {
      ...form.options,
      requestHeaders: form.options.requestHeaders.filter((h) => h.name.trim()),
      responseHeaders: form.options.responseHeaders.filter((h) => h.name.trim()),
    },
  }
  try {
    if (isEdit.value) {
      await api.put(`/sites/${siteId.value}`, payload)
      ElMessage.success('站点已保存并应用')
    } else {
      await api.post('/sites', payload)
      ElMessage.success('站点已创建并应用')
    }
    initialSnapshot.value = JSON.stringify(form)
    emit('saved')
    visible.value = false
  } catch (err) {
    const message = err instanceof Error ? err.message : '保存失败'
    ElMessageBox.alert(message, '保存失败', { type: 'error' })
  } finally {
    saving.value = false
  }
}

watch(
  () => props.modelValue,
  (open) => {
    if (open) load()
  },
  { immediate: true },
)
watch(
  () => props.siteId,
  () => {
    if (visible.value) load()
  },
)

function beforeUnload(e: BeforeUnloadEvent) {
  if (!dirty.value) return
  e.preventDefault()
  e.returnValue = ''
}

async function beforeClose(done: () => void) {
  if (!dirty.value) {
    done()
    return
  }
  try {
    await ElMessageBox.confirm('当前修改尚未保存，确认关闭？', '未保存的修改', { type: 'warning' })
    done()
  } catch {
    // keep editing
  }
}

async function closeEditor() {
  if (!dirty.value) {
    visible.value = false
    return
  }
  try {
    await ElMessageBox.confirm('当前修改尚未保存，确认关闭？', '未保存的修改', { type: 'warning' })
    visible.value = false
  } catch {
    // keep editing
  }
}

async function goTo(path: string) {
  if (dirty.value) {
    try {
      await ElMessageBox.confirm('当前修改尚未保存，确认离开？', '未保存的修改', { type: 'warning' })
    } catch {
      return
    }
  }
  visible.value = false
  router.push(path)
}

window.addEventListener('beforeunload', beforeUnload)
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
</script>
