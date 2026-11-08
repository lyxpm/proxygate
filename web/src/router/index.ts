import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/LoginView.vue'),
      meta: { public: true, title: '登录' },
    },
    {
      path: '/',
      component: () => import('@/layouts/DefaultLayout.vue'),
      redirect: '/dashboard',
      children: [
        { path: 'dashboard', name: 'dashboard', component: () => import('@/views/DashboardView.vue'), meta: { title: '仪表盘' } },
        { path: 'topology', name: 'topology', component: () => import('@/views/TopologyView.vue'), meta: { title: '业务拓扑' } },
        { path: 'applications', name: 'applications', component: () => import('@/views/ApplicationsView.vue'), meta: { title: '应用服务' } },
        { path: 'sites', name: 'sites', component: () => import('@/views/SitesView.vue'), meta: { title: '站点管理' } },
        { path: 'certificates', name: 'certificates', component: () => import('@/views/CertificatesView.vue'), meta: { title: 'SSL 证书' } },
        { path: 'statistics', name: 'statistics', component: () => import('@/views/StatisticsView.vue'), meta: { title: '流量分析' } },
        { path: 'logs', name: 'logs', component: () => import('@/views/LogsView.vue'), meta: { title: '日志中心' } },
        { path: 'settings', name: 'settings', component: () => import('@/views/SettingsView.vue'), meta: { title: '系统设置' } },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/dashboard' },
  ],
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (to.meta.public) {
    if (to.name === 'login' && auth.user) return '/dashboard'
    return true
  }
  try {
    await auth.fetchMe()
    return true
  } catch {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
})

router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title as string} · ProxyGate` : 'ProxyGate'
})

export default router
