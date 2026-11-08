import { createRouter, createWebHashHistory } from 'vue-router'
import { bootstrap } from './api'

const routes = [
  { path: '/setup', component: () => import('./views/SetupView.vue') },
  { path: '/login', component: () => import('./views/LoginView.vue') },
  { path: '/', component: () => import('./views/DashboardView.vue'), meta: { auth: true } },
  { path: '/lines', component: () => import('./views/LinesView.vue'), meta: { auth: true } },
  { path: '/floors', component: () => import('./views/FloorConfigView.vue'), meta: { auth: true } }, // 楼层配置
  { path: '/floors/:id', component: () => import('./views/FloorDetailView.vue'), meta: { auth: true } }, // 楼层详情：联通率+在线 IP
  { path: '/lines/:id', component: () => import('./views/LineDetailView.vue'), meta: { auth: true } },
  { path: '/probes', component: () => import('./views/ProbesStatusView.vue'), meta: { auth: true } }, // 探针状态：只读展示
  { path: '/line-status', component: () => import('./views/LineStatusView.vue'), meta: { auth: true } }, // 线路状态：各线路关键运行信息
  { path: '/floor-status', component: () => import('./views/FloorStatusView.vue'), meta: { auth: true } }, // 楼层状态：以楼层为单位聚合网络状况

  { path: '/scan', component: () => import('./views/ScanView.vue'), meta: { auth: true } }, // 楼层 IP 扫描（触发为 POST，观察者会被 403）
  { path: '/probes/manage', component: () => import('./views/ProbesManageView.vue'), meta: { auth: true } }, // 探针配置：绑定/运维/升级
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
      { path: 'collect', component: () => import('./views/settings/CollectSettings.vue'), meta: { auth: true } },
      { path: 'report', component: () => import('./views/settings/ReportSettings.vue'), meta: { auth: true } },
      { path: 'account', component: () => import('./views/settings/AccountAudit.vue'), meta: { auth: true } },
    ],
  },
  { path: '/probing', component: () => import('./views/settings/ProbeSettings.vue'), meta: { auth: true } }, // 探测配置（自系统设置拆出为一级页）
  { path: '/settings/probe', redirect: '/probing' }, // 旧深链重定向
  { path: '/screen', component: () => import('./views/ScreenView.vue') }, // 大屏（线路口径）：公开只读
  { path: '/screen/floor', component: () => import('./views/ScreenView.vue') }, // 大屏（楼层口径）：同一组件按路由切换，地址可分别投屏
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
