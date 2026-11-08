<template>
  <div class="page" :data-tab="tab">
    <el-radio-group v-model="tab" class="settings-tab-group">
      <el-radio-button v-for="item in tabs" :key="item.key" :value="item.key">{{ item.label }}</el-radio-button>
    </el-radio-group>
    <div class="grid-2-even settings-group settings-basic">
      <div class="card">
        <div class="card-body">
          <div class="card-title">
            <span>系统信息</span>
            <el-button
              v-if="auth.user?.role === 'admin'"
              size="small"
              type="warning"
              plain
              :loading="restarting"
              @click="restartEngine"
            >重启代理引擎</el-button>
          </div>
          <dl class="summary-grid" style="margin-top: 16px">
            <dt>版本</dt><dd>{{ info?.version || '-' }}</dd>
            <dt>运行环境</dt><dd>{{ info?.os }} / {{ info?.arch }} · {{ info?.go_version }}</dd>
            <dt>已运行</dt><dd>{{ uptimeText }}</dd>
            <dt>代理引擎</dt><dd>{{ info?.engine?.running ? '运行中' : '未运行' }}</dd>
            <dt>配置版本</dt><dd>{{ info?.engine?.config_version ?? 0 }}</dd>
            <dt>当前连接</dt><dd>{{ info?.engine?.active_connections ?? 0 }}</dd>
            <dt>CPU 使用率</dt><dd>{{ info?.host?.cpu_percent ?? 0 }}%</dd>
            <dt>内存使用</dt><dd>{{ formatBytes(info?.host?.memory_used ?? 0) }} / {{ formatBytes(info?.host?.memory_total ?? 0) }}（{{ info?.host?.memory_percent ?? 0 }}%）</dd>
            <dt>监听端口</dt>
            <dd>
              <el-tag v-for="p in info?.engine?.ports || []" :key="p.port" size="small" effect="plain" style="margin-right: 6px">
                :{{ p.port }} {{ p.tls ? 'HTTPS' : 'HTTP' }} · {{ p.sites }} 站点
              </el-tag>
            </dd>
            <dt>日志丢弃</dt><dd>{{ info?.access_log_dropped ?? 0 }}</dd>
          </dl>
        </div>
      </div>

      <div class="card">
        <div class="card-body">
          <div class="card-title"><span>系统设置</span></div>
          <el-form label-position="top" style="margin-top: 14px">
            <el-form-item>
              <template #label>
                <span class="security-block-title-row">日志保留天数
                  <el-tooltip content="访问日志按天切分；重启后按新的保留天数清理过期文件。" placement="top">
                    <el-icon class="hint-icon"><Warning /></el-icon>
                  </el-tooltip>
                </span>
              </template>
              <el-input-number v-model="settings.log_retention_days" :min="1" :max="365" style="width: 200px" />
            </el-form-item>
            <el-form-item>
              <template #label>
                <span class="security-block-title-row">时区
                  <el-tooltip content="重启后生效，影响仪表盘日期边界与日志时间。" placement="top">
                    <el-icon class="hint-icon"><Warning /></el-icon>
                  </el-tooltip>
                </span>
              </template>
              <el-select v-model="settings.timezone" filterable allow-create style="width: 260px" placeholder="Asia/Shanghai">
                <el-option v-for="tz in timezones" :key="tz" :label="tz" :value="tz" />
              </el-select>
            </el-form-item>
            <el-form-item>
              <template #label>
                <span class="security-block-title-row">配置版本保留上限
                  <el-tooltip content="每次成功应用生成快照；超过上限自动删除最旧版本，当前版本不可删除。" placement="top">
                    <el-icon class="hint-icon"><Warning /></el-icon>
                  </el-tooltip>
                </span>
              </template>
              <el-input-number v-model="settings.config_version_limit" :min="5" :max="200" style="width: 200px" />
            </el-form-item>
            <el-form-item>
              <template #label>
                <span class="security-block-title-row">证书续期窗口（天）
                  <el-tooltip content="默认到期前 30 天进入续期队列，另加 0–12 小时随机抖动。" placement="top">
                    <el-icon class="hint-icon"><Warning /></el-icon>
                  </el-tooltip>
                </span>
              </template>
              <el-input-number v-model="settings.certificate_renew_before_days" :min="7" :max="60" style="width: 200px" />
            </el-form-item>
            <div class="settings-actions">
              <el-button type="primary" :loading="saving" @click="saveSettings('basic')">保存设置</el-button>
            </div>
          </el-form>
        </div>
      </div>
    </div>

    <div class="card settings-group settings-security">
      <div class="card-body">
        <div class="card-title"><span>安全与登录策略</span></div>

        <div class="security-block">
          <div class="security-block-head">
            <div class="security-block-title-row">
              <span class="security-block-title">密码复杂度策略</span>
              <el-tooltip content="关闭时仅校验 8 位最低长度；启用后校验新密码和管理员重置密码。" placement="top">
                <el-icon class="hint-icon"><Warning /></el-icon>
              </el-tooltip>
            </div>
            <el-switch v-model="settings.password_policy_enabled" active-text="启用" inactive-text="关闭" />
          </div>
          <el-form label-position="top" style="margin-top: 12px">
            <div class="security-inline-grid">
              <el-form-item label="密码最小长度">
                <el-input-number v-model="settings.password_min_length" :min="8" :max="64" :disabled="!settings.password_policy_enabled" style="width: 100%" />
              </el-form-item>
              <el-form-item label="字符要求">
                <div class="security-checks">
                  <el-checkbox v-model="settings.password_require_upper" :disabled="!settings.password_policy_enabled">大写字母</el-checkbox>
                  <el-checkbox v-model="settings.password_require_lower" :disabled="!settings.password_policy_enabled">小写字母</el-checkbox>
                  <el-checkbox v-model="settings.password_require_digit" :disabled="!settings.password_policy_enabled">数字</el-checkbox>
                  <el-checkbox v-model="settings.password_require_symbol" :disabled="!settings.password_policy_enabled">符号</el-checkbox>
                </div>
              </el-form-item>
            </div>
          </el-form>
        </div>

        <div class="security-block">
          <div class="security-block-head">
            <div class="security-block-title-row">
              <span class="security-block-title">登录保护</span>
              <el-tooltip content="关闭时不做空闲超时和失败封锁，但失败仍记录审计。开启后：无操作达到空闲时间自动退出；连续失败达到上限后按 IP + 用户名封锁。" placement="top">
                <el-icon class="hint-icon"><Warning /></el-icon>
              </el-tooltip>
            </div>
            <el-switch v-model="settings.login_protection_enabled" active-text="启用" inactive-text="关闭" />
          </div>
          <el-form label-position="top" style="margin-top: 12px">
            <div class="security-three-col">
              <el-form-item label="空闲超时（分钟）">
                <el-input-number v-model="settings.session_idle_minutes" :min="5" :max="10080" :disabled="!settings.login_protection_enabled" style="width: 100%" />
              </el-form-item>
              <el-form-item label="失败次数上限">
                <el-input-number v-model="settings.login_max_failures" :min="3" :max="20" :disabled="!settings.login_protection_enabled" style="width: 100%" />
              </el-form-item>
              <el-form-item label="封锁时长（分钟）">
                <el-input-number v-model="settings.login_lock_minutes" :min="1" :max="1440" :disabled="!settings.login_protection_enabled" style="width: 100%" />
              </el-form-item>
            </div>
          </el-form>
        </div>

        <div class="security-block">
          <div class="security-block-head">
            <div class="security-block-title-row">
              <span class="security-block-title">管理面 HTTPS</span>
              <el-tooltip content="保存后自动重载管理 HTTPS；HTTP 管理端口保留健康检查。证书来自证书管理模块。" placement="top">
                <el-icon class="hint-icon"><Warning /></el-icon>
              </el-tooltip>
            </div>
            <el-switch v-model="settings.admin_https_enabled" active-text="启用" inactive-text="关闭" />
          </div>
          <el-form label-position="top" style="margin-top: 12px">
            <div class="security-inline-grid security-inline-grid-wide">
              <el-form-item label="HTTPS 端口">
                <el-input-number v-model="settings.admin_https_port" :min="1024" :max="65535" :disabled="!settings.admin_https_enabled" style="width: 100%" />
              </el-form-item>
              <el-form-item label="HTTPS 证书">
                <el-select v-model="settings.admin_https_certificate_id" style="width: 100%" placeholder="选择证书" :disabled="!settings.admin_https_enabled">
                  <el-option label="未选择" :value="0" />
                  <el-option v-for="c in certs" :key="c.id" :label="`${c.name}（${c.domains?.join(', ') || '-'}）`" :value="c.id" />
                </el-select>
              </el-form-item>
            </div>
          </el-form>
        </div>

        <el-button type="primary" :loading="saving" @click="saveSettings('security')">保存安全策略</el-button>
      </div>
    </div>

    <div class="card settings-group settings-versions">
      <div class="card-body">
        <div class="card-title">
          <span>配置版本</span>
          <div class="version-actions">
            <span class="muted tiny">每次成功应用配置都会生成快照</span>
            <el-button @click="backup">下载备份</el-button>
            <el-upload :auto-upload="false" :show-file-list="false" accept=".zip,application/zip" :on-change="restoreBackup">
              <el-button>恢复备份</el-button>
            </el-upload>
            <el-button type="danger" plain size="small" :disabled="!selectedVersions.length" @click="batchRemoveVersions">
              批量删除{{ selectedVersions.length ? `（${selectedVersions.length}）` : '' }}
            </el-button>
          </div>
        </div>
        <el-table :data="versions" size="small" style="margin-top: 12px" row-key="Version" @selection-change="onVersionSelection">
          <el-table-column type="selection" width="46" :selectable="canSelectVersion" />
          <el-table-column prop="Version" label="版本" width="110">
            <template #default="{ row }">
              <span>{{ row.Version }}</span>
              <el-tag v-if="versions[0]?.Version === row.Version" size="small" type="success" effect="plain" style="margin-left: 6px">当前</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="说明" min-width="160">
            <template #default="{ row }">{{ versionDescription(row.Description) }}</template>
          </el-table-column>
          <el-table-column label="时间" width="190">
            <template #default="{ row }">{{ formatTime(row.CreatedAt) }}</template>
          </el-table-column>
          <el-table-column label="校验和" width="140">
            <template #default="{ row }"><span class="code">{{ row.Checksum.slice(0, 12) }}</span></template>
          </el-table-column>
          <el-table-column label="操作" width="160">
            <template #default="{ row }">
              <el-button link type="warning" size="small" @click="rollback(row)">回滚</el-button>
              <el-button link type="danger" size="small" :disabled="versions[0]?.Version === row.Version" @click="removeVersion(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div v-if="!versions.length" class="empty" style="padding: 20px 0">暂无配置版本</div>
        <el-alert type="warning" :closable="false" show-icon style="margin-top: 14px" title="备份包含数据库、证书私钥、ACME 账户和配置，不包含历史配置版本、日志和会话；下载前需再次输入管理员密码。恢复后服务自动重启，配置版本重置为 1。" />
      </div>
    </div>

    <div class="card settings-group settings-accounts">
      <div class="card-body">
        <div class="card-title">
          <span>API 访问令牌</span>
          <el-button type="primary" size="small" :icon="Plus" @click="openTokenCreate">创建令牌</el-button>
        </div>
        <p class="muted tiny" style="margin-top: 6px">
          自动化脚本使用 <span class="code">Authorization: Bearer &lt;token&gt;</span>。只读令牌只能发起 GET 请求；创建后明文仅显示一次。
        </p>
        <el-table :data="tokens" size="small" style="margin-top: 10px">
          <el-table-column prop="name" label="名称" min-width="140" />
          <el-table-column label="权限" width="150">
            <template #default="{ row }">
              <el-tag v-for="scope in row.scopes" :key="scope" size="small" effect="plain" style="margin-right: 4px">{{ scope }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="最近使用" width="180">
            <template #default="{ row }">{{ row.last_used_at ? formatTime(row.last_used_at) : '从未' }}</template>
          </el-table-column>
          <el-table-column label="过期时间" width="180">
            <template #default="{ row }">{{ row.expires_at ? formatTime(row.expires_at) : '永不过期' }}</template>
          </el-table-column>
          <el-table-column label="创建时间" width="180">
            <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="90">
            <template #default="{ row }">
              <el-button link type="danger" size="small" @click="removeToken(row)">吊销</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div v-if="!tokens.length" class="empty" style="padding: 20px 0">还没有 API 令牌</div>
      </div>
    </div>

    <div class="card settings-group settings-security">
      <div class="card-body">
        <div class="card-title">
          <span>两步验证（TOTP）</span>
          <el-tag :type="totpEnabled ? 'success' : 'info'" size="small" effect="plain">{{ totpEnabled ? '已启用' : '未启用' }}</el-tag>
        </div>
        <p class="muted tiny" style="margin-top: 6px">
          使用 Google Authenticator、Microsoft Authenticator 或 1Password 等应用扫描/录入密钥。启用后登录必须输入 6 位动态验证码。
        </p>
        <template v-if="totpEnabled">
          <el-button type="danger" plain style="margin-top: 10px" :loading="totpLoading" @click="disableTOTP">关闭两步验证</el-button>
        </template>
        <template v-else>
          <el-button type="primary" style="margin-top: 10px" :loading="totpLoading" @click="startTOTPSetup">生成密钥</el-button>
          <div v-if="totpSetup" class="totp-setup">
            <div class="totp-qr">
              <img v-if="totpSetup.qr_data_url" :src="totpSetup.qr_data_url" alt="TOTP 二维码" />
              <div v-else class="muted tiny">二维码生成失败，请手动录入密钥</div>
            </div>
            <div class="totp-secret">
              <span class="muted tiny">密钥</span>
              <span class="code">{{ totpSetup.secret }}</span>
              <el-button link type="primary" size="small" @click="copyTOTPSecret">复制</el-button>
            </div>
            <div class="form-hint" style="word-break: break-all">{{ totpSetup.otpauth_url }}</div>
            <el-form label-position="top" style="max-width: 280px; margin-top: 10px">
              <el-form-item label="输入应用生成的 6 位验证码">
                <el-input v-model="totpCode" maxlength="6" placeholder="000000" />
              </el-form-item>
              <el-button type="primary" :loading="totpLoading" @click="enableTOTP">确认启用</el-button>
            </el-form>
          </div>
        </template>
      </div>
    </div>

    <div v-if="auth.user?.role === 'admin'" class="card settings-group settings-accounts">
      <div class="card-body">
        <div class="card-title">
          <span>用户管理</span>
          <el-button size="small" type="primary" :icon="Plus" @click="openCreateUser">新建用户</el-button>
        </div>
        <p class="muted tiny" style="margin-top: 6px">管理员可读写全部配置；只读用户只能查看和下载。</p>
        <el-table :data="users" size="small" style="margin-top: 10px">
          <el-table-column prop="username" label="用户名" min-width="140" />
          <el-table-column label="角色" width="100">
            <template #default="{ row }">
              <el-tag size="small" :type="row.role === 'admin' ? 'danger' : 'info'" effect="plain">
                {{ row.role === 'admin' ? '管理员' : '只读' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag size="small" :type="row.enabled ? 'success' : 'info'" effect="plain">{{ row.enabled ? '启用' : '禁用' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="最后登录" width="180">
            <template #default="{ row }">{{ row.last_login_at ? formatTime(row.last_login_at) : '-' }}</template>
          </el-table-column>
          <el-table-column label="操作" width="140" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="openEditUser(row)">编辑</el-button>
              <el-button v-if="row.id !== auth.user?.id" link type="danger" size="small" @click="removeUser(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </div>

    <div class="card settings-group settings-accounts">
      <div class="card-body">
        <div class="card-title">
          <span>登录会话</span>
          <el-button size="small" type="warning" plain @click="revokeOtherSessions">退出其他所有会话</el-button>
        </div>
        <p class="muted tiny" style="margin-top: 6px">会话保存在服务端；吊销后对应浏览器需要重新登录。</p>
        <el-table :data="sessions" size="small" style="margin-top: 10px">
          <el-table-column label="客户端 IP" width="150" prop="client_ip" />
          <el-table-column prop="user_agent" label="User-Agent" min-width="220" show-overflow-tooltip />
          <el-table-column label="登录时间" width="180">
            <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
          </el-table-column>
          <el-table-column label="空闲过期" width="180">
            <template #default="{ row }">{{ formatTime(row.idle_expires_at || row.expires_at) }}</template>
          </el-table-column>
          <el-table-column label="绝对过期" width="180">
            <template #default="{ row }">{{ formatTime(row.expires_at) }}</template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag v-if="row.current" type="success" size="small" effect="plain">当前</el-tag>
              <span v-else class="muted">活动</span>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="90">
            <template #default="{ row }">
              <el-button v-if="!row.current" link type="danger" size="small" @click="revokeSession(row)">吊销</el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </div>

    <el-dialog v-model="userDialog" :title="editingUserId ? '编辑用户' : '新建用户'" width="460px" destroy-on-close>
      <el-form label-position="top">
        <el-form-item label="用户名" required>
          <el-input v-model="userForm.username" :disabled="!!editingUserId" placeholder="3-64 位字母、数字、点、下划线或短横线" />
        </el-form-item>
        <el-form-item :label="editingUserId ? '重置密码（留空不修改）' : '初始密码'" :required="!editingUserId">
          <el-input v-model="userForm.password" type="password" show-password autocomplete="new-password" />
        </el-form-item>
        <el-form-item label="角色">
          <el-select v-model="userForm.role" style="width: 100%">
            <el-option label="管理员（读写全部配置）" value="admin" />
            <el-option label="只读用户（仅查看/下载）" value="viewer" />
          </el-select>
        </el-form-item>
        <el-form-item label="状态">
          <el-switch v-model="userForm.enabled" active-text="启用" inactive-text="禁用" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="userDialog = false">取消</el-button>
        <el-button type="primary" :loading="userSaving" @click="saveUser">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="tokenDialog" title="创建 API 令牌" width="480px" destroy-on-close>
      <el-form label-position="top">
        <el-form-item label="名称" required>
          <el-input v-model="tokenForm.name" placeholder="例如：CI 部署 / Prometheus 只读" />
        </el-form-item>
        <el-form-item label="权限">
          <el-switch v-model="tokenForm.read_only" active-text="只读（仅 GET）" inactive-text="读写" />
        </el-form-item>
        <el-form-item label="过期时间（可选）">
          <el-date-picker v-model="tokenForm.expires_at" type="datetime" placeholder="永不过期" style="width: 100%" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="tokenDialog = false">取消</el-button>
        <el-button type="primary" :loading="tokenCreating" @click="createToken">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="tokenResultDialog" title="令牌已创建" width="560px" :close-on-click-modal="false">
      <el-alert type="warning" :closable="false" show-icon title="请立即复制保存，关闭后无法再次查看完整令牌。" />
      <el-input :model-value="createdToken" readonly style="margin-top: 14px" />
      <template #footer>
        <el-button @click="copyToken">复制令牌</el-button>
        <el-button type="primary" @click="tokenResultDialog = false">我已保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, type UploadFile } from 'element-plus'
import { Plus, Warning } from '@element-plus/icons-vue'
import { api, csrfToken, formatBytes, formatTime } from '@/api/client'
import type { APIToken, ConfigVersion, HostStats, User, UserSession } from '@/api/types'
import { useAuthStore } from '@/stores/auth'

interface Info {
  version: string
  go_version: string
  os: string
  arch: string
  uptime_seconds: number
  access_log_dropped: number
  host: HostStats
  engine: {
    running: boolean
    config_version: number
    active_connections: number
    ports: { port: number; tls: boolean; sites: number }[]
  }
}

const auth = useAuthStore()
const info = ref<Info>()
const saving = ref(false)
const restarting = ref(false)
const tab = ref('basic')
const tabs = [
  { key: 'basic', label: '基础设置' },
  { key: 'security', label: '安全策略' },
  { key: 'accounts', label: '账户与访问' },
  { key: 'versions', label: '配置版本' },
]
const settings = reactive({
  log_retention_days: 14,
  timezone: 'Asia/Shanghai',
  config_version_limit: 20,
  certificate_renew_before_days: 30,
  password_policy_enabled: false,
  password_min_length: 12,
  password_require_upper: true,
  password_require_lower: true,
  password_require_digit: true,
  password_require_symbol: false,
  login_max_failures: 5,
  login_lock_minutes: 15,
  login_lockout_enabled: true,
  session_ttl_minutes: 1440,
  session_timeout_enabled: true,
  login_protection_enabled: true,
  session_idle_minutes: 1440,
  admin_https_enabled: false,
  admin_https_port: 18443,
  admin_https_certificate_id: 0,
})
const certs = ref<{ id: number; name: string; domains: string[] }[]>([])
const timezones = ['Asia/Shanghai', 'Asia/Tokyo', 'Asia/Singapore', 'UTC', 'Europe/London', 'America/New_York']
const versions = ref<ConfigVersion[]>([])
const selectedVersions = ref<number[]>([])
const totpEnabled = ref(false)
const totpSetup = ref<{ secret: string; otpauth_url: string; qr_data_url?: string }>()
const totpCode = ref('')
const totpLoading = ref(false)
const users = ref<User[]>([])
const userDialog = ref(false)
const userSaving = ref(false)
const editingUserId = ref(0)
const userForm = reactive({ username: '', password: '', role: 'viewer' as 'admin' | 'viewer', enabled: true })
const tokens = ref<APIToken[]>([])
const sessions = ref<UserSession[]>([])
const tokenDialog = ref(false)
const tokenResultDialog = ref(false)
const tokenCreating = ref(false)
const createdToken = ref('')
const tokenForm = reactive({ name: '', read_only: true, expires_at: null as Date | null })

const uptimeText = computed(() => {
  const s = info.value?.uptime_seconds ?? 0
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  return `${d} 天 ${h} 小时 ${m} 分钟`
})

async function load() {
  try {
    info.value = await api.get<Info>('/system/info')
    const data = await api.get<{ values: Record<string, string> }>('/system/settings')
    if (data.values.log_retention_days) settings.log_retention_days = Number(data.values.log_retention_days)
    if (data.values.timezone) settings.timezone = data.values.timezone
    if (data.values.config_version_limit) settings.config_version_limit = Number(data.values.config_version_limit)
    if (data.values.certificate_renew_before_days) settings.certificate_renew_before_days = Number(data.values.certificate_renew_before_days)
    settings.password_policy_enabled = data.values.password_policy_enabled === '1'
    if (data.values.password_min_length) settings.password_min_length = Number(data.values.password_min_length)
    settings.password_require_upper = data.values.password_require_upper !== '0'
    settings.password_require_lower = data.values.password_require_lower !== '0'
    settings.password_require_digit = data.values.password_require_digit !== '0'
    settings.password_require_symbol = data.values.password_require_symbol === '1'
    if (data.values.login_max_failures) settings.login_max_failures = Number(data.values.login_max_failures)
    if (data.values.login_lock_minutes) settings.login_lock_minutes = Number(data.values.login_lock_minutes)
    settings.login_lockout_enabled = data.values.login_lockout_enabled !== '0'
    if (data.values.session_ttl_minutes) settings.session_ttl_minutes = Number(data.values.session_ttl_minutes)
    settings.session_timeout_enabled = data.values.session_timeout_enabled !== '0'
    settings.login_protection_enabled = data.values.login_protection_enabled !== undefined
      ? data.values.login_protection_enabled !== '0'
      : settings.login_lockout_enabled && settings.session_timeout_enabled
    settings.session_idle_minutes = Number(data.values.session_idle_minutes || data.values.session_ttl_minutes || 1440)
    settings.admin_https_enabled = data.values.admin_https_enabled === '1'
    if (data.values.admin_https_port) settings.admin_https_port = Number(data.values.admin_https_port)
    settings.admin_https_certificate_id = Number(data.values.admin_https_certificate_id || 0)
    const versionData = await api.get<{ items: ConfigVersion[] }>('/system/config-versions')
    versions.value = versionData.items ?? []
    const tokenData = await api.get<{ items: APIToken[] }>('/api-tokens')
    tokens.value = tokenData.items ?? []
    const sessionData = await api.get<{ items: UserSession[] }>('/auth/sessions')
    sessions.value = sessionData.items ?? []
    const totp = await api.get<{ enabled: boolean }>('/auth/totp')
    totpEnabled.value = totp.enabled
    if (auth.user?.role === 'admin') {
      const userData = await api.get<{ items: User[] }>('/users')
      users.value = userData.items ?? []
      const certData = await api.get<{ items: { id: number; name: string; domains: string[] }[] }>('/certificates?page=1&page_size=200')
      certs.value = certData.items ?? []
    }
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '加载系统信息失败')
  }
}

async function startTOTPSetup() {
  totpLoading.value = true
  try {
    totpSetup.value = await api.post<{ secret: string; otpauth_url: string; qr_data_url?: string }>('/auth/totp/setup')
    totpCode.value = ''
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '生成密钥失败')
  } finally {
    totpLoading.value = false
  }
}

async function copyTOTPSecret() {
  if (!totpSetup.value) return
  try {
    await navigator.clipboard.writeText(totpSetup.value.secret)
    ElMessage.success('密钥已复制')
  } catch {
    ElMessage.error('复制失败')
  }
}

async function enableTOTP() {
  if (!/^\d{6}$/.test(totpCode.value)) return ElMessage.warning('请输入 6 位动态验证码')
  totpLoading.value = true
  try {
    await api.post('/auth/totp/enable', { code: totpCode.value })
    totpEnabled.value = true
    totpSetup.value = undefined
    totpCode.value = ''
    ElMessage.success('两步验证已启用')
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '启用失败')
  } finally {
    totpLoading.value = false
  }
}

async function disableTOTP() {
  try {
    const { value } = await ElMessageBox.prompt('请输入当前管理员密码以关闭两步验证', '关闭两步验证', {
      inputType: 'password',
      inputValidator: (v: string) => (v ? true : '请输入密码'),
    })
    totpLoading.value = true
    await api.post('/auth/totp/disable', { password: value })
    totpEnabled.value = false
    ElMessage.success('两步验证已关闭')
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '关闭失败')
  } finally {
    totpLoading.value = false
  }
}

async function loadUsers() {
  const data = await api.get<{ items: User[] }>('/users')
  users.value = data.items ?? []
}

function openCreateUser() {
  editingUserId.value = 0
  Object.assign(userForm, { username: '', password: '', role: 'viewer', enabled: true })
  userDialog.value = true
}

function openEditUser(row: User) {
  editingUserId.value = row.id
  Object.assign(userForm, { username: row.username, password: '', role: row.role, enabled: row.enabled })
  userDialog.value = true
}

async function saveUser() {
  if (!userForm.username.trim()) return ElMessage.warning('请输入用户名')
  if (!editingUserId.value && userForm.password.length < 8) return ElMessage.warning('初始密码至少 8 位')
  userSaving.value = true
  try {
    if (editingUserId.value) {
      await api.put(`/users/${editingUserId.value}`, {
        role: userForm.role,
        enabled: userForm.enabled,
        password: userForm.password,
      })
      ElMessage.success('用户已更新')
    } else {
      await api.post('/users', {
        username: userForm.username.trim(),
        password: userForm.password,
        role: userForm.role,
      })
      ElMessage.success('用户已创建')
    }
    userDialog.value = false
    await loadUsers()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '保存失败')
  } finally {
    userSaving.value = false
  }
}

async function removeUser(row: User) {
  try {
    await ElMessageBox.confirm(`确认删除用户 “${row.username}”？该用户的会话会立即失效。`, '删除用户', { type: 'warning' })
    await api.del(`/users/${row.id}`)
    ElMessage.success('用户已删除')
    await loadUsers()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '删除失败')
  }
}

async function restartEngine() {
  try {
    const { value } = await ElMessageBox.prompt(
      '重启会重新加载代理配置和管理 HTTPS 监听，期间可能有短暂中断。请输入管理员密码确认。',
      '重启代理引擎',
      {
        inputType: 'password',
        inputPlaceholder: '管理员密码',
        inputValidator: (v: string) => (v ? true : '请输入管理员密码'),
      },
    )
    restarting.value = true
    await api.post('/system/restart', { password: value })
    ElMessage.info('正在重启代理引擎…')
    await new Promise((resolve) => setTimeout(resolve, 2500))
    window.location.reload()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') {
      ElMessage.error(err instanceof Error ? err.message : '重启失败')
    }
  } finally {
    restarting.value = false
  }
}

async function saveSettings(scope: 'basic' | 'security') {
  saving.value = true
  const wasHTTPS = window.location.protocol === 'https:'
  try {
    const basic = {
      log_retention_days: String(settings.log_retention_days),
      timezone: settings.timezone,
      config_version_limit: String(settings.config_version_limit),
      certificate_renew_before_days: String(settings.certificate_renew_before_days),
    }
    const security = {
      password_policy_enabled: settings.password_policy_enabled ? '1' : '0',
      password_min_length: String(settings.password_min_length),
      password_require_upper: settings.password_require_upper ? '1' : '0',
      password_require_lower: settings.password_require_lower ? '1' : '0',
      password_require_digit: settings.password_require_digit ? '1' : '0',
      password_require_symbol: settings.password_require_symbol ? '1' : '0',
      login_max_failures: String(settings.login_max_failures),
      login_lock_minutes: String(settings.login_lock_minutes),
      login_protection_enabled: settings.login_protection_enabled ? '1' : '0',
      session_idle_minutes: String(settings.session_idle_minutes),
      admin_https_enabled: settings.admin_https_enabled ? '1' : '0',
      admin_https_port: String(settings.admin_https_port),
      admin_https_certificate_id: String(settings.admin_https_certificate_id),
    }
    await api.put('/system/settings', scope === 'basic' ? basic : security)
    if (scope === 'security' && wasHTTPS && !settings.admin_https_enabled) {
      ElMessage.success('管理 HTTPS 已关闭，正在切换到 HTTP…')
      window.setTimeout(() => {
        const port = window.location.port === '18443' ? '18080' : (window.location.port || '18080')
        window.location.href = `http://${window.location.hostname}:${port}/settings`
      }, 1200)
      return
    }
    ElMessage.success(scope === 'basic' ? '基础设置已保存' : '安全策略已保存')
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '保存失败')
  } finally {
    saving.value = false
  }
}

async function backup() {
  try {
    const { value } = await ElMessageBox.prompt('备份包含数据库、证书私钥、ACME 账户和配置，不包含历史配置版本、日志和会话。请输入管理员密码确认下载。', '下载备份', {
      inputType: 'password',
      inputValidator: (v: string) => (v ? true : '请输入密码'),
    })
    const res = await fetch('/api/v1/system/backup', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() },
      body: JSON.stringify({ password: value }),
    })
    if (!res.ok) throw new Error(`备份失败（HTTP ${res.status}）`)
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `proxygate-${new Date().toISOString().slice(0, 10)}.zip`
    a.click()
    URL.revokeObjectURL(url)
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '备份失败')
  }
}

async function restoreBackup(file: UploadFile) {
  if (!file.raw) return
  try {
    await ElMessageBox.confirm(
      '恢复会替换当前数据库、证书、ACME 账户和配置，服务将自动重启；恢复后配置版本重置为 1。确认继续？',
      '恢复备份',
      { type: 'warning', confirmButtonText: '恢复并重启' },
    )
    const { value } = await ElMessageBox.prompt('恢复备份需要管理员密码确认。', '恢复备份', {
      inputType: 'password',
      inputPlaceholder: '管理员密码',
      inputValidator: (v: string) => (v ? true : '请输入管理员密码'),
    })
    const res = await fetch('/api/v1/system/restore', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/zip', 'X-Admin-Password': value, 'X-CSRF-Token': csrfToken() },
      body: file.raw,
    })
    const body = (await res.json().catch(() => null)) as { message?: string } | null
    if (!res.ok) throw new Error(body?.message || `恢复失败（HTTP ${res.status}）`)
    ElMessage.success('恢复已接受，服务正在重启…')
    await new Promise((resolve) => setTimeout(resolve, 3000))
    for (let i = 0; i < 60; i++) {
      try {
        const health = await fetch('/api/v1/system/health', { cache: 'no-store' })
        if (health.ok) {
          window.location.replace('/login')
          return
        }
      } catch {
        // service is restarting
      }
      await new Promise((resolve) => setTimeout(resolve, 1000))
    }
    ElMessage.error('恢复后服务未在预期时间内恢复，请刷新页面检查')
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '恢复失败')
  }
}

async function rollback(row: ConfigVersion) {
  try {
    await ElMessageBox.confirm(
      `确认回滚到配置版本 ${row.Version}？站点与应用服务会恢复到该快照，证书保持不变；当前配置会先保存为新版本。`,
      '回滚配置',
      { type: 'warning' },
    )
    await api.post(`/system/config-versions/${row.Version}/rollback`)
    ElMessage.success(`已回滚到版本 ${row.Version}`)
    await load()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '回滚失败')
  }
}

function versionDescription(description: string) {
  return ({ 'runtime update': '运行时配置更新' } as Record<string, string>)[description] || description || '-'
}

function onVersionSelection(rows: ConfigVersion[]) {
  selectedVersions.value = rows.map((r) => r.Version)
}

function canSelectVersion(row: ConfigVersion) {
  return versions.value[0]?.Version !== row.Version
}

async function batchRemoveVersions() {
  if (!selectedVersions.value.length) return
  try {
    await ElMessageBox.confirm(
      `确认删除选中的 ${selectedVersions.value.length} 个配置版本？删除后无法用于回滚。`,
      '批量删除配置版本',
      { type: 'warning' },
    )
    const { value } = await ElMessageBox.prompt('请输入管理员密码以确认删除', '管理员验证', {
      inputType: 'password',
      inputPlaceholder: '管理员密码',
      inputValidator: (v: string) => (v && v.length > 0 ? true : '请输入管理员密码'),
    })
    const data = await api.post<{ deleted: number }>('/system/config-versions/batch-delete', {
      versions: selectedVersions.value,
      password: value,
    })
    ElMessage.success(`已删除 ${data.deleted} 个配置版本`)
    selectedVersions.value = []
    await load()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '批量删除失败')
  }
}

async function removeVersion(row: ConfigVersion) {
  try {
    await ElMessageBox.confirm(
      `确认删除配置版本 ${row.Version}？删除后无法用于回滚。`,
      '删除配置版本',
      { type: 'warning' },
    )
    const { value } = await ElMessageBox.prompt('请输入管理员密码以确认删除', '管理员验证', {
      inputType: 'password',
      inputPlaceholder: '管理员密码',
      inputValidator: (v: string) => (v && v.length > 0 ? true : '请输入管理员密码'),
    })
    await api.del(`/system/config-versions/${row.Version}`, { password: value })
    ElMessage.success(`配置版本 ${row.Version} 已删除`)
    await load()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '删除失败')
  }
}

function openTokenCreate() {
  tokenForm.name = ''
  tokenForm.read_only = true
  tokenForm.expires_at = null
  tokenDialog.value = true
}

async function createToken() {
  if (!tokenForm.name.trim()) return ElMessage.warning('请填写令牌名称')
  tokenCreating.value = true
  try {
    const data = await api.post<{ token: string; item: APIToken }>('/api-tokens', {
      name: tokenForm.name.trim(),
      read_only: tokenForm.read_only,
      expires_at: tokenForm.expires_at ? tokenForm.expires_at.toISOString() : null,
    })
    createdToken.value = data.token
    tokenDialog.value = false
    tokenResultDialog.value = true
    await load()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '创建令牌失败')
  } finally {
    tokenCreating.value = false
  }
}

async function copyToken() {
  try {
    await navigator.clipboard.writeText(createdToken.value)
    ElMessage.success('令牌已复制')
  } catch {
    ElMessage.warning('复制失败，请手动选择文本复制')
  }
}

async function removeToken(row: APIToken) {
  try {
    await ElMessageBox.confirm(`确认吊销 API 令牌 “${row.name}”？使用该令牌的脚本会立即失效。`, '吊销令牌', { type: 'warning' })
    await api.del(`/api-tokens/${row.id}`)
    ElMessage.success('令牌已吊销')
    await load()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '吊销失败')
  }
}

async function revokeSession(row: UserSession) {
  try {
    await ElMessageBox.confirm(`确认吊销来自 ${row.client_ip || '未知地址'} 的会话？`, '吊销会话', { type: 'warning' })
    await api.del(`/auth/sessions/${row.id}`)
    ElMessage.success('会话已吊销')
    await load()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '吊销失败')
  }
}

async function revokeOtherSessions() {
  try {
    await ElMessageBox.confirm('确认退出除当前会话之外的所有登录会话？', '退出其他会话', { type: 'warning' })
    const data = await api.post<{ revoked: number }>('/auth/sessions/revoke-others')
    ElMessage.success(`已退出 ${data.revoked} 个其他会话`)
    await load()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(err instanceof Error ? err.message : '操作失败')
  }
}


onMounted(load)
</script>
