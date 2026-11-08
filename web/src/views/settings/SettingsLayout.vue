<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <div>
        <h4 class="mb-0">⚙️ 系统设置</h4>
        <div class="text-secondary small mt-1">通知 / 告警 / 采集 / 报表 / 账号，各页独立保存</div>
      </div>
    </div>
    <!-- 二级页签：下划线标题式（与探测历史一致）；侧栏不再挂二级菜单 -->
    <div class="nw-tabs mb-3">
      <router-link v-for="t in tabs" :key="t.path" :to="t.path" :class="{ active: route.path === t.path }">{{ t.label }}</router-link>
    </div>
    <router-view />
  </div>
</template>

<script setup>
import { useRoute } from 'vue-router'

const route = useRoute()
// '/settings' 本身重定向到 notify（router.js），route.path 恒为五个子页之一，精确匹配即可
const tabs = [
  { path: '/settings/notify', label: '通知渠道' },
  { path: '/settings/alert', label: '告警策略' },
  { path: '/settings/collect', label: '采集与测速' },
  { path: '/settings/report', label: '报表推送' },
  { path: '/settings/account', label: '用户与审计' },
]
</script>
