<template>
  <el-container class="layout">
    <el-aside class="sidebar" :width="collapsed ? '64px' : '204px'">
      <div class="brand" :class="{ collapsed }">
        <div v-if="!collapsed" class="brand-mark">P</div>
        <div v-if="!collapsed" class="brand-copy">
          <div>ProxyGate</div>
          <div class="brand-sub">代理网关</div>
        </div>
        <el-button class="sidebar-toggle" text circle @click="collapsed = !collapsed">
          <el-icon :size="18"><Fold v-if="!collapsed" /><Expand v-else /></el-icon>
        </el-button>
      </div>
      <el-menu :default-active="activeMenu" :collapse="collapsed" router>
        <el-menu-item v-for="item in menu" :key="item.path" :index="item.path">
          <el-icon><component :is="item.icon" /></el-icon>
          <template #title>{{ item.title }}</template>
        </el-menu-item>
      </el-menu>
      <div v-if="!collapsed" class="sidebar-footer">
        v0.1.0 · ProxyGate<br />
        {{ auth.user?.username || 'admin' }}
      </div>
    </el-aside>
    <el-container>
      <el-header class="main-header">
        <div class="header-left">
          <div class="header-title">{{ title }}</div>
        </div>
        <div class="header-right">
          <el-tag v-if="engineRunning" type="success" effect="plain" size="small">代理运行中</el-tag>
          <el-tag v-if="auth.user?.role === 'viewer'" type="info" effect="plain" size="small">只读账号</el-tag>
          <el-dropdown @command="onCommand">
            <span style="cursor: pointer; display: flex; align-items: center; gap: 6px">
              <el-avatar :size="28" style="background: #0ea5a4">{{ initials }}</el-avatar>
              <span>{{ auth.user?.username || 'admin' }}</span>
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="settings">系统设置</el-dropdown-item>
                <el-dropdown-item command="password">修改密码</el-dropdown-item>
                <el-dropdown-item command="logout" divided>退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>
      <el-main class="content">
        <router-view />
      </el-main>
    </el-container>

    <el-dialog v-model="passwordDialog" title="修改密码" width="440px" destroy-on-close>
      <el-form label-position="top">
        <el-form-item label="当前密码">
          <el-input v-model="passwordForm.old_password" type="password" show-password autocomplete="current-password" />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input v-model="passwordForm.new_password" type="password" show-password autocomplete="new-password" />
        </el-form-item>
        <el-form-item label="确认新密码">
          <el-input v-model="passwordForm.confirm" type="password" show-password autocomplete="new-password" />
        </el-form-item>
        <div class="form-hint">修改成功后当前密码和所有会话都会失效，需要重新登录。</div>
      </el-form>
      <template #footer>
        <el-button @click="passwordDialog = false">取消</el-button>
        <el-button type="primary" :loading="changingPassword" @click="submitPassword">更新密码</el-button>
      </template>
    </el-dialog>
  </el-container>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  ArrowDown,
  Connection,
  Document,
  Expand,
  Files,
  Fold,
  Lock,
  Odometer,
  Setting,
  Share,
  TrendCharts,
  User,
} from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'
import { api } from '@/api/client'
import type { EngineStatus } from '@/api/types'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const collapsed = ref(false)
const engineRunning = ref(false)
const passwordDialog = ref(false)
const changingPassword = ref(false)
const passwordForm = reactive({ old_password: '', new_password: '', confirm: '' })

const menu = [
  { path: '/dashboard', title: '仪表盘', icon: Odometer },
  { path: '/topology', title: '业务拓扑', icon: Share },
  { path: '/applications', title: '应用服务', icon: Files },
  { path: '/sites', title: '站点管理', icon: Connection },
  { path: '/certificates', title: 'SSL 证书', icon: Lock },
  { path: '/statistics', title: '流量分析', icon: TrendCharts },
  { path: '/logs', title: '日志中心', icon: Document },
  { path: '/settings', title: '系统设置', icon: Setting },
]

const title = computed(() => (route.meta.title as string) || '仪表盘')
const activeMenu = computed(() => {
  const found = menu.find((m) => route.path === m.path || route.path.startsWith(m.path + '/'))
  return found?.path || '/dashboard'
})
const initials = computed(() => (auth.user?.username || 'admin').slice(0, 1).toUpperCase())

async function loadStatus() {
  try {
    const info = await api.get<{ engine: EngineStatus }>('/system/info')
    engineRunning.value = info.engine.running
  } catch {
    engineRunning.value = false
  }
}

function onUnauthorized() {
  auth.clear()
  router.push({ name: 'login', query: { redirect: route.fullPath } })
}

async function onCommand(cmd: string) {
  if (cmd === 'settings') {
    router.push('/settings')
    return
  }
  if (cmd === 'password') {
    Object.assign(passwordForm, { old_password: '', new_password: '', confirm: '' })
    passwordDialog.value = true
    return
  }
  if (cmd === 'logout') {
    await auth.logout()
    ElMessage.success('已退出登录')
    router.push('/login')
  }
}

async function submitPassword() {
  if (!passwordForm.old_password || !passwordForm.new_password) {
    ElMessage.warning('请填写完整密码')
    return
  }
  if (passwordForm.new_password !== passwordForm.confirm) {
    ElMessage.warning('两次输入的新密码不一致')
    return
  }
  changingPassword.value = true
  try {
    await api.put('/auth/password', {
      old_password: passwordForm.old_password,
      new_password: passwordForm.new_password,
    })
    passwordDialog.value = false
    ElMessage.success('密码已更新，请重新登录')
    auth.clear()
    router.push('/login')
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '修改密码失败')
  } finally {
    changingPassword.value = false
  }
}

let timer: number | undefined
onMounted(() => {
  loadStatus()
  timer = window.setInterval(() => {
    if (document.visibilityState === 'visible') loadStatus()
  }, 30_000)
  window.addEventListener('pm:unauthorized', onUnauthorized)
  document.addEventListener('visibilitychange', onVisibilityChange)
})
onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
  window.removeEventListener('pm:unauthorized', onUnauthorized)
  document.removeEventListener('visibilitychange', onVisibilityChange)
})

function onVisibilityChange() {
  if (document.visibilityState === 'visible') loadStatus()
}
</script>
