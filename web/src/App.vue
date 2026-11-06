<template>
  <div v-if="showLayout" class="nw-layout">
    <aside class="nw-sidebar">
      <div class="brand">🛰️ NetWatch</div>
      <div class="nw-nav-group">监控展示</div>
      <router-link to="/">📋 总览</router-link>
      <router-link to="/alerts">🔔 告警中心</router-link>
      <router-link to="/probes">📡 探针状态</router-link>
      <router-link to="/scan">🔎 IP 扫描</router-link>
      <router-link to="/history">📜 探测历史</router-link>
      <router-link to="/reports">📊 报表</router-link>
      <a href="#/screen" target="_blank">🖥️ 大屏展示</a>
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
    <main class="nw-main">
      <router-view :key="$route.fullPath" />
    </main>
  </div>
  <router-view v-else />
</template>

<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'

const route = useRoute()
// 登录/初始化/大屏页不带侧栏（大屏独占整屏）
const showLayout = computed(() => !['/login', '/setup', '/screen'].includes(route.path))
// 系统设置二级菜单：进入 /settings/* 自动展开，离开自动收起（无需手动状态）
const settingsOpen = computed(() => route.path.startsWith('/settings'))
</script>
