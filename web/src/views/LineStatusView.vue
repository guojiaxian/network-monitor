<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <div>
        <h4 class="mb-0">📈 线路状态</h4>
        <div class="text-secondary small mt-1">每条线路的关键运行信息（详情图表请点线路名）· 每 15 秒自动刷新<span v-if="refreshMsg" class="text-success ms-2">{{ refreshMsg }}</span><span v-if="err" class="text-danger ms-2">{{ err }}</span></div>
      </div>
      <button class="nw-btn nw-btn-accent nw-btn-spin ms-auto" :disabled="refreshing" @click="refreshNow"
        title="命令全部探针立即重探出口 IP 并排程一轮测速（下一心跳 ≤15s 回报，约 1 分钟内开测）">
        <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M11.534 7h3.932a.25.25 0 0 1 .192.41l-1.966 2.36a.25.25 0 0 1-.384 0l-1.966-2.36a.25.25 0 0 1 .192-.41zm-11 2h3.932a.25.25 0 0 0 .192-.41L2.692 6.23a.25.25 0 0 0-.384 0L.342 8.59A.25.25 0 0 0 .534 9z"/><path fill-rule="evenodd" d="M8 3c-1.552 0-2.94.707-3.857 1.818a.5.5 0 1 1-.771-.636A6.002 6.002 0 0 1 13.917 7H12.9A5.002 5.002 0 0 0 8 3zM3.1 9a5.002 5.002 0 0 0 8.757 2.182.5.5 0 1 1 .771.636A6.002 6.002 0 0 1 2.083 9H3.1z"/></svg>{{ refreshing ? '下发中…' : '立即探测' }}
      </button>
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
          <div class="mb-2"><HeartbeatGrid :cells="hbOf(l)" /></div>
          <div class="row g-1 small mb-2">
            <div class="col-6">时延：<b>{{ fmtMs(rttOf(l)) }}</b></div>
            <div class="col-6">丢包：<b :class="lossCls(l)">{{ lossOf(l) }}%</b></div>
            <div class="col-6">TCP：{{ tcpOf(l) ? '✓' : '×' }}</div>
            <div class="col-6">HTTP：{{ httpOf(l) ? '✓' : '×' }}</div>
          </div>
          <!-- 在线 IP 设备数：该线路各楼层最近一次 IP 扫描的在线主机数之和（定时扫描每小时刷新） -->
          <div class="small mb-2">
            在线 IP：<b style="color:#0a8f4d">{{ l.online_sum ?? '—' }}</b>
            <template v-if="l.online_sum !== null && l.online_sum !== undefined">
              <span class="text-secondary"> 台（共扫 {{ l.scan_sum }} 个地址）</span>
              <span class="text-secondary ms-1" v-if="l.scanned_at">· {{ scanAgo(l.scanned_at) }}</span>
            </template>
            <span class="text-secondary ms-1" v-else>（该线路楼层尚未扫描）</span>
          </div>
          <div class="small mb-2">
            测速 ↓ <b>{{ speedOf(l) ? speedOf(l).toFixed(1) : '—' }}</b> Mbps
            <span class="text-secondary ms-1" v-if="speedAtOf(l)">（{{ speedAtOf(l) }}）</span>
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
import HeartbeatGrid from '../components/HeartbeatGrid.vue'
import { fmtTime, minuteCells } from '../utils'

// 线路状态：把线路详情页的关键数字（出口/契约/测速/时延丢包/心跳）提升到列表级。
// 数据全部来自 overview 单次调用；多探针线路按"交叉验证"语义聚合（全成功=绿）。
const lines = ref([]), err = ref('')
const lossThresh = ref(10) // 降级丢包阈值（接口下发，与 P2 告警同口径）
const refreshing = ref(false), refreshMsg = ref('')
let timer = null

function statusesOf(l) { return l.statuses || [] }
function rttOf(l) { return Math.max(0, ...statusesOf(l).map(s => s.rtt_ms || 0)) }
function lossOf(l) { return Number(Math.max(0, ...statusesOf(l).map(s => s.loss_pct || 0)).toFixed(1)) }
function lossCls(l) { return lossOf(l) > lossThresh.value ? 'text-warning' : '' }
function tcpOf(l) { return statusesOf(l).length > 0 && statusesOf(l).every(s => s.tcp_ok) }
function httpOf(l) { return statusesOf(l).length > 0 && statusesOf(l).every(s => s.http_ok) }
function hbOf(l) {
  // 心跳格：后端已按探针并集汇总到线路（1 格 = 1 分钟，全部样本失败才算失败）
  return minuteCells(l.minutes, lossThresh.value)
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
// 扫描时间的人性化相对时间（"12 分钟前"），超过 2 小时改为绝对时间
function scanAgo(t) {
  const diff = Date.now() - new Date(t).getTime()
  const min = Math.floor(diff / 60000)
  if (min < 1) return '刚刚扫描'
  if (min < 60) return `${min} 分钟前扫描`
  if (min < 120) return `${Math.floor(min / 60)} 小时前扫描`
  return fmtTime(t) + ' 扫描'
}
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
    if (ov.loss_thresh > 0) lossThresh.value = ov.loss_thresh
    probeNames = {}
    for (const p of ps) probeNames[p.probe.id] = p.probe.name || p.probe.hostname
    err.value = ''
  } catch (e) { err.value = e.message }
}

async function refreshNow() {
  refreshing.value = true; refreshMsg.value = ''
  try {
    await api.post('/api/admin/probes/refresh', {})
    refreshMsg.value = '已下发：探针下一心跳（≤15s）重探出口并回报，同时已排程一轮测速（约 1 分钟内开始），页面加速刷新中'
    clearInterval(timer)
    let n = 10
    timer = setInterval(() => { load(); if (--n <= 0) { clearInterval(timer); timer = setInterval(load, 15000) } }, 3000)
    setTimeout(() => { refreshing.value = false }, 8000)
  } catch (e) { err.value = e.message; refreshing.value = false }
}

onMounted(() => { load(); timer = setInterval(load, 15000) })
onUnmounted(() => clearInterval(timer))
</script>
