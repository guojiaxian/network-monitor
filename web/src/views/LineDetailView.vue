<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <button type="button" class="btn btn-sm btn-outline-secondary me-2" @click="goBack(router, '/lines')">← 返回</button>
      <h4 class="mb-0">📈 {{ line.name || '线路详情' }}</h4>
      <StatusBadge :status="status" class="ms-3" />
    </div>

    <div class="row row-cols-2 row-cols-md-3 row-cols-xl-5 g-3 mb-3" v-if="line.id">
      <div class="col"><div class="nw-card h-100"><div class="text-secondary small">楼层 / 子网</div><b>{{ line.floor_name || '—' }}</b> <code class="small">{{ line.cidr || '' }}</code></div></div>
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">在线 IP 设备</div>
        <b style="color:#0a8f4d" :title="onlineDetail">{{ line.online_sum ?? '—' }}</b>
        <span class="text-secondary small" v-if="line.online_sum !== null && line.online_sum !== undefined"> / 共扫 {{ line.scan_sum }} 个地址</span>
        <div class="text-secondary small" v-if="line.scanned_at">{{ fmtTime(line.scanned_at) }} 扫描</div>
        <div class="text-secondary small" v-else>尚未扫描</div>
      </div></div>
      <div class="col"><div class="nw-card h-100"><div class="text-secondary small">出口公网 IP</div><b>{{ line.public_ip || '未填写' }}</b></div></div>
      <div class="col"><div class="nw-card h-100"><div class="text-secondary small">契约带宽</div><b>{{ line.contract_down_mbps || '?' }}M / {{ line.contract_up_mbps || '?' }}M</b></div></div>
      <div class="col"><div class="nw-card h-100"><div class="text-secondary small">绑定探针</div><b>{{ probeIds.length || '无' }}</b> 台</div></div>
    </div>

    <div class="nw-card mb-3" v-if="line.id">
      <div class="d-flex align-items-center">
        <h6 class="mb-0">出口 IP 变化记录</h6>
        <span class="text-secondary small ms-auto">绑定探针每 5 分钟重探出口，动态换 IP（如家宽重拨）自动回写本线路并留痕</span>
      </div>
      <table class="table nw-table table-sm align-middle mb-0 mt-2" v-if="ipHistory.length">
        <thead>
          <tr><th style="width:24%">时间</th><th>出口 IP 变化</th><th style="width:16%">来源</th><th style="width:22%">操作者</th></tr>
        </thead>
        <tbody>
          <tr v-for="h in ipHistory" :key="h.id">
            <td class="text-secondary">{{ fmtTime(h.created_at) }}</td>
            <td>
              <template v-if="h.old_ip"><code>{{ h.old_ip }}</code> <span class="text-secondary">→</span> <code>{{ h.new_ip }}</code></template>
              <template v-else><code>{{ h.new_ip }}</code> <span class="text-secondary small">（首次登记）</span></template>
            </td>
            <td><span class="badge" :class="h.source === 'manual' ? 'bg-secondary' : 'bg-success'">{{ h.source === 'manual' ? '手工修改' : '自动对账' }}</span></td>
            <td class="text-secondary">{{ h.probe_name || '—' }}</td>
          </tr>
        </tbody>
      </table>
      <div class="text-secondary small mt-2" v-else>暂无变化记录（该线路出口 IP 未发生过变更）</div>
    </div>

    <div class="nw-card mb-3">
      <div class="d-flex align-items-center">
        <h6 class="mb-0">近 24 小时时延（各探测目标）</h6>
        <span class="text-secondary small ms-auto">数据来自 VictoriaMetrics，5 分钟粒度</span>
      </div>
      <div style="height: 260px" class="mt-2"><canvas id="rttChart"></canvas></div>
    </div>

    <div class="nw-card">
      <div class="d-flex align-items-center">
        <h6 class="mb-0">连通性心跳</h6>
        <span class="text-secondary small ms-auto">每格 = 1 分钟探测汇总 · 右端最新，放不下最左移出 · 数据来自 VictoriaMetrics</span>
      </div>
      <div class="mt-2" v-if="hbCells.length"><HeartbeatGrid :cells="hbCells" :end="hbEnd" legend axis /></div>
      <div class="text-secondary small py-3 text-center" v-else>暂无连通性数据。</div>
    </div>

    <div class="nw-card mt-3">
      <h6>近 7 天测速（下行 Mbps，30 分钟粒度）</h6>
      <div style="height: 220px" class="mt-2"><canvas id="speedChart"></canvas></div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import dayjs from 'dayjs'
import Chart from 'chart.js/auto'
import { api } from '../api'
import { fmtTime, goBack, vmMinuteCells } from '../utils'
import StatusBadge from '../components/StatusBadge.vue'
import HeartbeatGrid from '../components/HeartbeatGrid.vue'

const route = useRoute()
const router = useRouter()
const line = ref({}), probeIds = ref([]), status = ref('unknown')
// 出口 IP 变化轨迹（探针对账自动回写与手工修改均留痕）
const ipHistory = ref([])
// 连通性心跳（1 格 = 1 分钟滑动窗口，HeartbeatGrid 渲染）
const hbCells = ref([]), hbEnd = ref(0)
// 在线 IP 卡片的悬停明细：逐楼层在线数（多楼层线路便于核对加总）
const onlineDetail = computed(() => (line.value.online_ips || [])
  .map(f => `${f.floor_name}：${f.up}/${f.total}`).join('；'))
let rttChart = null, speedChart = null

const palette = ['#2f81f7', '#05c96b', '#ffc107', '#dc3545', '#6f42c1', '#20c997', '#fd7e14', '#adb5bd']

async function loadLine() {
  const [lines, ov, ps] = await Promise.all([
    api.get('/api/admin/lines'), api.get('/api/admin/overview'), api.get('/api/admin/probes'),
  ])
  line.value = lines.find(l => l.id === Number(route.params.id)) || {}
  // 探针 id → 显示名（测速图按 probe 分组，图例显示 probe-N 而非原始 UUID）
  for (const p of ps) probeNames[p.probe.id] = p.probe.name || p.probe.hostname
  const ol = (ov.lines || []).find(l => l.id === Number(route.params.id))
  if (ol) {
    probeIds.value = ol.probe_ids || []
    status.value = ol.status
  }
}

let probeNames = {}
function probeLabel(id) { return probeNames[id] || `探针 ${String(id).slice(0, 8)}` }

async function fetchSeries(expr, hours = 24, step = 300, range = null) {
  const start = range ? range.start : dayjs().subtract(hours, 'hour').unix()
  const end = range ? range.end : dayjs().unix()
  const d = await api.get(`/api/admin/metrics/query_range?query=${encodeURIComponent(expr)}&start=${start}&end=${end}&step=${step}`)
  return (d.data && d.data.result) || []
}

async function draw() {
  const name = line.value.name
  if (!name) return

  const rttSeries = await fetchSeries(`avg by (target) (nw_ping_rtt_ms{line="${name}"})`)

  const mk = (series, yTitle) => {
    const labels = series[0] ? series[0].values.map(v => dayjs(v[0] * 1000).format('HH:mm')) : []
    const datasets = series.map((s, i) => ({
      label: s.metric.target || probeLabel(s.metric.probe),
      data: s.values.map(v => v[1] == null ? null : Number(v[1])),
      borderColor: palette[i % palette.length],
      backgroundColor: 'transparent', borderWidth: 1.6, pointRadius: 0, tension: .25, spanGaps: true,
    }))
    return { labels, datasets, yTitle }
  }

  const render = (id, cfg) => {
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

  rttChart = render('rttChart', mk(rttSeries, 'ms'))
  // 连通性心跳：1 格 = 1 分钟（avg by (target) 的 [300s] 滚动窗口 step=60，与降质告警 5min 窗口
  // 同款——vmMinuteCells 逐点按目标丢包率下中位数判黄格），取近 3 小时，
  // 组件按容器宽度滑动展示；终点前移 90s 避开在途分钟与 VM 写入延迟
  const end = dayjs().unix() - 90
  const grid = await fetchSeries(`avg by (target) (avg_over_time(nw_ping_ok{line="${name}"}[300s]))`, 3, 60, { start: end - 3 * 3600, end }).catch(() => [])
  hbCells.value = vmMinuteCells(grid)
  hbEnd.value = end

  // 测速曲线（近 7 天，30 分钟粒度；契约下行参考线已删——固定水平线无信息量，用户要求）
  const speedSeries = await fetchSeries7d(`max by (probe) (nw_speed_down_mbps{line="${name}"})`)
  speedChart = render('speedChart', mk(speedSeries, 'Mbps'))
}

async function fetchSeries7d(expr) {
  const start = dayjs().subtract(7, 'day').unix()
  const end = dayjs().unix()
  const d = await api.get(`/api/admin/metrics/query_range?query=${encodeURIComponent(expr)}&start=${start}&end=${end}&step=1800`)
  return (d.data && d.data.result) || []
}

onMounted(async () => {
  await loadLine()
  api.get(`/api/admin/lines/${route.params.id}/ip-history`).then(d => { ipHistory.value = d || [] }).catch(() => {})
  await draw()
})
</script>
