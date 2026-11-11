<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <div>
        <h4 class="mb-0">📊 报表</h4>
        <div class="text-secondary small mt-1">连通性 / 告警 / 测速等运维汇总报告，定时自动生成，也可在此手动补生成并导出 Word</div>
      </div>
      <select v-model="kind" class="form-select form-select-sm w-auto ms-auto" @change="load">
        <option value="all">全部</option>
        <option value="day">日报</option>
        <option value="week">周报</option>
        <option value="month">月报</option>
      </select>
      <button class="nw-btn nw-btn-accent ms-2" @click="gen('day')">
        <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M4 0h5.5v1H4a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V4.5h1V14a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V2a2 2 0 0 1 2-2z"/><path d="M9.5 3V.5h1V3H13v1h-2.5v2.5h-1V4H7V3h2.5z"/></svg>生成日报（昨日）
      </button>
      <button class="nw-btn nw-btn-accent ms-2" @click="gen('week')">
        <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M4 0h5.5v1H4a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V4.5h1V14a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V2a2 2 0 0 1 2-2z"/><path d="M9.5 3V.5h1V3H13v1h-2.5v2.5h-1V4H7V3h2.5z"/></svg>生成周报（上周）
      </button>
      <button class="nw-btn nw-btn-accent ms-2" @click="gen('month')">
        <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M4 0h5.5v1H4a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V4.5h1V14a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V2a2 2 0 0 1 2-2z"/><path d="M9.5 3V.5h1V3H13v1h-2.5v2.5h-1V4H7V3h2.5z"/></svg>生成月报（上月）
      </button>
    </div>
    <p v-if="msg" class="small text-danger mb-2">{{ msg }}</p>

    <div class="nw-card mb-3">
      <div class="text-secondary small mb-2">点击任意一行，可在下方查看该报表摘要（无需导出）</div>
      <table class="table nw-table table-sm align-middle">
        <thead><tr><th>类型</th><th>统计周期</th><th>生成时间</th><th>邮件发送</th><th style="width:300px"></th></tr></thead>
        <tbody>
          <tr v-for="r in reports" :key="r.id" class="rep-row" :class="{ 'table-active': detailId === r.id }" @click="view(r)">
            <td><span class="badge" :class="kindBadge(r.kind)">{{ kindName(r.kind) }}</span></td>
            <td>{{ fmt(r.period_start) }} ~ {{ fmt(r.period_end) }}</td>
            <td class="small text-secondary">{{ fmt(r.created_at) }}</td>
            <td class="small">
              <span v-if="r.mailed_at" class="text-secondary">{{ fmt(r.mailed_at) }}</span>
              <span v-else class="badge bg-warning text-dark">待发送</span>
            </td>
            <td class="text-end">
              <button class="btn btn-sm btn-outline-primary me-1" @click.stop="view(r)">{{ detailId === r.id ? '收起' : '查看摘要' }}</button>
              <a class="btn btn-sm btn-success me-1" :href="'#/reports'" @click.prevent.stop="exportDocx(r)">导出 Word</a>
              <a class="btn btn-sm btn-outline-success" :href="'#/reports'" @click.prevent.stop="exportCSV(r)">导出 CSV</a>
            </td>
          </tr>
          <tr v-if="!reports.length"><td colspan="5" class="text-secondary text-center py-4">暂无报表，可手动生成；每日/每周/每月也会按设置自动生成并邮件推送</td></tr>
        </tbody>
      </table>
    </div>

    <div v-if="detail" ref="detailEl" class="nw-card">
      <div class="d-flex mb-2">
        <h6 class="mb-0">{{ kindName(detail.kind) }}：{{ fmt(detail.start) }} ~ {{ fmt(detail.end) }}</h6>
        <button class="btn-close ms-auto" @click="closeDetail"></button>
      </div>

      <!-- 报表摘要（表格形式；旧报表无 summary 时由线路明细反推） -->
      <h6 class="mt-1">报表摘要</h6>
      <table v-if="sum" class="table nw-table table-sm mb-0">
        <tbody>
          <tr v-for="(pair, i) in sumPairs" :key="i">
            <td class="text-secondary" style="width:16%">{{ pair[0] }}</td>
            <td style="width:34%" :style="{ color: pair[4] || undefined, fontWeight: pair[4] ? 'bold' : undefined }">{{ pair[1] }}</td>
            <td class="text-secondary" style="width:16%">{{ pair[2] || '' }}</td>
            <td>{{ pair[3] || '' }}</td>
          </tr>
          <tr v-if="sum.overall"><td class="text-secondary">总体评价</td><td colspan="3">{{ sum.overall }}</td></tr>
          <tr v-if="sum.conclusion"><td class="text-secondary">结论与建议</td><td colspan="3">{{ sum.conclusion }}</td></tr>
        </tbody>
      </table>
      <p v-else class="text-secondary small mb-0">该报表为旧版本数据，无摘要信息（可用下方线路明细查看）。</p>

      <h6 class="mt-3">线路运行明细</h6>
      <table class="table nw-table table-sm">
        <thead><tr><th>线路</th><th>楼层</th><th>可用率</th><th>平均时延</th><th>平均丢包</th><th>测速峰值</th><th>测速均值</th><th>告警次数</th></tr></thead>
        <tbody>
          <tr v-for="l in detail.lines" :key="l.name">
            <td>{{ l.name }}</td><td>{{ l.floor || '—' }}</td>
            <td :style="{ color: color(l.avail_pct), fontWeight: 'bold' }">{{ pct(l.avail_pct) }}</td>
            <td>{{ v(l.avg_rtt_ms, 1) }} ms</td>
            <td>{{ v(l.avg_loss_pct, 2) }} %</td>
            <td>{{ v(l.speed_max_mbps, 0) }} Mbps</td>
            <td>{{ v(l.speed_avg_mbps, 0) }} Mbps</td>
            <td>{{ l.alert_count }}</td>
          </tr>
          <tr v-if="!(detail.lines || []).length"><td colspan="8" class="text-secondary text-center py-3">无数据</td></tr>
        </tbody>
      </table>

      <template v-if="(detail.floors || []).length">
        <h6 class="mt-3">楼层网络与在线终端</h6>
        <table class="table nw-table table-sm">
          <thead><tr><th>楼层</th><th>网段</th><th>关联线路</th><th>在线 IP</th><th>扫描总数</th><th>最近扫描</th></tr></thead>
          <tbody>
            <tr v-for="f in detail.floors" :key="f.name + f.cidr">
              <td>{{ f.name }}</td><td>{{ f.cidr || '—' }}</td><td>{{ f.lines || '—' }}</td>
              <td>{{ f.online_ips == null ? '—' : f.online_ips }}</td>
              <td>{{ f.scan_total || '—' }}</td>
              <td class="small text-secondary">{{ f.scanned_at || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </template>

      <template v-if="(detail.alerts || []).length">
        <h6 class="mt-3">告警明细</h6>
        <table class="table nw-table table-sm">
          <thead><tr><th>触发时间</th><th>级别</th><th>类型</th><th>内容</th><th>持续时长</th><th>状态</th></tr></thead>
          <tbody>
            <tr v-for="(a, i) in detail.alerts" :key="i">
              <td class="small">{{ a.time }}</td>
              <td><span class="badge" :class="levelBadge(a.level)">{{ a.level }}</span></td>
              <td>{{ kindName2(a.kind) }}</td>
              <td>{{ a.title }}</td>
              <td>{{ a.duration }}</td>
              <td><span :class="a.status === '已恢复' ? 'text-success' : 'text-danger'">{{ a.status }}</span></td>
            </tr>
          </tbody>
        </table>
      </template>

      <template v-if="(detail.speeds || []).length">
        <h6 class="mt-3">测速记录（各线路最近一次）</h6>
        <table class="table nw-table table-sm">
          <thead><tr><th>线路</th><th>探针</th><th>测速时间</th><th>下载</th><th>上传</th><th>契约下行</th></tr></thead>
          <tbody>
            <tr v-for="(s, i) in detail.speeds" :key="i">
              <td>{{ s.line }}</td><td>{{ s.probe || '—' }}</td><td class="small text-secondary">{{ s.time }}</td>
              <td>{{ v(s.down_mbps, 1) }} Mbps</td>
              <td>{{ v(s.up_mbps, 1) }} Mbps</td>
              <td>{{ s.contract_mbps > 0 ? s.contract_mbps + ' Mbps' : '—' }}</td>
            </tr>
          </tbody>
        </table>
      </template>

      <template v-for="sec in itemSections" :key="sec.key">
        <template v-if="(detail[sec.key] || []).length">
          <h6 class="mt-3">{{ sec.title }}</h6>
          <table class="table nw-table table-sm">
            <thead><tr><th>{{ sec.nameCol }}</th><th>类型</th><th>目标地址</th><th>可用率</th><th>平均响应</th></tr></thead>
            <tbody>
              <tr v-for="(it, i) in detail[sec.key]" :key="i">
                <td>{{ it.name }}</td><td>{{ it.type }}</td><td>{{ it.target }}</td>
                <td :style="{ color: color(it.avail_pct), fontWeight: 'bold' }">{{ pct(it.avail_pct) }}</td>
                <td>{{ v(it.avg_rtt_ms, 1) }} ms</td>
              </tr>
            </tbody>
          </table>
        </template>
      </template>

      <p class="text-secondary small mb-0 mt-3">生成时间：{{ detail.generated }}；完整版式请在列表中「导出 Word」</p>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, nextTick, onMounted } from 'vue'
import dayjs from 'dayjs'
import { api } from '../api'

const reports = ref([]), detail = ref(null), detailId = ref(null), detailEl = ref(null)
const kind = ref('all'), msg = ref('')

// 通用监测项段落（公网目标/内置监控/行业网共用一套表格列）
const itemSections = [
  { key: 'targets', title: '公网目标探测（TCP/HTTP）', nameCol: '目标' },
  { key: 'monitors', title: '内网内置监控', nameCol: '监控项' },
  { key: 'industries', title: '行业网监测', nameCol: '名称' },
]

const kindName = (k) => ({ day: '日报', week: '周报', month: '月报' }[k] || k)
const kindBadge = (k) => ({ day: 'bg-secondary', week: 'bg-primary', month: 'bg-dark' }[k] || 'bg-secondary')
const levelBadge = (lv) => ({ P0: 'bg-danger', P1: 'bg-warning text-dark', P2: 'bg-info text-dark' }[lv] || 'bg-secondary')
const kindName2 = (k) => ({ line_down: '线路中断', floor_down: '楼层中断', blind: '监控盲区', degraded: '线路降质', speed_low: '网速慢', probe_conflict: '数据冲突', site_down: '网站不可达', site_conflict: '外网数据冲突', cert_expiring: '证书临期', monitor_down: '内网服务不可达', probe_resource: '探针资源紧张', industry_down: '行业网不可达' }[k] || k)

const fmt = (t) => t ? dayjs(t).format('YYYY-MM-DD HH:mm') : '—'
const fmtNum = (n) => n == null ? '—' : Number(n).toLocaleString('zh-CN')
const pct = (v2) => v2 == null ? '无数据' : Number(v2).toFixed(2) + '%'
const v = (val, d) => val == null ? '—' : Number(val).toFixed(d)
const color = (p) => (p == null ? '#999' : p < 99 ? '#dc3545' : '#05c96b')

async function load() {
  reports.value = await api.get(`/api/admin/reports?kind=${kind.value}`)
}

async function view(r) {
  if (detailId.value === r.id) { closeDetail(); return } // 再点同一行收起
  detailId.value = r.id
  try {
    detail.value = await api.get('/api/admin/reports/' + r.id)
  } catch (e) {
    detailId.value = null
    msg.value = e.message
    return
  }
  // 列表较长时把摘要卡滚入视野
  await nextTick()
  detailEl.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function closeDetail() {
  detail.value = null
  detailId.value = null
}

// 摘要数据：新报表用后端 summary；旧报表由线路明细反推关键指标
const sum = computed(() => {
  const d = detail.value
  if (!d) return null
  const s = d.summary
  if (s) return s
  const lines = d.lines || []
  if (!lines.length) return null
  const avgOf = (k) => {
    const vals = lines.map((l) => l[k]).filter((x) => x != null)
    return vals.length ? vals.reduce((a, b) => a + b, 0) / vals.length : null
  }
  return {
    line_total: lines.length,
    floor_total: (d.floors || []).length,
    avg_avail: avgOf('avail_pct'),
    avg_rtt: avgOf('avg_rtt_ms'),
    avg_loss: avgOf('avg_loss_pct'),
    avg_speed: avgOf('speed_avg_mbps'),
    samples: null,
    alert_total: lines.reduce((a, l) => a + (l.alert_count || 0), 0),
  }
})

// 摘要表行：[左指标, 左值, 右指标, 右值, (可选)左值染色]
const sumPairs = computed(() => {
  const m = sum.value
  if (!m) return []
  const num = (x, d2, unit) => (x == null ? '—' : Number(x).toFixed(d2) + unit)
  const alerts = m.alert_p0 == null
    ? `${m.alert_total} 次`
    : `${m.alert_total} 次（P0 ${m.alert_p0} / P1 ${m.alert_p1} / P2 ${m.alert_p2}）`
  const items = [
    ['监测线路数', `${m.line_total} 条`],
    ['楼层网段数', `${m.floor_total} 个`],
    ['平均可用率', num(m.avg_avail, 2, ' %'), color(m.avg_avail)],
    ['平均时延', num(m.avg_rtt, 1, ' ms')],
    ['平均丢包率', num(m.avg_loss, 2, ' %')],
    ['平均测速', num(m.avg_speed, 0, ' Mbps')],
    ['连通性样本数', m.samples == null ? '—' : fmtNum(m.samples)],
    ['告警总数', alerts],
  ]
  // 两列并排，行数减半
  const rows = []
  for (let i = 0; i < items.length; i += 2) {
    const a = items[i], b = items[i + 1] || ['', '', '']
    rows.push([a[0], a[1], b[0], b[1], a[2]])
  }
  return rows
})

function exportCSV(r) {
  const w = window.open(`/api/admin/reports/${r.id}/export.csv`, '_blank')
  if (w) w.opener = null
}

function exportDocx(r) {
  const w = window.open(`/api/admin/reports/${r.id}/export.docx`, '_blank')
  if (w) w.opener = null
}

async function gen(k) {
  msg.value = ''
  try {
    await api.post('/api/admin/reports/generate', { kind: k })
    await load()
  } catch (e) { msg.value = e.message }
}

onMounted(load)
</script>

<style scoped>
/* 整行可点击查看摘要 */
.rep-row { cursor: pointer; }
.rep-row:hover { background: rgba(47, 129, 247, 0.06); }
</style>
