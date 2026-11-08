<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <div>
        <h4 class="mb-0">🏢 楼层状态</h4>
        <div class="text-secondary small mt-1">以楼层为单位汇总关联线路的探针状态与最近一次 IP 扫描 · 每 15 秒自动刷新<span v-if="refreshMsg" class="text-success ms-2">{{ refreshMsg }}</span><span v-if="err" class="text-danger ms-2">{{ err }}</span></div>
      </div>
      <button class="nw-btn nw-btn-accent nw-btn-spin ms-auto" :disabled="refreshing" @click="refreshNow"
        title="命令全部探针立即重探出口 IP 并排程一轮测速（下一心跳 ≤15s 回报，约 1 分钟内开测）">
        <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M11.534 7h3.932a.25.25 0 0 1 .192.41l-1.966 2.36a.25.25 0 0 1-.384 0l-1.966-2.36a.25.25 0 0 1 .192-.41zm-11 2h3.932a.25.25 0 0 0 .192-.41L2.692 6.23a.25.25 0 0 0-.384 0L.342 8.59A.25.25 0 0 0 .534 9z"/><path fill-rule="evenodd" d="M8 3c-1.552 0-2.94.707-3.857 1.818a.5.5 0 1 1-.771-.636A6.002 6.002 0 0 1 13.917 7H12.9A5.002 5.002 0 0 0 8 3zM3.1 9a5.002 5.002 0 0 0 8.757 2.182.5.5 0 1 1 .771.636A6.002 6.002 0 0 1 2.083 9H3.1z"/></svg>{{ refreshing ? '下发中…' : '立即探测' }}
      </button>
    </div>

    <div class="row g-3">
      <div class="col-md-6 col-xxl-4" v-for="f in floors" :key="f.id">
        <div class="nw-card h-100 d-flex flex-column">
          <div class="d-flex align-items-center mb-1">
            <router-link :to="'/floors/' + f.id" class="fw-bold text-decoration-none fs-6">{{ f.name }}</router-link>
            <StatusBadge :status="f.status" class="ms-2" />
            <span class="badge bg-light text-dark border ms-auto">{{ f.cidr || '未配置网段' }}</span>
          </div>
          <!-- 关联线路（楼层网络状况即其出口线路状况；多线路楼层逐行列出各自状态） -->
          <div class="small text-secondary mb-2" v-if="(f.line_stats || []).length">
            <div v-for="ln in f.line_stats" :key="ln.id">
              线路 <router-link :to="'/lines/' + ln.id" class="text-decoration-none">{{ ln.name }}</router-link>
              <StatusBadge v-if="f.line_stats.length > 1" :status="ln.status" class="ms-1" />
              <span class="ms-1">·</span> 出口 <code>{{ ln.public_ip || '未探得' }}</code>
            </div>
          </div>
          <div class="small text-secondary mb-2" v-else>未关联线路（该楼层无出口线路，仅网段信息）</div>
          <div class="mb-2"><HeartbeatGrid :cells="hbOf(f)" /></div>
          <div class="row g-1 small mb-2">
            <div class="col-6">时延：<b>{{ fmtMs(f.rtt_ms) }}</b></div>
            <div class="col-6">丢包：<b :class="lossCls(f)">{{ lossOf(f) }}%</b></div>
            <div class="col-6">TCP：{{ f.tcp_ok ? '✓' : '×' }}</div>
            <div class="col-6">HTTP：{{ f.http_ok ? '✓' : '×' }}</div>
          </div>
          <!-- 在线 IP 设备数：本楼层最近一次 IP 扫描的在线主机数（定时扫描按计划刷新） -->
          <div class="small mb-2" v-if="f.cidr">
            在线 IP：<b style="color:#0a8f4d">{{ f.online ?? '—' }}</b>
            <template v-if="f.online !== null && f.online !== undefined">
              <span class="text-secondary"> 台（共扫 {{ f.total }} 个地址）</span>
              <span class="text-secondary ms-1" v-if="f.scanned_at">· {{ scanAgo(f.scanned_at) }}</span>
            </template>
            <span class="text-secondary ms-1" v-else>（本楼层尚未扫描）</span>
          </div>
          <div class="mt-auto d-flex align-items-center small">
            <span class="text-secondary">探针：{{ (f.probe_names || []).join('、') || '未绑定' }}</span>
            <router-link :to="'/floors/' + f.id" class="ms-auto text-decoration-none">详情图表 →</router-link>
          </div>
        </div>
      </div>
      <div v-if="!floors.length" class="col-12">
        <div class="nw-card text-secondary py-4 text-center">暂无楼层。到「配置管理 → 楼层配置」新增。</div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import HeartbeatGrid from '../components/HeartbeatGrid.vue'
import { fmtTime, minuteCells, applyStatusToCurrent } from '../utils'

// 心跳格：后端已按关联线路并集汇总到楼层（1 格 = 1 分钟）
function hbOf(f) {
  // 当前分钟按楼层状态（最差线路）兜底，避免"已中断/降质"时当前格仍为绿色。
  return applyStatusToCurrent(minuteCells(f.minutes, lossThresh.value), f.status)
}

// 楼层状态：与线路状态同款卡片，按楼层聚合——状态取关联线路最差（后端 /api/admin/floor-status），
// 在线 IP 为本楼层最近一次扫描结果（区别于线路页的跨楼层求和）。
const floors = ref([]), err = ref('')
const lossThresh = ref(10) // 降级丢包阈值（接口下发，与 P2 告警同口径）
const refreshing = ref(false), refreshMsg = ref('')
let timer = null

// 扫描时间的人性化相对时间（"12 分钟前"），超过 2 小时改为绝对时间
function scanAgo(t) {
  const diff = Date.now() - new Date(t).getTime()
  const min = Math.floor(diff / 60000)
  if (min < 1) return '刚刚扫描'
  if (min < 60) return `${min} 分钟前扫描`
  if (min < 120) return `${Math.floor(min / 60)} 小时前扫描`
  return fmtTime(t) + ' 扫描'
}
function fmtMs(v) { return v ? v.toFixed(1) + ' ms' : '—' }
function lossOf(f) { return Number(Number(f.loss_pct || 0).toFixed(1)) }
function lossCls(f) { return lossOf(f) > lossThresh.value ? 'text-warning' : '' }

async function load() {
  try {
    const d = await api.get('/api/admin/floor-status')
    floors.value = d.floors || []
    if (d.loss_thresh > 0) lossThresh.value = d.loss_thresh
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
