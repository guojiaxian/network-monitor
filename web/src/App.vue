<template>
  <div v-if="showLayout" class="nw-layout">
    <aside class="nw-sidebar">
      <div class="brand">物探中心网络监控系统<div class="brand-en">NetWatch</div></div>
      <div class="nw-nav-group">监控展示</div>
      <router-link to="/">总览</router-link>
      <router-link to="/alerts">告警中心</router-link>
      <router-link to="/probes">探针状态</router-link>
      <router-link to="/line-status">线路状态</router-link>
      <router-link to="/floor-status">楼层状态</router-link>
      <router-link to="/scan">IP 扫描</router-link>
      <router-link to="/history">探测历史</router-link>
      <router-link to="/reports">报表</router-link>
      <div class="nw-nav-group">配置管理</div>
      <router-link to="/floors">楼层配置</router-link>
      <router-link to="/lines">线路配置</router-link>
      <router-link to="/probes/manage">探针配置</router-link>
      <router-link to="/probing">探测配置</router-link>
      <!-- 系统设置一级直达：二级页签在页内（SettingsLayout.vue），侧栏不再挂下拉菜单 -->
      <router-link to="/settings">系统设置</router-link>
      <div class="foot">物探中心网络监控系统 · v{{ appVersion }}</div>
    </aside>
    <div class="nw-body">
      <header class="nw-topbar">
        <div class="nw-crumb">
          <span v-if="crumb.group" class="nw-crumb-group">{{ crumb.group }}</span>
          <span v-if="crumb.group" class="nw-crumb-sep">/</span>
          <span class="nw-crumb-page">{{ crumb.page }}</span>
        </div>
        <div class="nw-topbar-right">
          <span v-if="pwdMsg" class="small text-success">{{ pwdMsg }}</span>
          <button type="button" class="btn btn-sm btn-outline-secondary nw-screen-btn" @click="docOpen = true" title="自动探测与监测机制说明">监测说明</button>
          <a class="btn btn-sm btn-primary nw-screen-btn" href="#/screen" target="_blank" title="新窗口打开投屏大屏">大屏展示</a>
          <span v-if="user" class="nw-user-wrap" @click.stop>
            <span class="nw-user" :title="user.role === 'admin' ? '管理员' : '观察者（只读）'" @click="menuOpen = !menuOpen">
              <span class="nw-avatar">{{ user.username.slice(0, 1).toUpperCase() }}</span>
              <span class="nw-username">{{ user.username }}</span>
              <span class="nw-caret">▾</span>
            </span>
            <div v-if="menuOpen" class="nw-user-menu">
              <div class="nw-user-menu-role">{{ user.role === 'admin' ? '管理员' : '观察者（只读）' }}</div>
              <button type="button" @click="openPwd()">修改密码</button>
              <button type="button" class="nw-user-logout" @click="doLogout()">退出登录</button>
            </div>
          </span>
        </div>
      </header>
      <main class="nw-main">
        <router-view :key="$route.fullPath" />
      </main>
    </div>
  </div>
  <router-view v-else />
  <ChangePasswordModal :show="pwdOpen" @close="pwdOpen = false" @saved="onPwdSaved" />
  <MonitorDocModal :show="docOpen" @close="docOpen = false" />
</template>

<script setup>
import { computed, ref, watch, onMounted, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import { api } from './api'
import ChangePasswordModal from './components/ChangePasswordModal.vue'
import MonitorDocModal from './components/MonitorDocModal.vue'

const route = useRoute()
// 前端版本号由 vite.config.js 从 web/package.json 注入（发版时与后端 VERSION 同步）
const appVersion = __APP_VERSION__
// 登录/初始化/大屏页不带侧栏（大屏独占整屏；/screen/floor 为楼层口径，同样独占）
const showLayout = computed(() => !['/login', '/setup'].includes(route.path) && !route.path.startsWith('/screen'))
// 顶栏面包屑：分组 / 页面（路由→标题映射，与侧栏菜单一致）
const TITLES = [
  ['/', '', '总览'], ['/alerts', '监控展示', '告警中心'], ['/probes/manage', '配置管理', '探针配置'], ['/floors/', '监控展示', '楼层详情'], ['/floors', '配置管理', '楼层配置'], ['/probing', '配置管理', '探测配置'],
  ['/probes/', '监控展示', '网页终端'], ['/probes', '监控展示', '探针状态'], ['/line-status', '监控展示', '线路状态'], ['/floor-status', '监控展示', '楼层状态'], ['/scan', '监控展示', 'IP 扫描'],
  ['/history', '监控展示', '探测历史'], ['/reports', '监控展示', '报表'],
  ['/lines/', '配置管理', '线路详情'], ['/lines', '配置管理', '线路配置'],
  ['/settings/notify', '系统设置', '通知渠道'], ['/settings/alert', '系统设置', '告警策略'],
  ['/probing', '配置管理', '探测配置'], ['/settings/collect', '系统设置', '采集与测速'],
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
// 布局页预留滚动条槽位：明细行数多少导致的滚动条出现/消失不再引起整页 15px 横移；
// 大屏/登录页不滚动不预留（避免深色大屏右缘出现浅色条）
watch(showLayout, v => {
  if (v) fetchMe()
  document.documentElement.classList.toggle('nw-gutter', v)
}, { immediate: true })

// 顶栏用户菜单：点击头像区展开（修改密码/退出登录），点击页面其他位置收起
const menuOpen = ref(false)
function onDocClick() { menuOpen.value = false }
onMounted(() => document.addEventListener('click', onDocClick))
onBeforeUnmount(() => document.removeEventListener('click', onDocClick))

// 修改密码：成功后其他设备会话已下线，当前会话保持；顶栏短暂提示
const pwdOpen = ref(false)
const docOpen = ref(false) // 监测说明弹窗（机制文档维护点：components/MonitorDocModal.vue）
const pwdMsg = ref('')
let pwdMsgTimer = null
function openPwd() { menuOpen.value = false; pwdOpen.value = true }
function onPwdSaved() {
  pwdMsg.value = '密码已修改'
  clearTimeout(pwdMsgTimer)
  pwdMsgTimer = setTimeout(() => (pwdMsg.value = ''), 4000)
}

async function doLogout() {
  menuOpen.value = false
  try { await api.post('/api/admin/logout') } catch {} // 会话可能已失效，失败也照样回登录页
  user.value = null
  window.location.hash = '#/login'
}
</script>
