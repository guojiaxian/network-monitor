<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <router-link to="/lines" class="btn btn-sm btn-link px-0 me-2">← 返回</router-link>
      <h4 class="mb-0">{{ line.name || '线路详情' }}</h4>
      <StatusBadge :status="status" class="ms-3" />
    </div>

    <div class="row g-3 mb-3" v-if="line.id">
      <div class="col-md-3"><div class="nw-card"><div class="text-secondary small">楼层 / 子网</div><b>{{ line.floor_name || '—' }}</b> <code>{{ line.cidr || '' }}</code></div></div>
      <div class="col-md-3"><div class="nw-card"><div class="text-secondary small">出口公网 IP</div><b>{{ line.public_ip || '未填写' }}</b></div></div>
      <div class="col-md-3"><div class="nw-card"><div class="text-secondary small">契约带宽</div><b>{{ line.contract_down_mbps || '?' }}M / {{ line.contract_up_mbps || '?' }}M</b></div></div>
      <div class="col-md-3"><div class="nw-card"><div class="text-secondary small">绑定探针</div><b>{{ probeIds.length || '无' }}</b> 台</div></div>
    </div>

    <div class="nw-card mb-3">
      <div class="d-flex align-items-center">
        <h6 class="mb-0">近 24 小时时延（各探测目标）</h6>
        <span class="text-secondary small ms-auto">数据来自 VictoriaMetrics，5 分钟粒度</span>
      </div>
      <div style="height: 260px" class="mt-2"><canvas id="rttChart"></canvas></div>
    </div>

    <div class="nw-card">
      <h6>近 24 小时连通率（%，100=全部成功）</h6>
      <div style="height: 220px" class="mt-2"><canvas id="okChart"></canvas></div>
    </div>

    <div class="nw-card mt-3" v-if="line.contract_down_mbps > 0 || true">
      <div class="d-flex align-items-center">
        <h6 class="mb-0">近 24 小时真实流量（SNMP，上下行 Mbps）</h6>
        <span class="text-secondary small ms-auto" v-if="!snmpHasData">未启用 SNMP 采集或暂无数据（可在系统设置中开启）</span>
      </div>
      <div style="height: 220px" class="mt-2"><canvas id="snmpChart"></canvas></div>
    </div>

    <div class="nw-card mt-3">
      <h6>近 7 天测速（下行 Mbps，30 分钟粒度）</h6>
      <div style="height: 220px" class="mt-2"><canvas id="speedChart"></canvas></div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import dayjs from 'dayjs'
import Chart from 'chart.js/auto'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge.vue'

const route = useRoute()
const line = ref({}), probeIds = ref([]), status = ref('unknown')
const snmpHasData = ref(false)
let rttChart = null, okChart = null, speedChart = null, snmpChart = null

const palette = ['#2f81f7', '#05c96b', '#ffc107', '#dc3545', '#6f42c1', '#20c997', '#fd7e14', '#adb5bd']

async function loadLine() {
  const [lines, ov] = await Promise.all([api.get('/api/admin/lines'), api.get('/api/admin/overview')])
  line.value = lines.find(l => l.id === Number(route.params.id)) || {}
  const ol = (ov.lines || []).find(l => l.id === Number(route.params.id))
  if (ol) {
    probeIds.value = ol.probe_ids || []
    status.value = ol.status
  }
}

async function fetchSeries(expr) {
  const start = dayjs().subtract(24, 'hour').unix()
  const end = dayjs().unix()
  const d = await api.get(`/api/admin/metrics/query_range?query=${encodeURIComponent(expr)}&start=${start}&end=${end}&step=300`)
  return (d.data && d.data.result) || []
}

async function draw() {
  const name = line.value.name
  if (!name) return

  const rttSeries = await fetchSeries(`avg by (target) (nw_ping_rtt_ms{line="${name}"})`)
  const okSeries = await fetchSeries(`avg by (target) (nw_ping_ok{line="${name}"}) `)

  const mk = (series, yTitle) => {
    const labels = series[0] ? series[0].values.map(v => dayjs(v[0] * 1000).format('HH:mm')) : []
    const datasets = series.map((s, i) => ({
      label: s.metric.target || s.metric.probe,
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
  okChart = render('okChart', mk(okSeries.map(s => ({ ...s, values: s.values.map(v => [v[0], (Number(v[1]) * 100)]) })), '%'))

  // 测速曲线（近 7 天，含契约带宽参考线）
  if (line.value.contract_down_mbps > 0) {
    const speedSeries = await fetchSeries7d(`max by (probe) (nw_speed_down_mbps{line="${name}"})`)
    const sc = mk(speedSeries, 'Mbps')
    sc.datasets.push({
      label: `契约下行 ${line.value.contract_down_mbps}M`,
      data: sc.labels.map(() => line.value.contract_down_mbps),
      borderColor: '#dc3545', borderDash: [6, 4], borderWidth: 1.2, pointRadius: 0, fill: false,
    })
    speedChart = render('speedChart', sc)
  }

  // SNMP 真实流量曲线（近 24h，bps → Mbps）
  const snmpIn = await fetchSeries(`avg_over_time(nw_snmp_in_bps{line="${name}"}[5m])`).catch(() => [])
  const snmpOut = await fetchSeries(`avg_over_time(nw_snmp_out_bps{line="${name}"}[5m])`).catch(() => [])
  if (snmpIn.length || snmpOut.length) {
    snmpHasData.value = true
    const base = snmpIn[0] || snmpOut[0]
    const labels = base.values.map(v => dayjs(v[0] * 1000).format('HH:mm'))
    const datasets = []
    if (snmpIn.length) {
      datasets.push({ label: '下行（收）', data: snmpIn[0].values.map(v => v[1] == null ? null : Number(v[1]) / 1e6),
        borderColor: '#2f81f7', backgroundColor: 'transparent', borderWidth: 1.6, pointRadius: 0, tension: .25, spanGaps: true })
    }
    if (snmpOut.length) {
      datasets.push({ label: '上行（发）', data: snmpOut[0].values.map(v => v[1] == null ? null : Number(v[1]) / 1e6),
        borderColor: '#05c96b', backgroundColor: 'transparent', borderWidth: 1.6, pointRadius: 0, tension: .25, spanGaps: true })
    }
    if (line.value.contract_down_mbps > 0) {
      datasets.push({ label: `契约下行 ${line.value.contract_down_mbps}M`,
        data: labels.map(() => line.value.contract_down_mbps),
        borderColor: '#dc3545', borderDash: [6, 4], borderWidth: 1.2, pointRadius: 0, fill: false })
    }
    const el = document.getElementById('snmpChart')
    if (el) {
      snmpChart = new Chart(el, {
        type: 'line',
        data: { labels, datasets },
        options: {
          responsive: true, maintainAspectRatio: false, animation: false,
          interaction: { mode: 'index', intersect: false },
          scales: { y: { title: { display: true, text: 'Mbps' } } },
          plugins: { legend: { labels: { boxWidth: 12, font: { size: 11 } } } },
        },
      })
    }
  }
}

async function fetchSeries7d(expr) {
  const start = dayjs().subtract(7, 'day').unix()
  const end = dayjs().unix()
  const d = await api.get(`/api/admin/metrics/query_range?query=${encodeURIComponent(expr)}&start=${start}&end=${end}&step=1800`)
  return (d.data && d.data.result) || []
}

onMounted(async () => { await loadLine(); await draw() })
</script>
