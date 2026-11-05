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
          <tr><th style="width:28px"></th><th>名称</th><th>分组</th><th>绑定线路</th><th>状态</th><th>自健康 (CPU/内存/磁盘)</th><th>出口公网 IP</th><th>出口源 IP</th><th>默认网关</th><th>最后心跳</th></tr>
        </thead>
        <tbody>
          <template v-for="p in probes" :key="p.probe.id">
            <tr>
              <td><a href="javascript:void(0)" class="text-decoration-none" @click="toggle(p.probe.id)">{{ expanded[p.probe.id] ? '▾' : '▸' }}</a></td>
              <td><b>{{ p.probe.name || p.probe.hostname }}</b> <code class="small text-secondary" :title="p.probe.id">{{ p.probe.id.slice(0, 8) }}</code></td>
              <td><span class="badge" :class="isExt(p) ? 'bg-success' : 'bg-primary'">{{ isExt(p) ? '外网组' : '内网组' }}</span></td>
              <td>{{ lineName(p) }}</td>
              <td><StatusBadge :status="p.status && p.status.status || 'unknown'" /></td>
              <td>
                <span v-if="health(p)" class="small">
                  <span :class="p.status.cpu_pct > 90 ? 'text-danger' : ''">{{ Math.round(p.status.cpu_pct) }}%</span> /
                  <span :class="p.status.mem_pct > 90 ? 'text-danger' : ''">{{ Math.round(p.status.mem_pct) }}%</span> /
                  <span :class="p.status.disk_pct > 90 ? 'text-danger' : ''">{{ Math.round(p.status.disk_pct) }}%</span>
                  <span v-if="clockOff(p)" class="text-warning ms-1" :title="'探针时钟偏移 ' + Math.round(p.status.clock_offset_ms / 1000) + ' 秒，请为其配置 NTP'">⏰偏移</span>
                </span>
                <span v-else class="text-secondary small">—</span>
              </td>
              <td>
                <code>{{ p.status && p.status.egress_ip || '—' }}</code>
                <span v-if="p.status && p.status.egress_isp" class="text-secondary small ms-1" :title="p.status.egress_geo">{{ p.status.egress_isp }}</span>
                <span v-if="p.status && p.status.egress_ipv6" class="text-secondary small" title="出口 IPv6">+v6</span>
              </td>
              <td><code>{{ p.probe.source_ip || '—' }}</code></td>
              <td><code>{{ p.probe.gateway || '—' }}</code></td>
              <td class="small text-secondary">{{ fmtTime(p.probe.last_seen) }}</td>
            </tr>
            <tr v-if="expanded[p.probe.id]">
              <td colspan="10" class="bg-light">
                <div class="row g-2 small p-1">
                  <div class="col-md-6">
                    <b class="d-block mb-1">探针资产（注册时自动采集）</b>
                    <span v-if="assetItems(p).length">
                      <span v-for="a in assetItems(p)" :key="a[0]" class="me-3 d-inline-block"><span class="text-secondary">{{ a[1] }}：</span><code>{{ a[0] }}</code></span>
                    </span>
                    <span v-else class="text-secondary">暂无（该探针注册于旧版本，重启探针后自动补采）</span>
                  </div>
                  <div class="col-md-6">
                    <b class="d-block mb-1">出口信息（每 5 分钟自动探测）</b>
                    <span class="me-3 d-inline-block"><span class="text-secondary">公网 IPv4：</span><code>{{ p.status && p.status.egress_ip || '未探得' }}</code></span>
                    <span class="me-3 d-inline-block"><span class="text-secondary">运营商：</span><code>{{ p.status && p.status.egress_isp || '未探得' }}</code></span>
                    <span class="me-3 d-inline-block"><span class="text-secondary">归属地：</span><code>{{ (p.status && p.status.egress_geo) || '未探得' }}</code></span>
                    <span class="me-3 d-inline-block"><span class="text-secondary">IPv6：</span><code>{{ (p.status && p.status.egress_ipv6) || '不支持/未探得' }}</code></span>
                    <span v-if="p.status && p.status.clock_offset_ms" class="d-inline-block"><span class="text-secondary">时钟偏移：</span><code>{{ (p.status.clock_offset_ms / 1000).toFixed(1) }}s</code></span>
                  </div>
                </div>
              </td>
            </tr>
          </template>
          <tr v-if="!probes.length">
            <td colspan="10" class="text-secondary py-4 text-center">
              暂无探针注册。请在楼层 VM 上执行：<code>deploy/install-agent.sh http://&lt;中心IP&gt; &lt;注册令牌&gt;</code>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="text-secondary small">本页为只读总览；探针的绑定线路、分组、SSH 运维与版本升级请前往 <router-link to="/probes/manage">探针管理</router-link>。探针自身内存/磁盘连续超 90% 会触发 P2"探针资源紧张"告警。</p>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, onUnmounted } from 'vue'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import { fmtTime } from '../utils'

const probes = ref([]), lines = ref([]), err = ref('')
const expanded = reactive({})
let timer = null

const isExt = (p) => (p.probe.group || 'internal') === 'external'
function lineName(p) {
  if (isExt(p)) return '探测官网'
  const l = lines.value.find(x => x.id === p.probe.line_id)
  return l ? l.name : '未绑定'
}
const health = (p) => p.status && (p.status.cpu_pct !== undefined || p.status.mem_pct !== undefined)
const clockOff = (p) => p.status && Math.abs(p.status.clock_offset_ms || 0) > 30000
function toggle(id) { expanded[id] = !expanded[id] }

// 资产明细（key → 展示名）
const assetLabels = {
  cpu_model: 'CPU', cpu_cores: '核数', mem_total_mb: '内存MB', disk_total_gb: '磁盘GB', disk_used_gb: '已用GB',
  kernel: '内核', distro: '系统', virt: '虚拟化', iface: '网卡', mac: 'MAC', mtu: 'MTU',
}
function assetItems(p) {
  const si = p.probe.sysinfo || {}
  return Object.entries(si).map(([k, v]) => [v, assetLabels[k] || k]).filter(x => x[0] !== '' && x[0] !== undefined)
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
