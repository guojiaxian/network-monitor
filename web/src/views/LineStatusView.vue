<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <h4 class="mb-0">线路状态</h4>
      <span class="text-secondary small ms-3">每条线路的关键运行信息（详情图表请点线路名）· 每 15 秒自动刷新</span>
      <button class="btn btn-sm btn-outline-primary ms-2" :disabled="refreshing" @click="refreshNow">
        {{ refreshing ? '刷新中…' : '🔄 立即刷新' }}
      </button>
      <span v-if="refreshMsg" class="text-success small ms-2">{{ refreshMsg }}</span>
      <span v-if="err" class="text-danger small ms-3">{{ err }}</span>
    </div>

    <div class="row g-3">
      <div class="col-md-6 col-xxl-4" v-for="l in lines" :key="l.id">
        <div class="nw-card h-100 d-flex flex-column">
          <div class="d-flex align-items-center mb-1">
            <router-link :to="'/lines/' + l.id" class="fw-bold text-decoration-none fs-6">{{ l.name }}</router-link>
            <StatusBadge :status="l.status" class="ms-2" />
            <span class="badge bg-light text-dark border ms-auto">{{ l.floor_name || '未关联楼层' }}</span>
          </div>
          <div class="small text-secondary mb-2">
            出口 <code>{{ l.public_ip || '未探得' }}</code>
            <span v-if="ispOf(l)"> · {{ ispOf(l) }}</span>
            <span v-if="geoOf(l)"> · {{ geoOf(l) }}</span>
          </div>
          <div class="mb-2"><HeartbeatBar :beats="recentOf(l)" /></div>
          <div class="row g-1 small mb-2">
            <div class="col-6">时延：<b>{{ fmtMs(rttOf(l)) }}</b></div>
            <div class="col-6">丢包：<b>{{ lossOf(l) }}%</b></div>
            <div class="col-6">TCP：{{ tcpOf(l) ? '✓' : '✗' }}</div>
            <div class="col-6">HTTP：{{ httpOf(l) ? '✓' : '✗' }}</div>
          </div>
          <div class="small mb-1" v-if="l.contract_down_mbps > 0 || speedOf(l)">
            测速 ↓ <b>{{ speedOf(l) ? speedOf(l).toFixed(1) : '—' }}</b> Mbps
            <span class="text-secondary" v-if="l.contract_down_mbps > 0">/ 契约 {{ l.contract_down_mbps }}M</span>
            <span class="text-secondary ms-1" v-if="speedAtOf(l)">（{{ speedAtOf(l) }}）</span>
          </div>
          <div class="progress mb-1" v-if="l.contract_down_mbps > 0 && speedOf(l)" style="height:8px">
            <div class="progress-bar" :class="pctOf(l) >= 85 ? 'bg-success' : (pctOf(l) >= 70 ? 'bg-warning' : 'bg-danger')"
              :style="{ width: Math.min(100, pctOf(l)) + '%' }"></div>
          </div>
          <div class="small text-secondary mb-2" v-if="l.contract_down_mbps > 0 && speedOf(l)">
            契约达成 {{ pctOf(l) }}%
          </div>
          <div class="mt-auto d-flex align-items-center small">
            <span class="text-secondary">探针：{{ probesOf(l) || '未绑定' }}</span>
            <router-link :to="'/lines/' + l.id" class="ms-auto text-decoration-none">详情图表 →</router-link>
          </div>
        </div>
      </div>
      <div v-if="!lines.length" class="col-12">
        <div class="nw-card text-secondary py-4 text-center">暂无线路。到「配置管理 → 线路配置」新增，或开通探针时自动创建。</div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import HeartbeatBar from '../components/HeartbeatBar.vue'
import { fmtTime } from '../utils'

// 线路状态：把线路详情页的关键数字（出口/契约/测速/时延丢包/心跳）提升到列表级。
// 数据全部来自 overview 单次调用；多探针线路按"交叉验证"语义聚合（全成功=绿）。
const lines = ref([]), err = ref('')
const refreshing = ref(false), refreshMsg = ref('')
let timer = null

function statusesOf(l) { return l.statuses || [] }
function rttOf(l) { return Math.max(0, ...statusesOf(l).map(s => s.rtt_ms || 0)) }
function lossOf(l) { return Math.max(0, ...statusesOf(l).map(s => s.loss_pct || 0)) }
function tcpOf(l) { return statusesOf(l).length > 0 && statusesOf(l).every(s => s.tcp_ok) }
function httpOf(l) { return statusesOf(l).length > 0 && statusesOf(l).every(s => s.http_ok) }
function recentOf(l) {
  // 逐位与：所有探针都成功才算成功（交叉验证语义）
  const ss = statusesOf(l).filter(s => (s.recent || []).length)
  if (!ss.length) return []
  const n = Math.min(...ss.map(s => s.recent.length))
  const out = []
  for (let i = 0; i < n; i++) out.push(ss.every(s => s.recent[s.recent.length - n + i]))
  return out
}
function speedOf(l) {
  const withSpeed = statusesOf(l).filter(s => s.speed_down_mbps > 0)
  if (!withSpeed.length) return 0
  // 取最近一次测速值
  withSpeed.sort((a, b) => (b.speed_at || '').localeCompare(a.speed_at || ''))
  return withSpeed[0].speed_down_mbps
}
function speedAtOf(l) {
  const withSpeed = statusesOf(l).filter(s => s.speed_at)
  if (!withSpeed.length) return ''
  withSpeed.sort((a, b) => (b.speed_at || '').localeCompare(a.speed_at || ''))
  return fmtTime(withSpeed[0].speed_at)
}
function pctOf(l) { return l.contract_down_mbps > 0 ? Math.round(speedOf(l) * 100 / l.contract_down_mbps) : 0 }
function ispOf(l) {
  const s = statusesOf(l).find(x => x.egress_isp)
  return (s && s.egress_isp) || l.isp || ''
}
function geoOf(l) {
  const s = statusesOf(l).find(x => x.egress_geo)
  return (s && s.egress_geo) || l.geo || ''
}
let probeNames = {}
function probesOf(l) {
  return (l.probe_ids || []).map(id => probeNames[id] || id.slice(0, 8)).join('、')
}
function fmtMs(v) { return v ? v.toFixed(1) + ' ms' : '—' }

async function load() {
  try {
    const [ov, ps] = await Promise.all([api.get('/api/admin/overview'), api.get('/api/admin/probes')])
    lines.value = ov.lines || []
    probeNames = {}
    for (const p of ps) probeNames[p.probe.id] = p.probe.name || p.probe.hostname
    err.value = ''
  } catch (e) { err.value = e.message }
}

async function refreshNow() {
  refreshing.value = true; refreshMsg.value = ''
  try {
    await api.post('/api/admin/probes/refresh', {})
    refreshMsg.value = '已下发：探针下一心跳（≤15s）重探出口并回报'
    clearInterval(timer)
    let n = 10
    timer = setInterval(() => { load(); if (--n <= 0) { clearInterval(timer); timer = setInterval(load, 15000) } }, 3000)
    setTimeout(() => { refreshing.value = false }, 8000)
  } catch (e) { err.value = e.message; refreshing.value = false }
}

onMounted(() => { load(); timer = setInterval(load, 15000) })
onUnmounted(() => clearInterval(timer))
</script>
