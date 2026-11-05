<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <h4 class="mb-0">告警中心</h4>
      <select v-model="filter" class="form-select form-select-sm w-auto ms-3" @change="load">
        <option value="firing">进行中</option>
        <option value="resolved">已恢复</option>
        <option value="all">全部</option>
      </select>
      <button class="btn btn-sm btn-outline-secondary ms-auto" @click="load">刷新</button>
    </div>
    <p class="text-secondary small mb-2">维护期需要暂停某线路告警？到 <router-link to="/settings/alert">系统设置 → 告警策略</router-link> 创建静默窗口。</p>

    <div class="nw-card">
      <h6>告警列表</h6>
      <table class="table nw-table table-sm align-middle">
        <thead><tr><th>级别</th><th>类型</th><th>标题</th><th>开始时间</th><th>恢复时间</th><th>状态</th></tr></thead>
        <tbody>
          <tr v-for="a in alerts" :key="a.id">
            <td><span class="badge" :class="sevCls(a.severity)">{{ a.severity }}</span></td>
            <td class="small">{{ kindText(a.kind) }}</td>
            <td>{{ a.title }}</td>
            <td class="small">{{ fmt(a.starts_at) }}</td>
            <td class="small">{{ a.ends_at ? fmt(a.ends_at) : '—' }}</td>
            <td>
              <span class="badge" :class="a.status === 'firing' ? 'bg-danger' : 'bg-success'">
                {{ a.status === 'firing' ? '进行中' : '已恢复' }}
              </span>
            </td>
          </tr>
          <tr v-if="!alerts.length"><td colspan="6" class="text-secondary text-center py-4">暂无告警</td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import dayjs from 'dayjs'
import { api } from '../api'

const alerts = ref([]), filter = ref('firing')

const kinds = { line_down: '线路中断', floor_down: '楼层中断', blind: '监控盲区', degraded: '线路降质', speed_low: '带宽不达标', probe_conflict: '探针数据冲突', site_down: '网站外网不可达', site_conflict: '外网探针冲突', cert_expiring: '证书即将过期', monitor_down: '内网服务不可达' }
function kindText(k) { return kinds[k] || k }
function sevCls(s) { return { P0: 'bg-dark', P1: 'bg-danger', P2: 'bg-warning text-dark' }[s] || 'bg-secondary' }
function fmt(t) { return dayjs(t).format('YYYY-MM-DD HH:mm:ss') }

async function load() {
  alerts.value = await api.get(`/api/admin/alerts?status=${filter.value}&limit=300`)
}

onMounted(load)
</script>
