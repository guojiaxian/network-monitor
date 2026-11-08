<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <button type="button" class="btn btn-sm btn-outline-secondary me-2" @click="goBack(router, '/floor-status')">← 返回</button>
      <h4 class="mb-0">🏢 {{ floor.name || '楼层详情' }}</h4>
      <StatusBadge :status="floor.status || 'unknown'" class="ms-3" />
      <code class="ms-3">{{ floor.cidr || '未配置网段' }}</code>
    </div>

    <!-- KPI 行：与线路详情页同款五卡布局 -->
    <div class="row row-cols-2 row-cols-md-3 row-cols-xl-5 g-3 mb-3" v-if="floor.id">
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">关联线路</div>
        <template v-if="(floor.line_stats || []).length">
          <div v-for="ln in floor.line_stats" :key="ln.id">
            <router-link :to="'/lines/' + ln.id" class="fw-bold text-decoration-none">{{ ln.name }}</router-link>
            <span class="ms-1 small" :class="lineStatusColor(ln.status)">{{ lineStatusText(ln.status) }}</span>
          </div>
        </template>
        <b v-else>未关联线路</b>
      </div></div>
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">在线 IP 设备</div>
        <b style="color:#0a8f4d">{{ floor.online ?? '—' }}</b>
        <span class="text-secondary small" v-if="floor.online !== null && floor.online !== undefined"> / 共扫 {{ floor.total }} 个地址</span>
        <div class="text-secondary small" v-if="floor.scanned_at">{{ fmtTime(floor.scanned_at) }} 扫描</div>
        <div class="text-secondary small" v-else>本楼层尚未扫描</div>
      </div></div>
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">出口公网 IP</div>
        <b>{{ (floor.line_stats || []).map(l => l.public_ip || '未探得').join('、') || '—' }}</b>
      </div></div>
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">近 24h 连通率</div>
        <b :style="okRate !== null && okRate < 99 ? 'color:#b45309' : 'color:#0a8f4d'">{{ okRate === null ? '—' : okRate + '%' }}</b>
        <div class="text-secondary small" v-if="okRate === null">无出口线路或暂无数据</div>
      </div></div>
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">绑定探针</div>
        <b :title="(floor.probe_names || []).join('、')">{{ (floor.probe_names || []).length || '无' }}</b> 台
      </div></div>
    </div>

    <div class="text-secondary py-4 text-center nw-card mb-3" v-if="!floor.id">楼层不存在或已被删除。</div>

    <!-- 联通率详情：心跳与楼层状态卡同源；时延/连通率来自 VictoriaMetrics -->
    <div class="nw-card mb-3">
      <div class="d-flex align-items-center">
        <h6 class="mb-0">连通性心跳（关联线路聚合）</h6>
        <span class="text-secondary small ms-auto">每格 = 1 分钟探测汇总 · 右端最新，放不下最左移出 · 与楼层状态卡同源（中心逐分钟统计，最近 3 小时）</span>
      </div>
      <div class="mt-2" v-if="hbCells.length"><HeartbeatGrid :cells="hbCells" legend axis /></div>
      <div class="text-secondary small py-4 text-center" v-else>暂无可用的连通性数据。</div>
    </div>

    <div class="nw-card mb-3" v-if="hasLineData">
      <h6>近 24 小时时延（各探测目标）</h6>
      <div style="height: 260px" class="mt-2"><canvas id="rttChart"></canvas></div>
    </div>

    <!-- 在线 IP 详情：本楼层 IP 扫描结果 -->
    <div class="nw-card mb-3">
      <div class="d-flex align-items-center flex-wrap gap-2">
        <h6 class="mb-0">近 7 天在线 IP 变化（本楼层扫描）</h6>
        <router-link :to="'/scan?floor=' + floorId" class="btn btn-sm btn-outline-primary ms-auto">前往 IP 扫描 →</router-link>
      </div>
      <div style="height: 200px" class="mt-2" v-if="scanChartPoints.length"><canvas id="scanChart"></canvas></div>
      <div class="text-secondary small py-3 text-center" v-else>近 7 天无扫描记录，点击右上角发起扫描。</div>
    </div>

    <div class="nw-card">
      <div class="d-flex align-items-center flex-wrap gap-2 mb-2">
        <h6 class="mb-0">逐 IP 明细</h6>
        <span class="text-secondary small" v-if="detail">{{ detail.cidr }} · 在线 <b style="color:#0a8f4d">{{ detail.up }}</b> / {{ detail.total }} · {{ fmtTime(detail.created_at) }} 扫描</span>
        <span class="text-secondary small ms-auto" v-if="detail">展示最近一次扫描结果</span>
      </div>
      <div v-if="detailLoading" class="text-secondary small py-2">加载明细中…</div>
      <template v-else-if="detail">
        <div class="ip-grid" v-if="(detail.results || []).length">
          <div v-for="res in detail.results" :key="res.ip" class="ip-cell"
               :class="res.online ? 'on' : 'off'"
               :title="res.ip + (res.online ? ' · 在线 · ' + res.rtt_ms.toFixed(1) + 'ms' : ' · 离线')">
            {{ cellLabel(res.ip, detail.cidr) }}
          </div>
        </div>
        <div class="text-secondary small" v-else>无结果数据</div>
      </template>
      <div class="text-secondary small py-2" v-else>暂无已完成的扫描记录。</div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import dayjs from 'dayjs'
import Chart from 'chart.js/auto'
import { api } from '../api'
import { fmtTime, goBack, minuteCells, applyStatusToCurrent } from '../utils'
import StatusBadge from '../components/StatusBadge.vue'
import HeartbeatGrid from '../components/HeartbeatGrid.vue'

const router = useRouter()

// 楼层详情：联通率详情（关联线路探针的连通率/时延曲线，VM 按 line 标签过滤）
// + 在线 IP 详情（本楼层扫描记录趋势 + 逐 IP 格子墙，数据全部来自既有接口，无后端改动）。
const route = useRoute()
const floorId = Number(route.params.id)
const floor = ref({})
const okRate = ref(null)        // 近 24h 平均连通率（各线路全部目标平均）
const hasLineData = ref(false)  // 是否查到出口线路连通性序列
const hbCells = ref([])          // 连通性心跳（1 格 = 1 分钟，最近 3 小时；与楼层状态卡同一份后端分钟统计）
const engineCells = ref([])      // 后端最近 3 小时分钟统计（floor-status?minutes=180）
const lossThresh = ref(10)       // 降级丢包阈值（接口下发，与 P2 告警同口径）
const runs = ref([])            // 本楼层近 7 天扫描记录（不含明细，供在线趋势图与最新明细取数）
const detail = ref(null)        // 当前展示的逐 IP 明细
const detailLoading = ref(false)
const scanChartPoints = ref([])
let rttChart = null, scanChart = null
let liveTimer = null // 状态/心跳 15s 实时刷新（图表不重绘）

const palette = ['#2f81f7', '#05c96b', '#ffc107', '#dc3545', '#6f42c1', '#20c997', '#fd7e14', '#adb5bd']

function lineStatusText(s) {
  return { online: '正常', down: '中断', floor_down: '中断', degraded: '降质', speed_low: '带宽不达标', conflict: '数据冲突', blind: '盲区', unknown: '未知' }[s] || s
}
function lineStatusColor(s) {
  return { online: 'text-success', down: 'text-danger', floor_down: 'text-danger', degraded: 'text-warning', speed_low: 'text-warning', conflict: 'text-danger', blind: 'text-secondary' }[s] || 'text-secondary'
}
// 与扫描页同款：格子标签在比 /24 宽的网段显示末两字节，否则只显示末字节
function cellLabel(ip, cidr) {
  const parts = ip.split('.')
  const mask = parseInt((cidr || '').split('/')[1], 10)
  return mask && mask < 24 ? `${parts[2]}.${parts[3]}` : parts[3]
}
// 线路名拼 VM 正则前转义元字符（线路名可含括号等合法字符）
function linePattern(names) {
  return names.map(n => n.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')
}

async function fetchSeries(expr, hours, step, range = null) {
  const start = range ? range.start : dayjs().subtract(hours, 'hour').unix()
  const end = range ? range.end : dayjs().unix()
  const d = await api.get(`/api/admin/metrics/query_range?query=${encodeURIComponent(expr)}&start=${start}&end=${end}&step=${step}`)
  return (d.data && d.data.result) || []
}

function render(id, cfg) {
  const el = document.getElementById(id)
  if (!el) return
  return new Chart(el, {
    type: 'line',
    data: { labels: cfg.labels, datasets: cfg.datasets },
    options: {
      responsive: true, maintainAspectRatio: false, animation: false,
      interaction: { mode: 'index', intersect: false },
      scales: { y: { title: { display: true, text: cfg.yTitle } } },
      plugins: { legend: { labels: { boxWidth: 12, font: { size: 11 } } } },
    },
  })
}

function mk(series, yTitle, labelOf) {
  const labels = series[0] ? series[0].values.map(v => dayjs(v[0] * 1000).format('HH:mm')) : []
  const datasets = series.map((s, i) => ({
    label: labelOf ? labelOf(s.metric) : (s.metric.target || s.metric.probe || s.metric.line),
    data: s.values.map(v => v[1] == null ? null : Number(v[1])),
    borderColor: palette[i % palette.length],
    backgroundColor: 'transparent', borderWidth: 1.6, pointRadius: 0, tension: .25, spanGaps: true,
  }))
  return { labels, datasets, yTitle }
}

async function fetchStatus() {
  const d = await api.get('/api/admin/floor-status?minutes=180')
  floor.value = (d.floors || []).find(f => f.id === floorId) || {}
  if (d.loss_thresh > 0) lossThresh.value = d.loss_thresh
  // 详情页整段 3 小时心跳都用后端同一份分钟统计（不再是引擎 60min + VM 历史两源合并），
  // 与楼层状态卡/告警同一数据、同一判定；当前分钟按状态兜底。
  engineCells.value = minuteCells(floor.value.minutes || [], lossThresh.value)
  hbCells.value = applyStatusToCurrent(engineCells.value, floor.value.status)
}

async function load() {
  try { await fetchStatus() } catch (e) { floor.value = {} }
  if (floor.value.id) { await Promise.all([drawConnectivity(), loadRuns()]) }
}

// 活动告警期间详情页必须跟楼层状态卡同步：15s 刷新状态徽章 + 最近 3 小时心跳（不重绘历史图表）
async function refreshLive() {
  try { await fetchStatus() } catch {}
}

// 联通率详情：关联线路（可多线）的连通性心跳/时延曲线，24h 平均连通率进 KPI
async function drawConnectivity() {
  const names = (floor.value.line_stats || []).map(l => l.name)
  if (!names.length) return
  const pat = linePattern(names)
  hasLineData.value = true
  // v-if 刚放行时 canvas 尚未入 DOM（Vue 异步渲染），等一拍再取元素画图
  await nextTick()
  // 24h 平均连通率 KPI：独立低分辨率查询（nw_ping_ok 取值 0/1 → ×100 得百分比）
  const day = await fetchSeries(`avg_over_time(nw_ping_ok{line=~"${pat}"}[30m])`, 24, 1800).catch(() => [])
  let sum = 0, n = 0
  for (const s of day) for (const v of s.values) { const x = Number(v[1]); if (!isNaN(x)) { sum += x; n++ } }
  if (n) okRate.value = Math.round(sum / n * 100 * 10) / 10
  const rttSeries = await fetchSeries(`avg by (line, target) (nw_ping_rtt_ms{line=~"${pat}"})`, 24, 300).catch(() => [])
  if (rttSeries.length) rttChart = render('rttChart', mk(rttSeries, 'ms', m => `${m.line}·${m.target}`))
}

// 在线 IP 详情：本楼层近 7 天扫描记录 + 最新一次逐 IP 格子墙
async function loadRuns() {
  try {
    const res = await api.get(`/api/admin/scan/runs?floor_id=${floorId}&days=7&limit=20`)
    runs.value = res.rows || []
  } catch { runs.value = [] }
  const done = runs.value.filter(r => r.status === 'done')
  if (done.length) await selectRun(done[0]) // 列表按时间倒序，首条即最新；明细区只展示这一次结果
  // 在线数变化曲线：时间升序
  const pts = done.slice().sort((a, b) => new Date(a.created_at) - new Date(b.created_at))
  scanChartPoints.value = pts
  if (pts.length) {
    await nextTick() // 同上：canvas 由 v-if 控制，等入 DOM 再画
    scanChart = render('scanChart', {
      labels: pts.map(r => dayjs(r.created_at).format('MM-DD HH:mm')),
      datasets: [{
        label: '在线 IP 数',
        data: pts.map(r => r.up),
        borderColor: '#0a8f4d', backgroundColor: 'rgba(10,143,77,.08)',
        borderWidth: 1.6, pointRadius: 2, tension: .25, spanGaps: true, fill: true,
      }],
      yTitle: '台',
    })
  }
}

async function selectRun(r) {
  detailLoading.value = true
  try { detail.value = await api.get(`/api/admin/scan/runs/${r.id}`) }
  catch { detail.value = null }
  finally { detailLoading.value = false }
}

onMounted(async () => {
  await load()
  liveTimer = setInterval(refreshLive, 15000)
})
onUnmounted(() => {
  if (liveTimer) clearInterval(liveTimer)
  for (const c of [rttChart, scanChart]) c && c.destroy()
})
</script>
