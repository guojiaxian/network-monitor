<template>
  <div>
    <h4 class="mb-3">系统设置</h4>
    <ul class="nav nav-pills nw-tabs mb-3">
      <li class="nav-item"><router-link class="nav-link" to="/settings/notify">📧 通知渠道</router-link></li>
      <li class="nav-item"><router-link class="nav-link" to="/settings/alert">🔔 告警策略</router-link></li>
      <li class="nav-item"><router-link class="nav-link" to="/settings/probe">🛰️ 探测配置</router-link></li>
      <li class="nav-item"><router-link class="nav-link" to="/settings/collect">📶 采集与测速</router-link></li>
      <li class="nav-item"><router-link class="nav-link" to="/settings/report">📈 报表推送</router-link></li>
      <li class="nav-item"><router-link class="nav-link" to="/settings/account">👤 用户与审计</router-link></li>
    </ul>
    <router-view />
  </div>
</template>
