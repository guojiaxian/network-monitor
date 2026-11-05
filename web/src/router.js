import { createRouter, createWebHashHistory } from 'vue-router'
import { bootstrap } from './api'

const routes = [
  { path: '/setup', component: () => import('./views/SetupView.vue') },
  { path: '/login', component: () => import('./views/LoginView.vue') },
  { path: '/', component: () => import('./views/DashboardView.vue'), meta: { auth: true } },
  { path: '/lines', component: () => import('./views/LinesView.vue'), meta: { auth: true } },
  { path: '/lines/:id', component: () => import('./views/LineDetailView.vue'), meta: { auth: true } },
  { path: '/probes', component: () => import('./views/ProbesStatusView.vue'), meta: { auth: true } }, // 探针状态：只读展示
  { path: '/probes/manage', component: () => import('./views/ProbesManageView.vue'), meta: { auth: true } }, // 探针管理：绑定/运维/升级
  { path: '/probes/:id/term', component: () => import('./views/SshTermView.vue'), meta: { auth: true } },
  { path: '/alerts', component: () => import('./views/AlertsView.vue'), meta: { auth: true } },
  { path: '/history', component: () => import('./views/HistoryView.vue'), meta: { auth: true } },
  { path: '/reports', component: () => import('./views/ReportsView.vue'), meta: { auth: true } },
  // 系统设置：页内 Tab，各 Tab 独立子路由可直达
  {
    path: '/settings',
    component: () => import('./views/settings/SettingsLayout.vue'),
    meta: { auth: true },
    children: [
      { path: '', redirect: '/settings/notify' },
      { path: 'notify', component: () => import('./views/settings/NotifySettings.vue'), meta: { auth: true } },
      { path: 'alert', component: () => import('./views/settings/AlertSettings.vue'), meta: { auth: true } },
      { path: 'probe', component: () => import('./views/settings/ProbeSettings.vue'), meta: { auth: true } },
      { path: 'collect', component: () => import('./views/settings/CollectSettings.vue'), meta: { auth: true } },
      { path: 'report', component: () => import('./views/settings/ReportSettings.vue'), meta: { auth: true } },
      { path: 'account', component: () => import('./views/settings/AccountAudit.vue'), meta: { auth: true } },
    ],
  },
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
