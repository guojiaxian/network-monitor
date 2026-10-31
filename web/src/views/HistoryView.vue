<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <h4 class="mb-0">探测历史</h4>
      <span class="text-secondary small ms-3">原始探测记录逐条查询（数据保留 90 天）</span>
      <button class="btn btn-sm btn-outline-secondary ms-auto" @click="load">刷新</button>
      <button class="btn btn-sm btn-outline-success ms-2" :disabled="!records.length" @click="exportCSV">导出 CSV</button>
    </div>

    <div class="nw-card mb-3">
      <div class="row g-2 align-items-end">
        <div class="col-md-3">
          <label class="form-label small mb-1">探针 / 监控来源</label>
          <select v-model="f.probe" class="form-select form-select-sm" @change="load">
            <option v-for="s in probeOptions" :key="s.value" :value="s.value">{{ s.label }}</option>
          </select>
        </div>
        <div class="col-md-2">
          <label class="form-label small mb-1">类型</label>
          <select v-model="f.type" class="form-select form-select-sm" @change="load">
            <option value="ping">Ping（内网组）</option>
            <option value="tcp">TCP（内网组）</option>
            <option value="dns">DNS（内网组）</option>
            <option value="http">HTTP（内网组）</option>
            <option value="monitor">内→内 监控</option>
            <option value="speed">测速（内网组）</option>
          </select>
        </div>
        <div class="col-md-3">
          <label class="form-label small mb-1">目标</label>
          <select v-model="f.target" class="form-select form-select-sm" @change="load">
            <option value="">全部目标</option>
            <option v-for="t in targets" :key="t" :value="t">{{ t }}</option>
          </select>
        </div>
        <div class="col-md-2">
          <label class="form-label small mb-1">时间范围</label>
          <select v-model="f.range" class="form-select form-select-sm" @change="load">
            <option value="1800">最近 30 分钟</option>
            <option value="3600">最近 1 小时</option>
            <option value="21600">最近 6 小时</option>
            <option value="86400">最近 24 小时</option>
            <option value="604800">最近 7 天</option>
          </select>
        </div>
        <div class="col-md-2">
          <label class="form-label small mb-1">条数上限</label>
          <select v-model.number="f.limit" class="form-select form-select-sm" @change="load">
            <option :value="2000">2000</option>
            <option :value="5000">5000</option>
            <option :value="10000">10000</option>
            <option :value="20000">20000</option>
          </select>
        </div>
      </div>
      <div v-if="err" class="text-danger small mt-2">{{ err }}</div>
      <div v-if="truncated" class="text-warning small mt-2">
        窗口内共 {{ total }} 条，仅返回最新 {{ records.length }} 条（图表覆盖最新部分）。需要更长历史请缩小时间范围。
      </div>
    </div>

    <div class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">时延趋势（所选目标）</h6>
        <span class="text-secondary small ms-auto">红点 = 该次探测失败 · 绿点 = 成功</span>
      </div>
      <div style="height: 260px" class="mt-1"><canvas id="histRtt"></canvas></div>
      <div v-if="rttChartEmpty" class="text-secondary small text-center py-3">选择类型与目标后显示（当前类型无时延数据或该目标无记录）</div>
    </div>

    <div class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">可用性状态条（全部目标 · PRTG 风格）</h6>
        <span class="text-secondary small ms-auto">
          <span style="color:#05c96b">■ 成功</span>　<span style="color:#dc3545">■ 失败</span>　（每行为一个目标，颜色持续到下一次探测）
        </span>
      </div>
      <canvas id="stripCanvas" style="width:100%;height:1px"></canvas>
      <div v-if="!strips.length" class="text-secondary small text-center py-2">暂无数据</div>
    </div>

    <div class="nw-card">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">明细表（{{ records.length }} 条，按时间倒序）</h6>
        <button class="btn btn-sm btn-outline-secondary ms-auto" @click="showTable = !showTable">{{ showTable ? '收起' : '展开' }}</button>
      </div>
      <div v-show="showTable" style="max-height:480px;overflow:auto">
        <table class="table nw-table table-sm table-hover align-middle mb-0">
          <thead>
            <tr>
              <th style="width:170px">时间</th>
              <th>目标</th>
              <th v-if="f.type === 'ping'" style="width:90px">类别</th>
              <th style="width:110px">结果</th>
              <th style="width:120px">时延/耗时</th>
              <th v-if="f.type === 'http'" style="width:100px">状态码</th>
              <th v-if="f.type === 'http'" style="width:130px">证书剩余</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(r, i) in records" :key="i">
              <td class="small text-secondary">{{ fmt(r.ts_ms) }}</td>
              <td><code class="small">{{ r.target }}</code></td>
              <td v-if="f.type === 'ping'" class="small">{{ r.class === 'gw' ? '网关' : '公网' }}</td>
              <td>
                <span class="badge" :class="r.ok ? 'bg-success' : 'bg-danger'">{{ r.ok ? '成功' : '失败' }}</span>
              </td>
              <td class="small">{{ r.rtt_ms ? r.rtt_ms.toFixed(2) + ' ms' : '—' }}</td>
              <td v-if="f.type === 'http'" class="small">{{ r.code || '—' }}</td>
              <td v-if="f.type === 'http'" class="small" :style="{ color: certColor(r.cert_days) }">{{ certText(r.cert_days) }}</td>
            </tr>
            <tr v-if="!records.length && !err"><td :colspan="tableCols" class="text-secondary text-center py-4">该条件下暂无探测记录</td></tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import dayjs from 'dayjs'
import Chart from 'chart.js/auto'
import { api } from '../api'

const records = ref([]), targets = ref([]), err = ref('')
const rttChartEmpty = ref(false), showTable = ref(true), truncated = ref(false)
const f = ref({ probe: '', type: 'ping', target: '', range: '1800', limit: 2000 })
const probeOptions = ref([])
let rttChart = null
let lastWindow = { start: 0, end: 0 }

const fmt = (ts) => dayjs(ts).format('YYYY-MM-DD HH:mm:ss')
const certText = (d) => !d ? '—' : Math.round(d) + ' 天'
const certColor = (d) => (!d ? '#999' : d < 15 ? '#dc3545' : d < 30 ? '#ffc107' : '#05c96b')
const tableCols = computed(() => f.value.type === 'ping' ? 6 : (f.value.type === 'http' ? 7 : 5))

// strips: [{name, segs:[{x0,x1,ok}]}]
const strips = ref([])

async function load() {
  err.value = ''
  try {
    const end = Math.floor(Date.now() / 1000)
    const start = end - Number(f.value.range)
    lastWindow = { start: start * 1000, end: end * 1000 }
    const qs = new URLSearchParams({
      type: f.value.type, probe: f.value.probe, target: f.value.target,
      start: String(start), end: String(end), limit: String(f.value.limit),
    })
    const d = await api.get('/api/admin/history?' + qs.toString())
    records.value = d.records || []
    targets.value = d.targets || []
    truncated.value = (d.total || 0) > (d.records || []).length
    await nextTick()
    drawStrip()
    drawRtt()
  } catch (e) { err.value = e.message }
}

// 状态色条：每目标一行，canvas 手绘（记录数大也流畅）
function drawStrip() {
  const cv = document.getElementById('stripCanvas')
  if (!cv) return
  const rows = stripRows()
  const rowH = 26, top = 8
  const dpr = window.devicePixelRatio || 1
  const w = cv.parentElement.clientWidth - 16
  cv.height = (top + rows.length * rowH + 8) * dpr
  cv.style.height = (top + rows.length * rowH + 8) + 'px'
  cv.width = w * dpr
  const ctx = cv.getContext('2d')
  ctx.scale(dpr, dpr)
  ctx.clearRect(0, 0, w, cv.height)
  if (!rows.length) { cv.style.height = '30px'; return }

  const { start, end } = lastWindow
  const span = Math.max(1, end - start)
  const x = (ts) => ((ts - start) / span) * (w - 8) + 4
  const labelW = 130
  const plotX0 = x(start), plotW = w - 8 - plotX0

  rows.forEach((row, ri) => {
    const y = top + ri * rowH
    ctx.fillStyle = '#eef1f5'
    ctx.fillRect(plotX0, y, plotW, rowH - 6)
    // 背景段：每段颜色 = 前一次状态（持续到下次探测）
    const recs = row.recs
    if (recs.length) {
      let prev = { ts: start, ok: recs[0].ok }
      for (const r of recs) {
        const x0 = Math.max(plotX0, x(Math.max(prev.ts, start)))
        const x1 = Math.min(plotX0 + plotW, x(r.ts))
        if (x1 > x0) {
          ctx.fillStyle = prev.ok ? '#05c96b' : '#dc3545'
          ctx.fillRect(x0, y, x1 - x0, rowH - 6)
        }
        prev = r
      }
      const x0 = Math.max(plotX0, x(prev.ts))
      if (plotX0 + plotW > x0) {
        ctx.fillStyle = prev.ok ? '#05c96b' : '#dc3545'
        ctx.fillRect(x0, y, plotX0 + plotW - x0, rowH - 6)
      }
    }
    ctx.fillStyle = '#1d2635'
    ctx.font = '12px sans-serif'
    const label = row.name.length > 18 ? row.name.slice(0, 17) + '…' : row.name
    ctx.fillText(label, 4, y + rowH / 2 + 4)
    void labelW
  })
}

// stripRows: 每目标的时间升序记录（图表用全量，不受表格渲染影响）
function stripRows() {
  const byT = {}
  for (const r of [...records.value].reverse()) { // asc
    const key = r.target
    ;(byT[key] = byT[key] || []).push({ ts: r.ts_ms, ok: r.ok })
  }
  return Object.keys(byT).sort().slice(0, 10).map(name => ({ name, recs: byT[name] }))
}

// 时延折线：目标未选时自动取第一个有数据的
function drawRtt() {
  const cv = document.getElementById('histRtt')
  if (!cv) return
  if (rttChart) { rttChart.destroy(); rttChart = null }
  if (f.value.type === 'speed' || f.value.type === 'monitor' && false) { rttChartEmpty.value = true; return }
  let tName = f.value.target
  if (!tName) {
    const rows = stripRows()
    tName = rows.length ? rows[0].name : ''
  }
  const recs = [...records.value].filter(r => r.target === tName).reverse()
  rttChartEmpty.value = recs.length === 0
  if (rttChartEmpty.value || !cv) return

  const labels = recs.map(r => dayjs(r.ts_ms).format('HH:mm:ss'))
  const okData = recs.map(r => (r.ok && r.rtt_ms) ? r.rtt_ms : null)
  const failData = recs.map(r => (!r.ok && r.rtt_ms !== undefined) ? null : null)
  const failPts = recs.map((r, i) => (!r.ok) ? { x: i, y: 0 } : null).filter(Boolean)

  rttChart = new Chart(cv, {
    type: 'line',
    data: {
      labels,
      datasets: [
        {
          label: '时延 ms', data: okData, borderColor: '#2f81f7', backgroundColor: 'transparent',
          borderWidth: 1.6, pointRadius: 0, tension: .2, spanGaps: true,
        },
        {
          label: '失败', data: recs.map(r => r.ok ? null : 0),
          pointBackgroundColor: '#dc3545', pointStyle: 'crossRot', pointRadius: 5,
          showLine: false,
        },
        {
          label: '', data: failPts, showLine: false, pointRadius: 0,
        },
      ].filter(d => d.data.some(v => v !== null)),
    },
    options: {
      responsive: true, maintainAspectRatio: false, animation: false,
      interaction: { mode: 'index', intersect: false },
      scales: { y: { title: { display: true, text: 'ms' } } },
      plugins: { legend: { labels: { boxWidth: 12, font: { size: 11 } } } },
    },
  })
  void failData
}

function exportCSV() {
  const rows = [['时间', '目标', f.value.type === 'ping' ? '类别' : '', '结果', '时延ms', '状态码', '证书剩余天']]
  for (const r of records.value) {
    rows.push([dayjs(r.ts_ms).format('YYYY-MM-DD HH:mm:ss'), r.target,
      f.value.type === 'ping' ? (r.class === 'gw' ? '网关' : '公网') : '',
      r.ok ? '成功' : '失败', r.rtt_ms ? r.rtt_ms.toFixed(2) : '', r.code || '', r.cert_days ? Math.round(r.cert_days) : ''])
  }
  const csv = '\uFEFF' + rows.map(r => r.join(',')).join('\n')
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `netwatch-history-${f.value.type}-${dayjs().format('YYYYMMDD-HHmmss')}.csv`
  a.click()
  URL.revokeObjectURL(a.href)
}

onMounted(async () => {
  try {
    const probes = await api.get('/api/admin/probes')
    probeOptions.value = probes.map(p => {
      const name = p.probe.name || p.probe.hostname || p.probe.id.slice(0, 8)
      return { value: p.probe.id, label: `${name}（${p.probe.group === 'external' ? '外网组' : '内网组'}）` }
    })
    if (probeOptions.value.length) f.value.probe = probeOptions.value[0].value
    await load()
  } catch (e) { err.value = e.message }
  window.addEventListener('resize', drawStrip)
})
onUnmounted(() => window.removeEventListener('resize', drawStrip))
</script>
