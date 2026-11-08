<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <div>
        <h4 class="mb-0">📋 总览</h4>
        <div class="text-secondary small mt-1">每 10 秒自动刷新<span v-if="err" class="text-danger ms-2">{{ err }}</span></div>
      </div>
    </div>

    <!-- 顶部统计行（2026-11-08 用户要求）：监测对象/互联网连通率/在线线路/平均测速/在线 IP。
         连通率与平均测速为「今日 00:00 至今」口径（平均测速剔除专线线路，即只统计线路 1-11），
         在线 IP 取各楼层最近一次扫描结果；随 10s 轮询刷新 -->
    <div class="row g-3 mb-3 row-cols-2 row-cols-md-5">
      <div class="col" v-for="s in summaryCards" :key="s.label">
        <div class="nw-card h-100">
          <div class="text-secondary small">{{ s.label }}</div>
          <div class="stat-num" :style="{ color: s.color }">
            <CountUp :value="s.num" :decimals="s.decimals || 0" :suffix="s.suffix || ''" />
          </div>
          <div class="stat-sub">{{ s.sub }}</div>
        </div>
      </div>
    </div>

    <div v-if="unbound.length" class="alert alert-warning py-2">
      有 {{ unbound.length }} 台探针未绑定线路：
      <code v-for="p in unbound" :key="p.id" class="me-2" :title="p.id">{{ p.name || p.hostname || p.id.slice(0,8) }}</code>
      → 前往 <router-link to="/probes/manage">探针配置</router-link> 完成绑定
    </div>

    <!-- 四卡片布局（2026-11-08 用户要求）：左上「内网」+ 左下「行业网」上下排列，
         右侧「今日告警」与左列等高、列表内部滚轮滚动，下方「互联网」通栏自适应高度。
         左右两列宽度 3:2；左列内容不足时按 3:2 分配剩余高度（内网高于行业网）。 -->
    <div class="dash-top mb-3">
      <div class="dash-left">
        <section class="nw-card dash-sec dash-sec-internal">
          <div class="dash-sec-head">
            <h5 class="mb-0">内网 · 内网服务</h5>
            <span class="text-secondary small">中心服务器内置探测（TCP / HTTP / ICMP）</span>
          </div>
          <div v-if="monitors.length" class="row g-3">
            <div class="col-sm-6 col-xxl-4" v-for="mst in monitors" :key="'m' + mst.name">
              <div class="nw-card line-card no-link h-100" :class="stCls(mst)">
                <div class="d-flex align-items-center gap-2 mb-2">
                  <StatusBadge :status="stOf(mst)" class="ms-auto order-2" />
                  <div class="fw-bold order-1">{{ mst.name }}</div>
                </div>
                <div class="text-secondary small mb-2">{{ typeOf(mst.type) }} · {{ mst.target }}</div>
                <HeartbeatGrid v-if="cellsOf(monHb[mst.name]).length" :cells="cellsOf(monHb[mst.name])" :end="hbEnd" :size="13" class="mb-2" />
                <div class="d-flex justify-content-between small">
                  <span>时延 <b>{{ mst.known && mst.rtt_ms ? mst.rtt_ms.toFixed(1) : '—' }}</b> ms</span>
                  <span>结果 <b v-if="!mst.known" class="text-secondary">—</b>
                    <b v-else-if="mst.type === 'http'" :style="{ color: mst.up ? '#05c96b' : '#dc3545' }">HTTP {{ mst.code || '?' }}</b>
                    <b v-else :style="{ color: mst.up ? '#05c96b' : '#dc3545' }">{{ mst.up ? '通' : '断' }}</b></span>
                </div>
              </div>
            </div>
          </div>
          <div v-else class="dash-empty">
            暂无内网监控目标。到「系统设置 → 探测配置 → 内网服务监控」添加（如 OA 系统、核心服务器、交换机管理地址）。
          </div>
        </section>

        <section class="nw-card dash-sec dash-sec-industry">
          <div class="dash-sec-head">
            <h5 class="mb-0">行业网 · 核心设备监测</h5>
            <span class="text-secondary small">中心服务器对行业网 IP 仅 ICMP ping（不做其他探测）</span>
          </div>
          <div v-if="indMons.length" class="row g-3">
            <div class="col-sm-6 col-xxl-4" v-for="m in indMons" :key="'i' + m.name">
              <div class="nw-card line-card no-link h-100" :class="stCls(m)">
                <div class="d-flex align-items-center gap-2 mb-2">
                  <StatusBadge :status="stOf(m)" class="ms-auto order-2" />
                  <div class="fw-bold order-1">{{ m.name }}</div>
                </div>
                <div class="text-secondary small mb-2">ICMP · {{ m.target }}</div>
                <HeartbeatGrid v-if="cellsOf(indHb[m.name]).length" :cells="cellsOf(indHb[m.name])" :end="hbEnd" :size="13" class="mb-2" />
                <div class="d-flex justify-content-between small">
                  <span>时延 <b>{{ m.known && m.rtt_ms ? m.rtt_ms.toFixed(1) : '—' }}</b> ms</span>
                  <span>结果 <b v-if="!m.known" class="text-secondary">—</b>
                    <b v-else :style="{ color: m.up ? '#05c96b' : '#dc3545' }">{{ m.up ? '通' : '断' }}</b></span>
                </div>
              </div>
            </div>
          </div>
          <div v-else class="dash-empty">
            暂无行业网监测目标。到「系统设置 → 探测配置 → 行业网监测」添加（仅 ICMP，如 10.5.x.x 的核心设备地址）。
          </div>
        </section>
      </div>

      <div class="dash-alert-cell">
        <div class="nw-card dash-alerts">
          <div class="dash-sec-head">
            <h5 class="mb-0">今日告警</h5>
            <span class="text-secondary small">{{ todayAlerts.length }} 条<template v-if="firingCount"> · 进行中 {{ firingCount }}</template></span>
            <router-link to="/alerts" class="small text-decoration-none ms-auto">告警中心 →</router-link>
          </div>
          <div class="dash-alarm-list">
            <!-- 列序（用户指定，从左到右）：级别 → 告警内容 → 时间 → 状态 -->
            <div v-for="a in todayAlerts" :key="a.id" class="dash-alarm">
              <span class="badge flex-shrink-0" :class="sevCls(a.severity)">{{ a.severity }}</span>
              <div class="dash-alarm-title" :title="a.title">{{ a.title }}</div>
              <span class="dash-alarm-time">{{ hhmmss(a.starts_at) }}</span>
              <span class="dash-alarm-st" :class="a.status === 'firing' ? 'is-firing' : 'is-recovered'">
                {{ a.status === 'firing' ? '进行中' : '已恢复' }}
              </span>
            </div>
            <div v-if="!todayAlerts.length" class="dash-empty">{{ alertErr || '今天暂无告警' }}</div>
          </div>
        </div>
      </div>
    </div>

    <!-- 互联网（内网 → 外网 · 宽带线路）：通栏卡片，高度随线路卡数量自适应 -->
    <section class="nw-card">
      <div class="dash-sec-head">
        <h5 class="mb-0">互联网 · 宽带线路</h5>
        <span class="text-secondary small">楼层探针逐线路探测（ICMP / TCP / DNS / HTTP）</span>
      </div>

      <div v-if="!lines.length && !err" class="text-center py-4 text-secondary">
        尚未录入线路。请先到 <router-link to="/lines">线路配置</router-link> 完成配置，再在
        <router-link to="/probes/manage">探针配置</router-link> 中绑定探针。
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
            <HeartbeatGrid v-if="hbOf(l).length" :cells="hbOf(l)" :end="hbEndOf(l)" :size="13" class="mb-2" />
            <div class="d-flex justify-content-between small" v-if="probeOf(l)">
              <span>时延 <b>{{ rttOf(l) }}</b> ms</span>
              <span>丢包 <b :class="lossCls(l)">{{ lossOf(l) }}</b> %</span>
              <span>测速 <b>{{ speedOf(l) }}</b></span>
              <span>TCP <b :style="{ color: probeOf(l).tcp_ok ? '#05c96b' : '#dc3545' }">{{ probeOf(l).tcp_ok ? '通' : '断' }}</b></span>
            </div>
            <div v-else class="small text-secondary">未绑定探针</div>
          </div>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import dayjs from 'dayjs'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import { minuteCells, vmMinuteCells, applyStatusToCurrent } from '../utils'
import HeartbeatGrid from '../components/HeartbeatGrid.vue'
import CountUp from '../components/CountUp.vue'

const lines = ref([]), unbound = ref([]), monitors = ref([]), indMons = ref([]), err = ref('')
// 内→内/行业网心跳格：VM 逐分钟序列（详情页同款 avg_over_time[60s] step=60），
// 终点前移 90s 避开在途分钟假灰。刷新对齐分钟边界 +5s：格子是分钟粒度（探测 30s/次
// → 每分钟 2 样本聚成 1 格），整点过后新格刚完成即取，最陈 ~65s；查询频率 1 次/分钟，
// 不必跟 10s 总览轮询（那 6 倍查询拿到的结果相同）
const monHb = ref({}), indHb = ref({}), hbEnd = ref(0)
let timer = null, hbTimer = null

// ===== 今日告警（红色卡片右侧滚动列表） =====
// 接口只支持滚动小时窗（hours 按 starts_at 过滤），取「今天零点至今」的上取整小时再按
// 日期精确过滤，避免窗口起点落在昨天而把昨天的告警算进来；limit 200 足够一天量级。
const todayAlerts = ref([]), alertErr = ref('')
const firingCount = computed(() => todayAlerts.value.filter(a => a.status === 'firing').length)
function hhmmss(t) { return dayjs(t).format('HH:mm:ss') }
function sevCls(s) { return { P0: 'bg-dark', P1: 'bg-danger', P2: 'bg-warning text-dark' }[s] || 'bg-secondary' }
async function loadAlerts() {
  const start = dayjs().startOf('day')
  const hours = Math.max(1, Math.ceil(dayjs().diff(start, 'minute') / 60))
  try {
    const d = await api.get(`/api/admin/alerts?hours=${hours}&limit=200`)
    todayAlerts.value = (d.rows || []).filter(a => !dayjs(a.starts_at).isBefore(start))
    alertErr.value = ''
  } catch (e) { alertErr.value = '告警加载失败：' + e.message }
}

// ===== 状态与样式（与线路卡同款 st-* 描边） =====
function stOf(m) { return !m.known ? 'unknown' : (m.up ? 'online' : 'down') }
function stCls(m) { return 'st-' + stOf(m) }
const TYPE_LABEL = { http: 'HTTP', tcp: 'TCP', icmp: 'ICMP', dns: 'DNS' }
function typeOf(t) { return TYPE_LABEL[t] || (t || '').toUpperCase() || '—' }

// ===== 心跳格数据 =====
function groupBy(result, label) {
  const by = {}
  for (const s of result || []) {
    const k = (s.metric && s.metric[label]) || '?'
    ;(by[k] = by[k] || []).push(s)
  }
  return by
}
function cellsOf(series) { return vmMinuteCells(series || []) }
function hbEndOf(l) {
  // 数据驱动尾格锚点（与大屏同款）：正常时尾格永远是有数据的分钟，绝不假灰
  const ms = (probeOf(l) && probeOf(l).minutes) || []
  const newest = ms.length ? ms[ms.length - 1].t : 0
  return Math.max(newest, Math.floor(Date.now() / 60000) * 60 - 150)
}
async function loadHb() {
  try {
    const end = Math.floor(Date.now() / 1000) - 90
    const q = async (expr) => {
      const d = await api.get(`/api/admin/metrics/query_range?query=${encodeURIComponent(expr)}&start=${end - 65 * 60}&end=${end}&step=60`)
      return (d.data && d.data.result) || []
    }
    const [mons, inds] = await Promise.all([
      q('avg_over_time(nw_monitor_ok[60s])'),
      q('avg_over_time(nw_industry_ok[60s])'),
    ])
    monHb.value = groupBy(mons, 'monitor')
    indHb.value = groupBy(inds, 'industry')
    hbEnd.value = end
  } catch (e) { /* 心跳数据拉取失败不打断总览主体 */ }
}

// 当天统计（连通率/平均测速）：窗口 = 今日 00:00 至今，用 sum(sum_over_time)/sum(count_over_time)
// 做样本加权，避免不同探针/目标采样密度不同导致偏差；测速剔除专线线路（名称含「专线」）。
async function loadToday() {
  const now = Math.floor(Date.now() / 1000)
  const since = dayjs().startOf('day').unix()
  const win = Math.max(60, now - since)
  const q = async (expr) => {
    const d = await api.get(`/api/admin/metrics/query_range?query=${encodeURIComponent(expr)}&start=${now - 60}&end=${now}&step=60`)
    const res = (d.data && d.data.result) || []
    const vals = (res[0] && res[0].values) || []
    const v = vals.length ? parseFloat(vals[vals.length - 1][1]) : NaN
    return Number.isFinite(v) ? v : null
  }
  try {
    const [rate, speed] = await Promise.all([
      q(`sum(sum_over_time(nw_ping_ok[${win}s])) / sum(count_over_time(nw_ping_ok[${win}s]))`),
      q(`sum(sum_over_time(nw_speed_down_mbps{line!~".*专线.*"}[${win}s])) / sum(count_over_time(nw_speed_down_mbps{line!~".*专线.*"}[${win}s]))`),
    ])
    todayStats.value = { rate, speed }
  } catch (e) { /* 统计查询失败保留上一次数值，不打断总览 */ }
}

// 线路状态计数（顶部统计行与互联网卡共用口径：正常/降质/中断/盲区）
const lineCounts = computed(() => {
  const c = { online: 0, bad: 0, blind: 0, degraded: 0, total: lines.value.length }
  for (const l of lines.value) {
    if (l.status === 'online') c.online++
    else if (l.status === 'blind') c.blind++
    else if (l.status === 'degraded' || l.status === 'speed_low') c.degraded++
    else if (l.status !== 'unknown') c.bad++
  }
  return c
})

// 在线 IP（当前值，非时间窗）：各楼层最近一次 IP 扫描的在线数，按 floor_id 去重后合计
// （线路的 online_ips 来自 LatestFloorScans，即每楼层最近一次已完成扫描）
const onlineIPs = computed(() => {
  const byFloor = new Map()
  for (const l of lines.value) {
    for (const f of l.online_ips || []) byFloor.set(f.floor_id, f)
  }
  let up = 0
  for (const f of byFloor.values()) up += f.up || 0
  return { up, floors: byFloor.size }
})

// 当天统计（今日 00:00 至今，VM 聚合；页面加载后每 10s 与总览同步刷新）：
// 连通率 = 全部线路 ping 样本成功率；平均测速 = nw_speed_down_mbps 样本加权均值，
// 且剔除专线线路（100M 协议带宽，与 1-11 号 1000M 线路不可比——用户要求只统计线路 1-11）。
const todayStats = ref({ rate: null, speed: null })

const summaryCards = computed(() => {
  const c = lineCounts.value
  const total = lines.value.length + monitors.value.length + indMons.value.length
  const rate = todayStats.value.rate
  const avgSpeed = todayStats.value.speed
  const ratePct = rate == null ? null : rate * 100 // PromQL 返回 0~1 的比例，展示为百分比
  const rateColor = ratePct == null ? '#1d2635' : ratePct >= 99 ? '#05c96b' : ratePct >= 95 ? '#ffc107' : '#dc3545'
  const lineColor = c.bad > 0 ? '#dc3545' : c.degraded > 0 ? '#ffc107' : '#05c96b'
  return [
    {
      label: '监测对象', num: total, color: '#1d2635',
      sub: `线路 ${lines.value.length} · 内网 ${monitors.value.length} · 行业网 ${indMons.value.length}`,
    },
    {
      label: '互联网连通率', num: ratePct, decimals: 1, suffix: '%', color: rateColor,
      sub: ratePct == null ? '今日暂无探测样本' : '今日 00:00 至今 · 全部线路',
    },
    {
      label: '在线线路', num: c.online, suffix: `/${c.total}`, color: c.total ? lineColor : '#adb5bd',
      sub: `降质 ${c.degraded} · 中断 ${c.bad} · 盲区 ${c.blind}`,
    },
    {
      label: '平均测速', color: '#2f81f7',
      num: avgSpeed, decimals: avgSpeed != null && avgSpeed < 100 ? 1 : 0, suffix: ' Mbps',
      sub: avgSpeed == null ? '今日暂无测速数据' : '今日均值 · 线路 1-11',
    },
    {
      label: '在线 IP', num: onlineIPs.value.up, color: '#1d2635',
      sub: onlineIPs.value.floors ? `最近一次扫描 · ${onlineIPs.value.floors} 个楼层` : '尚无扫描数据',
    },
  ]
})

const lossThresh = ref(10) // 降级丢包阈值（接口下发，与 P2 告警同口径）

function probeOf(l) { return (l.statuses && l.statuses[0]) || null }
// 心跳格：绑定探针的逐分钟汇总（1 格 = 1 分钟，滑动窗口；黄格=降级，与告警同口径）
function hbOf(l) {
  // 当前分钟按线路状态兜底：已中断/降质时不允许当前格还是绿色
  return applyStatusToCurrent(minuteCells(probeOf(l) && probeOf(l).minutes, lossThresh.value), l.status)
}
function rttOf(l) { const p = probeOf(l); return p && p.rtt_ms ? p.rtt_ms.toFixed(1) : '—' }
function lossOf(l) { const p = probeOf(l); return p && p.loss_pct != null ? p.loss_pct.toFixed(1) : '—' }
function lossCls(l) { const p = probeOf(l); return p && p.loss_pct > lossThresh.value ? 'text-warning' : '' }
function speedOf(l) {
  // 多探针线路取最近一次测速值（与线路状态页口径一致），而非固定首个探针
  const ss = ((l.statuses || []).filter(s => s.speed_down_mbps > 0))
    .sort((a, b) => (b.speed_at || '').localeCompare(a.speed_at || ''))
  if (!ss.length) return '—'
  const v = ss[0].speed_down_mbps
  return v >= 100 ? Math.round(v) + 'M' : v.toFixed(1) + 'M'
}
function cls(s) { return 'st-' + (s || 'unknown') }

async function load() {
  try {
    const d = await api.get('/api/admin/overview')
    lines.value = d.lines || []
    unbound.value = d.unbound_probes || []
    monitors.value = d.internal_monitors || []
    indMons.value = d.industry_monitors || []
    if (d.loss_thresh > 0) lossThresh.value = d.loss_thresh
    err.value = ''
  } catch (e) { err.value = e.message }
}

// 心跳格刷新对齐分钟边界 +5s：新分钟格刚完成即取（最陈 ~65s），频率仍 1 次/分钟
function scheduleHb() {
  const delay = (60 - (Date.now() / 1000) % 60) * 1000 + 5000
  hbTimer = setTimeout(() => { loadHb(); scheduleHb() }, delay)
}

onMounted(() => {
  load(); loadHb(); loadAlerts(); loadToday()
  timer = setInterval(() => { load(); loadAlerts(); loadToday() }, 10000)
  scheduleHb()
})
onUnmounted(() => { clearInterval(timer); clearTimeout(hbTimer) })
</script>
