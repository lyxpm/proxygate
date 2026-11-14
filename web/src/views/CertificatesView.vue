<template>
  <div class="page">
    <div class="page-toolbar">
      <div class="toolbar-left">
        <el-radio-group v-model="tab">
          <el-radio-button value="certs">证书列表</el-radio-button>
          <el-radio-button value="dns">DNS 服务商</el-radio-button>
          <el-radio-button value="accounts">ACME 账户</el-radio-button>
        </el-radio-group>
      </div>
      <div class="toolbar-right">
        <el-button :icon="Refresh" @click="loadAll">刷新</el-button>
        <template v-if="tab === 'certs'">
          <el-button :icon="Upload" @click="uploadDialog = true">上传证书</el-button>
          <el-button type="primary" :icon="Plus" @click="issueDialog = true">申请证书</el-button>
        </template>
        <el-button v-if="tab === 'dns'" type="primary" :icon="Plus" @click="openProviderCreate">添加 DNS 服务商</el-button>
      </div>
    </div>

    <template v-if="tab === 'certs'">
      <div class="card">
        <el-table v-loading="loading" :data="certs">
          <el-table-column label="证书" min-width="180">
            <template #default="{ row }">
              <div style="font-weight: 600">{{ row.name }}</div>
              <div class="muted tiny">{{ row.issuer || '未知签发者' }}</div>
            </template>
          </el-table-column>
          <el-table-column label="域名" min-width="220">
            <template #default="{ row }">
              <el-tag v-for="d in row.domains" :key="d" size="small" effect="plain" style="margin: 2px 4px 2px 0">{{ d }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="类型" width="90">
            <template #default="{ row }">
              <el-tag size="small" :type="row.certificate_type === 'acme' ? 'success' : 'info'" effect="plain">
                {{ row.certificate_type === 'acme' ? 'ACME' : '上传' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="110">
            <template #default="{ row }">
              <el-tag :type="certStatusType(row)" size="small" effect="light">{{ certStatusText(row) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="到期时间" width="190">
            <template #default="{ row }">
              <div>{{ formatTime(row.expires_at) }}</div>
              <div class="tiny" :style="{ color: (row.days_remaining ?? 99) <= 30 ? '#f59e0b' : '#64748b' }">
                {{ row.days_remaining != null ? `剩余 ${row.days_remaining} 天` : '-' }}
              </div>
            </template>
          </el-table-column>
          <el-table-column label="自动续期" width="90">
            <template #default="{ row }">
              <el-tag size="small" :type="row.auto_renew ? 'success' : 'info'" effect="plain">{{ row.auto_renew ? '开' : '关' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="260" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="openDetail(row)">查看</el-button>
              <el-button link type="primary" size="small" @click="downloadCert(row)">下载</el-button>
              <el-button v-if="row.certificate_type === 'acme'" link type="primary" size="small" @click="renew(row)">续期</el-button>
              <el-button link type="info" size="small" @click="showLogs(row)">日志</el-button>
              <el-button link type="danger" size="small" @click="removeCert(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </template>

    <template v-if="tab === 'dns'">
      <el-alert type="info" :closable="false" show-icon style="margin-bottom: 4px" title="DNS-01 支持通配符证书；凭据使用 AES-256-GCM 加密存储，接口永不返回明文。" />
      <div class="card">
        <el-table v-loading="loading" :data="providers">
          <el-table-column prop="name" label="名称" min-width="160" />
          <el-table-column label="类型" width="150">
            <template #default="{ row }">{{ row.meta?.title || row.provider_type }}</template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="row.enabled ? 'success' : 'info'" size="small" effect="plain">{{ row.enabled ? '启用' : '禁用' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="created_at" label="创建时间" width="180">
            <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="160" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="openProviderEdit(row)">编辑</el-button>
              <el-button link type="danger" size="small" @click="removeProvider(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </template>

    <template v-if="tab === 'accounts'">
      <div class="card">
        <el-table v-loading="loading" :data="accounts">
          <el-table-column prop="email" label="邮箱" min-width="200" />
          <el-table-column prop="ca_url" label="ACME 目录" min-width="280" show-overflow-tooltip />
          <el-table-column prop="status" label="状态" width="100" />
          <el-table-column prop="created_at" label="创建时间" width="180">
            <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="100" fixed="right">
            <template #default="{ row }">
              <el-button link type="danger" size="small" @click="removeAccount(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </template>

    <el-dialog v-model="issueDialog" align-center draggable :close-on-click-modal="false" title="申请 ACME 证书" width="640px" destroy-on-close>
      <el-form label-position="top">
        <el-form-item label="证书名称">
          <el-input v-model="issueForm.name" placeholder="留空使用第一个域名" />
        </el-form-item>
        <el-form-item label="域名（每行一个，支持通配符）" required>
          <el-input v-model="issueForm.domains" type="textarea" :rows="3" placeholder="example.com&#10;*.example.com" />
        </el-form-item>
        <el-row :gutter="16">
          <el-col :span="12">
            <el-form-item label="ACME 邮箱" required>
              <el-input v-model="issueForm.email" placeholder="admin@example.com" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="CA">
              <el-select v-model="issueForm.ca" style="width: 100%">
                <el-option label="Let's Encrypt" value="letsencrypt" />
                <el-option label="Let's Encrypt Staging" value="letsencrypt-staging" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="16">
          <el-col :span="12">
            <el-form-item label="验证方式">
              <el-radio-group v-model="issueForm.challenge">
                <el-radio-button value="http01">HTTP-01</el-radio-button>
                <el-radio-button value="dns01">DNS-01</el-radio-button>
              </el-radio-group>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="密钥类型">
              <el-select v-model="issueForm.key_type" style="width: 100%">
                <el-option label="ECDSA P-256（推荐）" value="P256" />
                <el-option label="ECDSA P-384" value="P384" />
                <el-option label="RSA 2048" value="2048" />
                <el-option label="RSA 4096" value="4096" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        <el-form-item v-if="issueForm.challenge === 'dns01'" label="DNS 服务商" required>
          <el-select v-model="issueForm.dns_provider_id" style="width: 100%" placeholder="选择 DNS 服务商">
            <el-option v-for="p in providers" :key="p.id" :label="`${p.name}（${p.meta?.title}）`" :value="p.id" />
          </el-select>
        </el-form-item>
        <el-alert v-if="issueForm.challenge === 'http01'" type="info" :closable="false" show-icon title="HTTP-01 需要 80 端口可从公网访问；代理未监听 80 时会临时监听该端口完成验证。" />
        <el-form-item label="自动续期" style="margin-top: 12px">
          <el-switch v-model="issueForm.auto_renew" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="issueDialog = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitIssue">提交申请</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="uploadDialog" align-center draggable :close-on-click-modal="false" title="上传证书" width="640px" destroy-on-close>
      <el-form label-position="top">
        <el-form-item label="证书名称"><el-input v-model="uploadForm.name" placeholder="留空使用证书域名" /></el-form-item>
        <el-form-item label="证书 PEM" required>
          <el-input v-model="uploadForm.certificate_pem" type="textarea" :rows="6" placeholder="-----BEGIN CERTIFICATE-----" />
        </el-form-item>
        <el-form-item label="私钥 PEM" required>
          <el-input v-model="uploadForm.private_key_pem" type="textarea" :rows="6" placeholder="-----BEGIN PRIVATE KEY-----" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="uploadDialog = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitUpload">上传</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="detailDialog" align-center draggable :close-on-click-modal="false" title="证书详情" width="760px" destroy-on-close>
      <div v-loading="detailLoading">
        <el-descriptions v-if="detail" :column="2" border size="small">
          <el-descriptions-item label="名称">{{ detail.name }}</el-descriptions-item>
          <el-descriptions-item label="业务编码"><span class="code">{{ detail.code }}</span></el-descriptions-item>
          <el-descriptions-item label="状态">{{ detail.status }}</el-descriptions-item>
          <el-descriptions-item label="颁发者" :span="2">{{ detail.issuer }}</el-descriptions-item>
          <el-descriptions-item label="Subject" :span="2">{{ detail.subject }}</el-descriptions-item>
          <el-descriptions-item label="序列号" :span="2"><span class="code">{{ detail.serial_number }}</span></el-descriptions-item>
          <el-descriptions-item label="域名" :span="2">{{ (detail.dns_names?.length ? detail.dns_names : detail.domains).join(', ') }}</el-descriptions-item>
          <el-descriptions-item label="生效时间">{{ formatTime(detail.not_before) }}</el-descriptions-item>
          <el-descriptions-item label="到期时间">{{ formatTime(detail.not_after) }}</el-descriptions-item>
          <el-descriptions-item label="SHA-256" :span="2"><span class="code" style="word-break: break-all">{{ detail.fingerprint_sha256 }}</span></el-descriptions-item>
        </el-descriptions>
        <div class="card-title" style="margin: 16px 0 8px">
          <span>证书 PEM（含证书链）</span>
          <el-button size="small" text type="primary" @click="copyPEM">复制</el-button>
        </div>
        <el-input :model-value="detail?.certificate_pem" type="textarea" :rows="14" readonly class="cert-pem" />
        <el-alert type="warning" :closable="false" show-icon style="margin-top: 10px" title="详情和普通下载不包含私钥；如需迁移证书，请使用“导出证书和私钥”，导出前需再次输入管理员密码。" />
      </div>
      <template #footer>
        <el-button @click="detailDialog = false">关闭</el-button>
        <el-button type="primary" @click="detail && downloadCert(detail)">下载 PEM</el-button>
        <el-button v-if="auth.user?.role === 'admin'" type="danger" plain @click="exportCertificateKey">导出证书+私钥</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="providerDialog" align-center draggable :close-on-click-modal="false" :title="providerEditing ? '编辑 DNS 服务商' : '添加 DNS 服务商'" width="560px" destroy-on-close>
      <el-form label-position="top">
        <el-form-item label="名称" required><el-input v-model="providerForm.name" placeholder="例如：Cloudflare 主账号" /></el-form-item>
        <el-form-item label="服务商" required>
          <el-select v-model="providerForm.provider_type" style="width: 100%" @change="onProviderTypeChange">
            <el-option v-for="m in supported" :key="m.name" :label="m.title" :value="m.name" />
          </el-select>
        </el-form-item>
        <el-form-item :label="currentMeta?.id_label || 'ID'">
          <el-input v-model="providerForm.credentials.id" />
        </el-form-item>
        <el-form-item :label="currentMeta?.secret_label || 'Secret'">
          <el-input v-model="providerForm.credentials.secret" type="password" show-password :placeholder="providerEditing ? '留空表示不修改' : ''" />
        </el-form-item>
        <el-form-item v-for="f in currentMeta?.extra || []" :key="f.key" :label="f.label">
          <el-input v-model="providerForm.credentials.extra[f.key]" :placeholder="f.placeholder" />
        </el-form-item>
        <el-switch v-model="providerForm.enabled" active-text="启用" />
      </el-form>
      <template #footer>
        <el-button @click="providerDialog = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitProvider">保存</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="logsDrawer" :title="`续期日志 · ${activeCert?.name || ''}`" size="620px">
      <el-timeline v-if="certLogs.length">
        <el-timeline-item v-for="item in certLogs" :key="item.ID" :timestamp="formatTime(item.CreatedAt)" :type="item.Level === 'error' ? 'danger' : 'primary'">
          {{ item.Message }}
        </el-timeline-item>
      </el-timeline>
      <el-empty v-else description="暂无日志" />
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh, Upload } from '@element-plus/icons-vue'
import { api, csrfToken, formatTime } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import type { ACMEAccount, Certificate, DNSProvider, Page } from '@/api/types'

const auth = useAuthStore()

interface CertLog {
  ID: number
  Level: string
  Message: string
  CreatedAt: string
}

interface CertDetail extends Certificate {
  certificate_pem: string
  subject: string
  serial_number: string
  dns_names: string[]
  not_before: string
  not_after: string
  fingerprint_sha256: string
}

const tab = ref('certs')
const certs = ref<Certificate[]>([])
const providers = ref<DNSProvider[]>([])
const accounts = ref<ACMEAccount[]>([])
const supported = ref<DNSProvider['meta'][]>([])
const loading = ref(false)
const submitting = ref(false)
const issueDialog = ref(false)
const uploadDialog = ref(false)
const providerDialog = ref(false)
const providerEditing = ref(false)
const editingProviderId = ref(0)
const logsDrawer = ref(false)
const activeCert = ref<Certificate>()
const certLogs = ref<CertLog[]>([])
const detailDialog = ref(false)
const detailLoading = ref(false)
const detail = ref<CertDetail>()

const issueForm = reactive({
  name: '',
  domains: '',
  email: '',
  ca: 'letsencrypt',
  challenge: 'http01',
  key_type: 'P256',
  dns_provider_id: 0 as number | undefined,
  auto_renew: true,
})
const uploadForm = reactive({ name: '', certificate_pem: '', private_key_pem: '' })
const providerForm = reactive({
  name: '',
  provider_type: 'cloudflare',
  credentials: { id: '', secret: '', extra: {} as Record<string, string> },
  enabled: true,
})

const currentMeta = computed(() => supported.value.find((m) => m.name === providerForm.provider_type))

async function loadAll() {
  loading.value = true
  try {
    const [c, p, a] = await Promise.all([
      api.get<Page<Certificate>>('/certificates?page=1&page_size=200'),
      api.get<{ items: DNSProvider[]; supported: DNSProvider['meta'][] }>('/dns-providers'),
      api.get<{ items: ACMEAccount[] }>('/acme-accounts'),
    ])
    certs.value = c.items ?? []
    providers.value = p.items ?? []
    supported.value = p.supported ?? []
    accounts.value = a.items ?? []
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '加载证书失败')
  } finally {
    loading.value = false
  }
}

function certStatusType(row: Certificate) {
  if (row.status === 'valid') return 'success'
  if (row.status === 'error') return 'danger'
  return 'warning'
}

function certStatusText(row: Certificate) {
  return ({ valid: '有效', pending: '申请中', error: '异常' } as Record<string, string>)[row.status] || row.status
}

async function submitIssue() {
  const domains = issueForm.domains.split(/\n|,/).map((d) => d.trim()).filter(Boolean)
  if (!domains.length) return ElMessage.warning('请填写至少一个域名')
  if (!issueForm.email.includes('@')) return ElMessage.warning('请填写有效的 ACME 邮箱')
  if (issueForm.challenge === 'dns01' && !issueForm.dns_provider_id) return ElMessage.warning('DNS-01 需要选择 DNS 服务商')
  submitting.value = true
  try {
    await api.post('/certificates/acme', {
      name: issueForm.name,
      domains,
      email: issueForm.email,
      ca: issueForm.ca,
      challenge: issueForm.challenge,
      key_type: issueForm.key_type,
      dns_provider_id: issueForm.challenge === 'dns01' ? issueForm.dns_provider_id : undefined,
      auto_renew: issueForm.auto_renew,
    })
    ElMessage.success('申请已提交，证书状态会在一段时间内更新')
    issueDialog.value = false
    await loadAll()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '提交失败')
  } finally {
    submitting.value = false
  }
}

async function submitUpload() {
  if (!uploadForm.certificate_pem.includes('BEGIN CERTIFICATE')) return ElMessage.warning('请填写证书 PEM')
  if (!uploadForm.private_key_pem.includes('PRIVATE KEY')) return ElMessage.warning('请填写私钥 PEM')
  submitting.value = true
  try {
    await api.post('/certificates/upload', uploadForm)
    ElMessage.success('证书已上传')
    uploadDialog.value = false
    uploadForm.name = ''
    uploadForm.certificate_pem = ''
    uploadForm.private_key_pem = ''
    await loadAll()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '上传失败')
  } finally {
    submitting.value = false
  }
}

async function renew(row: Certificate) {
  try {
    await api.post(`/certificates/${row.code}/renew`)
    ElMessage.success('续期任务已提交')
    await loadAll()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '续期失败')
  }
}

async function removeCert(row: Certificate) {
  try {
    await ElMessageBox.confirm(`确认删除证书 “${row.name}”？已关联的站点需要先解除关联。`, '删除证书', { type: 'warning' })
    await api.del(`/certificates/${row.code}`)
    ElMessage.success('证书已删除')
    await loadAll()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '删除失败')
  }
}

async function openDetail(row: Certificate) {
  detailDialog.value = true
  detailLoading.value = true
  detail.value = undefined
  try {
    detail.value = await api.get<CertDetail>(`/certificates/${row.code}/detail`)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '读取证书失败')
    detailDialog.value = false
  } finally {
    detailLoading.value = false
  }
}

function downloadCert(row: Certificate) {
  const a = document.createElement('a')
  a.href = `/api/v1/certificates/${row.code}/download`
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  a.remove()
}

async function exportCertificateKey() {
  if (!detail.value) return
  try {
    const { value } = await ElMessageBox.prompt(
      '导出的 ZIP 包含证书和私钥，请妥善保管。请输入管理员密码确认导出。',
      '导出证书和私钥',
      {
        inputType: 'password',
        inputValidator: (v: string) => (v ? true : '请输入管理员密码'),
      },
    )
    const res = await fetch(`/api/v1/certificates/${detail.value.code}/export`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ password: value }),
    })
    if (!res.ok) {
      const body = await res.json().catch(() => null)
      throw new Error(body?.message || `导出失败（HTTP ${res.status}）`)
    }
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${detail.value.name || 'certificate'}-key.zip`
    a.click()
    URL.revokeObjectURL(url)
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '导出失败')
  }
}

async function copyPEM() {
  if (!detail.value?.certificate_pem) return
  try {
    await navigator.clipboard.writeText(detail.value.certificate_pem)
    ElMessage.success('证书 PEM 已复制')
  } catch {
    ElMessage.error('复制失败，请手动选择文本')
  }
}

async function showLogs(row: Certificate) {
  activeCert.value = row
  logsDrawer.value = true
  try {
    const data = await api.get<{ items: CertLog[] }>(`/certificates/${row.code}/logs`)
    certLogs.value = data.items ?? []
  } catch (err) {
    certLogs.value = []
    ElMessage.error(err instanceof Error ? err.message : '加载日志失败')
  }
}

function openProviderCreate() {
  providerEditing.value = false
  editingProviderId.value = 0
  providerForm.name = ''
  providerForm.provider_type = 'cloudflare'
  providerForm.credentials = { id: '', secret: '', extra: {} }
  providerForm.enabled = true
  providerDialog.value = true
}

function openProviderEdit(row: DNSProvider) {
  providerEditing.value = true
  editingProviderId.value = row.id
  providerForm.name = row.name
  providerForm.provider_type = row.provider_type
  providerForm.credentials = { id: '', secret: '', extra: {} }
  providerForm.enabled = row.enabled
  providerDialog.value = true
}

function onProviderTypeChange() {
  providerForm.credentials.extra = {}
}

async function submitProvider() {
  if (!providerForm.name.trim()) return ElMessage.warning('请填写名称')
  submitting.value = true
  try {
    const payload = {
      name: providerForm.name,
      provider_type: providerForm.provider_type,
      credentials: providerForm.credentials,
      enabled: providerForm.enabled,
    }
    if (providerEditing.value) {
      await api.put(`/dns-providers/${editingProviderId.value}`, payload)
    } else {
      await api.post('/dns-providers', payload)
    }
    ElMessage.success('DNS 服务商已保存')
    providerDialog.value = false
    await loadAll()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '保存失败')
  } finally {
    submitting.value = false
  }
}

async function removeProvider(row: DNSProvider) {
  try {
    await ElMessageBox.confirm(`确认删除 DNS 服务商 “${row.name}”？`, '删除 DNS 服务商', { type: 'warning' })
    await api.del(`/dns-providers/${row.id}`)
    ElMessage.success('已删除')
    await loadAll()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '删除失败')
  }
}

async function removeAccount(row: ACMEAccount) {
  try {
    await ElMessageBox.confirm(`确认删除 ACME 账户 ${row.email}？`, '删除账户', { type: 'warning' })
    await api.del(`/acme-accounts/${row.id}`)
    ElMessage.success('已删除')
    await loadAll()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '删除失败')
  }
}

onMounted(loadAll)
</script>
