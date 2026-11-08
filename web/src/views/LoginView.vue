<template>
  <div class="login-page">
    <div class="login-hero">
      <div class="brand" style="padding: 0">
        <div class="brand-mark">P</div>
        <div>
          <div style="font-size: 18px">ProxyGate</div>
          <div class="brand-sub">代理网关</div>
        </div>
      </div>
      <h1>把反向代理、证书与流量<br />收进一个控制台</h1>
      <p>
        基于 Go 的高性能代理引擎，支持多域名、负载均衡、健康检查、ACME 自动证书、实时日志与站点级流量分析。
      </p>
      <div class="feature-list">
        <div class="feature-item"><b>01</b> 多站点 / 多应用服务 / 多策略负载均衡</div>
        <div class="feature-item"><b>02</b> HTTP-01 与 DNS-01 通配符证书自动化</div>
        <div class="feature-item"><b>03</b> 配置热更新，修改站点无需重启</div>
        <div class="feature-item"><b>04</b> 实时访问日志与可视化拓扑</div>
      </div>
    </div>
    <div class="login-panel">
      <div class="login-box">
        <h2>欢迎回来</h2>
        <p class="muted" style="margin-top: 0">使用管理员账号登录管理后台</p>
        <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @keyup.enter="submit">
          <el-form-item label="用户名" prop="username">
            <el-input v-model="form.username" size="large" placeholder="admin" autocomplete="username" />
          </el-form-item>
          <el-form-item label="密码" prop="password">
            <el-input
              v-model="form.password"
              type="password"
              size="large"
              show-password
              placeholder="请输入密码"
              autocomplete="current-password"
            />
          </el-form-item>
          <el-form-item v-if="totpVisible" label="动态验证码">
            <el-input
              v-model="form.totp_code"
              size="large"
              maxlength="6"
              placeholder="请输入 6 位动态验证码"
              autocomplete="one-time-code"
            />
          </el-form-item>
          <div v-else class="login-totp-toggle">
            <el-button link type="primary" size="small" @click="totpVisible = true">使用动态验证码</el-button>
          </div>
          <el-button type="primary" size="large" style="width: 100%; margin-top: 8px" :loading="loading" @click="submit">
            登录
          </el-button>
        </el-form>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()
const formRef = ref<FormInstance>()
const loading = ref(false)
const totpVisible = ref(false)
const form = reactive({ username: 'admin', password: '', totp_code: '' })
const rules: FormRules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

async function submit() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return
  loading.value = true
  try {
    await auth.login(form.username, form.password, form.totp_code)
    ElMessage.success('登录成功')
    const redirect = (route.query.redirect as string) || '/dashboard'
    router.replace(redirect)
  } catch (err) {
    const message = err instanceof Error ? err.message : '登录失败'
    if (message.includes('动态验证码')) totpVisible.value = true
    ElMessage.error(message)
  } finally {
    loading.value = false
  }
}
</script>
