<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <h4 class="mb-0">探针状态</h4>
      <span class="text-secondary small ms-3">每 15 秒自动刷新</span>
      <span v-if="err" class="text-danger small ms-3">{{ err }}</span>
      <router-link to="/probes/manage" class="btn btn-sm btn-outline-secondary ms-auto">🛠️ 绑定 / 运维操作 → 探针管理</router-link>
    </div>

    <div class="nw-card mb-3">
      <table class="table nw-table table-sm align-middle">
        <thead>
          <tr><th>名称</th><th>分组</th><th>绑定线路</th><th>状态</th><th>版本</th><th>出口源 IP</th><th>默认网关</th><th>最后心跳</th></tr>
        </thead>
        <tbody>
          <tr v-for="p in probes" :key="p.probe.id">
            <td><b>{{ p.probe.name || p.probe.hostname }}</b> <code class="small text-secondary" :title="p.probe.id">{{ p.probe.id.slice(0, 8) }}</code></td>
            <td><span class="badge" :class="isExt(p) ? 'bg-success' : 'bg-primary'">{{ isExt(p) ? '外网组' : '内网组' }}</span></td>
            <td>{{ lineName(p) }}</td>
            <td><StatusBadge :status="p.status && p.status.status || 'unknown'" /></td>
            <td>{{ p.probe.version || '—' }}</td>
            <td><code>{{ p.probe.source_ip || '—' }}</code></td>
            <td><code>{{ p.probe.gateway || '—' }}</code></td>
            <td class="small text-secondary">{{ fmtTime(p.probe.last_seen) }}</td>
          </tr>
          <tr v-if="!probes.length">
            <td colspan="8" class="text-secondary py-4 text-center">
              暂无探针注册。请在楼层 VM 上执行：<code>deploy/install-agent.sh http://&lt;中心IP&gt; &lt;注册令牌&gt;</code>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="text-secondary small">本页为只读总览；探针的绑定线路、分组、SSH 运维与版本升级请前往 <router-link to="/probes/manage">探针管理</router-link>。</p>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import { fmtTime } from '../utils'

const probes = ref([]), lines = ref([]), err = ref('')
let timer = null

const isExt = (p) => (p.probe.group || 'internal') === 'external'
function lineName(p) {
  if (isExt(p)) return '探测官网'
  const l = lines.value.find(x => x.id === p.probe.line_id)
  return l ? l.name : '未绑定'
}

async function load() {
  try {
    ;[probes.value, lines.value] = await Promise.all([
      api.get('/api/admin/probes'), api.get('/api/admin/lines'),
    ])
    err.value = ''
  } catch (e) { err.value = e.message }
}

onMounted(() => { load(); timer = setInterval(load, 15000) })
onUnmounted(() => clearInterval(timer))
</script>
