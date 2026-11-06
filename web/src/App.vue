<template>
  <div v-if="showLayout" class="nw-layout">
    <aside class="nw-sidebar">
      <div class="brand">🛰️ NetWatch</div>
      <div class="nw-nav-group">监控展示</div>
      <router-link to="/">📋 总览</router-link>
      <router-link to="/alerts">🔔 告警中心</router-link>
      <router-link to="/probes">📡 探针状态</router-link>
      <router-link to="/line-status">📈 线路状态</router-link>
      <router-link to="/scan">🔎 IP 扫描</router-link>
      <router-link to="/history">📜 探测历史</router-link>
      <router-link to="/reports">📊 报表</router-link>
      <div class="nw-nav-group">配置管理</div>
      <router-link to="/lines">🌐 线路配置</router-link>
      <router-link to="/probes/manage">🛠️ 探针管理</router-link>
      <!-- 低频设置收进二级菜单：进入设置域自动展开，离开自动收起，无三级 -->
      <router-link to="/settings" class="nw-nav-parent">⚙️ 系统设置</router-link>
      <div class="nw-submenu" v-show="settingsOpen">
        <router-link to="/settings/notify">通知渠道</router-link>
        <router-link to="/settings/alert">告警策略</router-link>
        <router-link to="/settings/probe">探测配置</router-link>
        <router-link to="/settings/collect">采集与测速</router-link>
        <router-link to="/settings/report">报表推送</router-link>
        <router-link to="/settings/account">用户与审计</router-link>
      </div>
      <div class="foot">多出口宽带监控 · v0.1</div>
    </aside>
    <div class="nw-body">
      <header class="nw-topbar">
        <div class="nw-crumb">
          <span v-if="crumb.group" class="nw-crumb-group">{{ crumb.group }}</span>
          <span v-if="crumb.group" class="nw-crumb-sep">/</span>
          <span class="nw-crumb-page">{{ crumb.page }}</span>
        </div>
        <div class="nw-topbar-right">
          <a class="btn btn-sm btn-primary nw-screen-btn" href="#/screen" target="_blank" title="新窗口打开投屏大屏">🖥️ 大屏展示</a>
          <span v-if="user" class="nw-user" :title="user.role === 'admin' ? '管理员' : '观察者（只读）'">
            <span class="nw-avatar">{{ user.username.slice(0, 1).toUpperCase() }}</span>
            <span class="nw-username">{{ user.username }}</span>
          </span>
        </div>
      </header>
      <main class="nw-main">
        <router-view :key="$route.fullPath" />
      </main>
    </div>
  </div>
  <router-view v-else />
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from './api'

const route = useRoute()
// 登录/初始化/大屏页不带侧栏（大屏独占整屏）
const showLayout = computed(() => !['/login', '/setup', '/screen'].includes(route.path))
// 系统设置二级菜单：进入 /settings/* 自动展开，离开自动收起（无需手动状态）
const settingsOpen = computed(() => route.path.startsWith('/settings'))
// 顶栏面包屑：分组 / 页面（路由→标题映射，与侧栏菜单一致）
const TITLES = [
  ['/', '', '总览'], ['/alerts', '监控展示', '告警中心'], ['/probes/manage', '配置管理', '探针管理'],
  ['/probes/', '监控展示', '网页终端'], ['/probes', '监控展示', '探针状态'], ['/line-status', '监控展示', '线路状态'], ['/scan', '监控展示', 'IP 扫描'],
  ['/history', '监控展示', '探测历史'], ['/reports', '监控展示', '报表'],
  ['/lines/', '配置管理', '线路详情'], ['/lines', '配置管理', '线路配置'],
  ['/settings/notify', '系统设置', '通知渠道'], ['/settings/alert', '系统设置', '告警策略'],
  ['/settings/probe', '系统设置', '探测配置'], ['/settings/collect', '系统设置', '采集与测速'],
  ['/settings/report', '系统设置', '报表推送'], ['/settings/account', '系统设置', '用户与审计'],
]
const crumb = computed(() => {
  for (const [p, g, t] of TITLES) {
    if (p === route.path || (p.length > 1 && p.endsWith('/') && route.path.startsWith(p))) return { group: g, page: t }
  }
  return { group: '', page: '总览' }
})
// 当前登录用户（顶栏显示；未登录/会话失效时静默隐藏）。
// App 在登录页就已挂载，me 须等布局出现（会话就绪）后再拉，登录后/watch 即触发
const user = ref(null)
async function fetchMe() {
  try { user.value = await api.get('/api/admin/me') } catch { user.value = null }
}
watch(showLayout, v => { if (v) fetchMe() }, { immediate: true })
</script>
