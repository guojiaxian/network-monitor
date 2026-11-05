<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <h4 class="mb-0">总览</h4>
      <span class="text-secondary small ms-3">每 10 秒自动刷新</span>
      <span v-if="err" class="text-danger small ms-3">{{ err }}</span>
    </div>

    <div class="row g-3 mb-3">
      <div class="col-md-3" v-for="s in statCards" :key="s.label">
        <div class="nw-card h-100">
          <div class="text-secondary small">{{ s.label }}</div>
          <div class="stat-num" :style="{ color: s.color }">{{ s.value }}</div>
        </div>
      </div>
    </div>

    <div v-if="unbound.length" class="alert alert-warning py-2">
      有 {{ unbound.length }} 台探针未绑定线路：
      <code v-for="p in unbound" :key="p.id" class="me-2" :title="p.id">{{ p.name || p.hostname || p.id.slice(0,8) }}</code>
      → 前往 <router-link to="/probes/manage">探针管理</router-link> 完成绑定
    </div>

    <div v-if="extProbes.length" class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">🌐 外网 → 内网 · 官网可达性</h6>
        <StatusBadge :status="extStatus" class="ms-3" />
        <span class="text-secondary small ms-auto">来自阿里云等外网服务器的探测（ping + HTTPS）</span>
      </div>
      <div class="row g-2">
        <div class="col-md-4" v-for="p in extProbes" :key="p.id">
          <div class="border rounded p-2 d-flex align-items-center gap-2">
            <StatusBadge :status="p.status.status || 'unknown'" />
            <div class="small">
              <b>{{ p.name || p.hostname || p.id.slice(0,8) }}</b><br />
              <span class="text-secondary">{{ extDesc(p) }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">🏢 内网 → 内网 · 内网服务</h6>
        <StatusBadge :status="monitorsUp ? 'online' : (monitors.length ? 'down' : 'unknown')" class="ms-3" />
        <span class="text-secondary small ms-auto">中心服务器内置探测（TCP / HTTP / ICMP）</span>
      </div>
      <div v-if="monitors.length" class="row g-2">
        <div class="col-md-4" v-for="mst in monitors" :key="mst.name">
          <div class="border rounded p-2 d-flex align-items-center gap-2">
            <StatusBadge :status="!mst.known ? 'unknown' : (mst.up ? 'online' : 'down')" />
            <div class="small">
              <b>{{ mst.name }}</b><br />
              <span class="text-secondary">{{ monDesc(mst) }}</span>
            </div>
          </div>
        </div>
      </div>
      <div v-else class="text-secondary small py-3 text-center">
        暂无内网监控目标。到「系统设置 → 探测配置 → 内网服务监控」添加（如 OA 系统、核心服务器、交换机管理地址）。
      </div>
    </div>

    <div class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">🛰️ 行业网监测</h6>
        <StatusBadge :status="indUp ? 'online' : (indMons.length ? 'down' : 'unknown')" class="ms-3" />
        <span class="text-secondary small ms-auto">中心服务器对行业网 IP 仅 ICMP ping（不做其他探测）</span>
      </div>
      <div v-if="indMons.length" class="row g-2">
        <div class="col-md-4" v-for="m in indMons" :key="m.name">
          <div class="border rounded p-2 d-flex align-items-center gap-2">
            <StatusBadge :status="!m.known ? 'unknown' : (m.up ? 'online' : 'down')" />
            <div class="small">
              <b>{{ m.name }}</b><br />
              <span class="text-secondary">{{ indDesc(m) }}</span>
            </div>
          </div>
        </div>
      </div>
      <div v-else class="text-secondary small py-3 text-center">
        暂无行业网监测目标。到「系统设置 → 探测配置 → 行业网监测」添加（仅 ICMP，如 10.5.x.x 的核心设备地址）。
      </div>
    </div>

    <h5 class="mb-3 mt-2">🔀 内网 → 外网 · 宽带线路</h5>
    <div v-if="!lines.length && !err" class="nw-card text-center py-5 text-secondary">
      尚未录入线路。请先到 <router-link to="/lines">线路配置</router-link> 完成配置，再在
      <router-link to="/probes/manage">探针管理</router-link> 中绑定探针。
    </div>

    <div class="row g-3">
      <div class="col-md-6 col-xl-4" v-for="l in lines" :key="l.id">
        <div class="nw-card line-card h-100" :class="cls(l.status)" @click="$router.push('/lines/' + l.id)">
          <div class="d-flex align-items-center gap-2 mb-2">
            <StatusBadge :status="l.status" class="ms-auto order-2" />
            <div class="fw-bold order-1">{{ l.name }}</div>
          </div>
          <div class="text-secondary small mb-2">
            {{ l.floor_name || '未关联楼层' }} · {{ l.cidr || '—' }}<br />
            出口 IP：{{ l.public_ip || '未填写' }} · 契约 {{ l.contract_down_mbps || '?' }}M / {{ l.contract_up_mbps || '?' }}M
          </div>
          <HeartbeatBar v-if="recentOf(l).length" :beats="recentOf(l)" class="mb-2" />
          <div class="d-flex justify-content-between small" v-if="probeOf(l)">
            <span>时延 <b>{{ rttOf(l) }}</b> ms</span>
            <span>丢包 <b>{{ lossOf(l) }}</b> %</span>
            <span>测速 <b>{{ speedOf(l) }}</b></span>
            <span>TCP <b :style="{ color: probeOf(l).tcp_ok ? '#05c96b' : '#dc3545' }">{{ probeOf(l).tcp_ok ? '通' : '断' }}</b></span>
          </div>
          <div v-else class="small text-secondary">未绑定探针</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import HeartbeatBar from '../components/HeartbeatBar.vue'

const lines = ref([]), unbound = ref([]), extProbes = ref([]), extStatus = ref('unknown'), monitors = ref([]), indMons = ref([]), err = ref('')
let timer = null

const monitorsUp = computed(() => monitors.value.length > 0 && monitors.value.every(m => m.up))
const indUp = computed(() => indMons.value.length > 0 && indMons.value.every(m => m.up))

function indDesc(m) {
  if (!m.known) return '等待首次探测…'
  const parts = []
  if (m.rtt_ms) parts.push(m.rtt_ms.toFixed(1) + 'ms')
  parts.push(m.target)
  return parts.join(' · ')
}

function monDesc(m) {
  if (!m.known) return '等待首次探测…'
  const parts = []
  if (m.type === 'http' && m.code) parts.push('HTTP ' + m.code)
  if (m.rtt_ms) parts.push(m.rtt_ms.toFixed(1) + 'ms')
  parts.push(m.target)
  return parts.join(' · ')
}

function extDesc(p) {
  const s = p.status || {}
  const parts = []
  if (s.rtt_ms) parts.push('时延 ' + s.rtt_ms.toFixed(0) + 'ms')
  parts.push('HTTPS ' + (s.http_ok ? '正常' : '失败'))
  return parts.join(' · ')
}

const statCards = computed(() => {
  const c = { online: 0, bad: 0, blind: 0, total: lines.value.length }
  for (const l of lines.value) {
    if (l.status === 'online') c.online++
    else if (l.status === 'blind') c.blind++
    else if (l.status !== 'unknown') c.bad++
  }
  return [
    { label: '线路总数', value: c.total, color: '#1d2635' },
    { label: '正常', value: c.online, color: '#05c96b' },
    { label: '异常（中断/降质）', value: c.bad, color: '#dc3545' },
    { label: '监控盲区', value: c.blind, color: '#6f42c1' },
  ]
})

function probeOf(l) { return (l.statuses && l.statuses[0]) || null }
function recentOf(l) { return (probeOf(l) && probeOf(l).recent) || [] }
function rttOf(l) { const p = probeOf(l); return p && p.rtt_ms ? p.rtt_ms.toFixed(1) : '—' }
function lossOf(l) { const p = probeOf(l); return p && p.loss_pct != null ? p.loss_pct.toFixed(1) : '—' }
function speedOf(l) {
  const p = probeOf(l)
  if (!p || !p.speed_down_mbps) return '—'
  return p.speed_down_mbps >= 100 ? Math.round(p.speed_down_mbps) + 'M' : p.speed_down_mbps.toFixed(1) + 'M'
}
function cls(s) { return 'st-' + (s || 'unknown') }

async function load() {
  try {
    const d = await api.get('/api/admin/overview')
    lines.value = d.lines || []
    unbound.value = d.unbound_probes || []
    extProbes.value = d.external_probes || []
    extStatus.value = d.external_status || 'unknown'
    monitors.value = d.internal_monitors || []
    indMons.value = d.industry_monitors || []
    err.value = ''
  } catch (e) { err.value = e.message }
}

onMounted(() => { load(); timer = setInterval(load, 10000) })
onUnmounted(() => clearInterval(timer))
</script>
