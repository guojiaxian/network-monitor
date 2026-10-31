import { createRouter, createWebHashHistory } from 'vue-router'
import { bootstrap } from './api'

const routes = [
  { path: '/setup', component: () => import('./views/SetupView.vue') },
  { path: '/login', component: () => import('./views/LoginView.vue') },
  { path: '/', component: () => import('./views/DashboardView.vue'), meta: { auth: true } },
  { path: '/lines', component: () => import('./views/LinesView.vue'), meta: { auth: true } },
  { path: '/lines/:id', component: () => import('./views/LineDetailView.vue'), meta: { auth: true } },
  { path: '/probes', component: () => import('./views/ProbesView.vue'), meta: { auth: true } },
  { path: '/probes/:id/term', component: () => import('./views/SshTermView.vue'), meta: { auth: true } },
  { path: '/alerts', component: () => import('./views/AlertsView.vue'), meta: { auth: true } },
  { path: '/history', component: () => import('./views/HistoryView.vue'), meta: { auth: true } },
  { path: '/reports', component: () => import('./views/ReportsView.vue'), meta: { auth: true } },
  { path: '/settings', component: () => import('./views/SettingsView.vue'), meta: { auth: true } },
  { path: '/screen', component: () => import('./views/ScreenView.vue') }, // 大屏：公开只读
]

export const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

let ready = false

router.beforeEach(async (to) => {
  if (!ready) {
    try {
      const b = await bootstrap()
      if (!b.initialized && to.path !== '/setup') return '/setup'
      if (b.initialized && to.path === '/setup') return '/login'
      ready = true
    } catch {
      // 中心端不可达：放行到页面由视图内提示
    }
  }
  return true
})
