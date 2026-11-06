<template>
  <div class="scr">
    <header class="scr-head">
      <div class="scr-brand">
        <span class="scr-dot" :class="globalCls"></span>
        <span class="scr-title">NetWatch · 出口线路监控大屏</span>
      </div>
      <div class="scr-kpis">
        <div class="kpi kpi-ok"><b>{{ okCount }}</b><i>正常</i></div>
        <div class="kpi kpi-bad"><b>{{ badCount }}</b><i>异常</i></div>
        <div class="kpi kpi-blind"><b>{{ blindCount }}</b><i>盲区</i></div>
        <div class="kpi kpi-alarm"><b>{{ alerts.length }}</b><i>告警</i></div>
      </div>
      <div class="scr-clock">
        <div class="scr-time">{{ clock }}</div>
        <div class="scr-date">{{ dateCn }}</div>
      </div>
    </header>

    <div class="scr-body">
      <main class="scr-main">
        <div v-if="!lines.length" class="scr-empty">
          <div class="scr-empty-t">暂无线路数据</div>
          <div class="scr-empty-s">请在管理后台添加线路并绑定探针</div>
        </div>
        <div v-else class="line-grid">
          <div v-for="l in lines" :key="l.id" class="line-card" :class="['st-' + (l.status || 'unknown'), { alarming: isAlarming(l) }]">
            <div class="lc-head">
              <div class="lc-name">{{ l.name }}</div>
              <div class="lc-pill"><span class="lc-pill-dot"></span>{{ statusText(l.status) }}</div>
            </div>
            <div class="lc-sub">{{ l.floor_name || '未关联楼层' }}<template v-if="l.public_ip"> · {{ l.public_ip }}</template><template v-if="l.isp"> · {{ l.isp }}</template></div>
            <div class="lc-hb" :title="'最近 30 次公网探测'">
              <span v-for="(ok, i) in hbOf(l)" :key="i" class="hb-cell" :class="hbClass(l, ok)"></span>
            </div>
            <div class="lc-metrics">
              <div class="m">
                <i>时延</i><b>{{ rttOf(l) }}</b><u>ms</u>
              </div>
              <div class="m">
                <i>丢包</i><b>{{ lossOf(l) }}</b><u>%</u>
              </div>
              <div class="m">
                <i>测速</i><b>{{ speedOf(l) }}</b><u>{{ speedUnitOf(l) }}</u>
              </div>
              <div v-if="speedPctOf(l) !== null" class="m">
                <i>契约</i><b>{{ speedPctOf(l) }}</b><u>%</u>
              </div>
            </div>
            <div v-if="speedPctOf(l) !== null" class="lc-bar">
              <div class="lc-bar-fill" :style="{ width: Math.min(100, speedPctOf(l)) + '%' }" :class="speedBarCls(l)"></div>
            </div>
          </div>
        </div>
      </main>

      <aside class="scr-rail">
        <section class="rail-sec">
          <div class="rail-title">外网探测 <span class="rail-badge" :class="'st-' + extStatus">{{ statusText(extStatus) }}</span></div>
          <div v-if="!extProbes.length" class="rail-none">未部署外网探针</div>
          <div v-for="p in extProbes" :key="p.id" class="rail-row">
            <span class="rdot" :class="'st-' + (p.status?.status || 'unknown')"></span>
            <span class="rname">{{ p.name || p.hostname }}</span>
            <span class="rval">{{ extVal(p) }}</span>
          </div>
        </section>

        <section class="rail-sec" v-if="monitors.length">
          <div class="rail-title">内网服务（内→内）</div>
          <div v-for="m in monitors" :key="m.name" class="rail-row">
            <span class="rdot" :class="m.known ? (m.up ? 'st-online' : 'st-down') : 'st-unknown'"></span>
            <span class="rname">{{ m.name }}</span>
            <span class="rval">{{ m.up ? (m.rtt_ms ? m.rtt_ms.toFixed(1) + 'ms' : '正常') : '失败' }}</span>
          </div>
        </section>

        <section class="rail-sec" v-if="industries.length">
          <div class="rail-title">行业网</div>
          <div v-for="m in industries" :key="m.name" class="rail-row">
            <span class="rdot" :class="m.known ? (m.up ? 'st-online' : 'st-down') : 'st-unknown'"></span>
            <span class="rname">{{ m.name }}</span>
            <span class="rval">{{ m.up ? (m.rtt_ms ? m.rtt_ms.toFixed(1) + 'ms' : '正常') : '失败' }}</span>
          </div>
        </section>

        <section class="rail-sec rail-grow">
          <div class="rail-title">当前告警</div>
          <div v-if="!alerts.length" class="rail-ok">✓ 系统运行正常</div>
          <div v-for="a in alerts" :key="a.id" class="alarm-row">
            <b class="sev" :class="'sev-' + a.severity">{{ a.severity }}</b>
            <span class="alarm-t">{{ a.title }}</span>
            <em class="alarm-since">{{ since(a.starts_at) }}</em>
          </div>
        </section>
      </aside>
    </div>

    <footer class="scr-foot">
      <span v-if="unbound.length" class="foot-warn">⚠ {{ unbound.length }} 台探针未绑定线路</span>
      <span v-if="stale" class="foot-stale">连接中心失败，自动重试中…</span>
      <span class="foot-right">每 10s 自动刷新 · 上次 {{ lastRefresh }}</span>
    </footer>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import dayjs from 'dayjs'
import { api } from '../api'

const lines = ref([]), alerts = ref([]), extProbes = ref([]), extStatus = ref('unknown')
const monitors = ref([]), industries = ref([]), unbound = ref([])
const clock = ref(''), stale = ref(false), lastRefresh = ref('')
let timer = null, clockTimer = null

const okCount = computed(() => lines.value.filter(l => l.status === 'online').length)
const badCount = computed(() => lines.value.filter(l => ['down', 'floor_down', 'degraded', 'speed_low', 'conflict'].includes(l.status)).length)
const blindCount = computed(() => lines.value.filter(l => l.status === 'blind').length)
const globalCls = computed(() => (badCount.value || blindCount.value || alerts.value.length) ? 'st-down' : 'st-online')
const dateCn = computed(() => {
  if (!clock.value) return ''
  const w = '日一二三四五六'[dayjs().day()]
  return `${dayjs().format('YYYY-MM-DD')} 星期${w}`
})

function probeOf(l) { return (l.statuses && l.statuses[0]) || {} }
function rttOf(l) { const p = probeOf(l); return p.rtt_ms ? p.rtt_ms.toFixed(0) : '—' }
function lossOf(l) { const p = probeOf(l); return p.loss_pct != null ? p.loss_pct.toFixed(0) : '—' }
function speedOf(l) {
  const p = probeOf(l)
  if (!p.speed_down_mbps) return '—'
  return p.speed_down_mbps >= 100 ? Math.round(p.speed_down_mbps).toString() : p.speed_down_mbps.toFixed(1)
}
function speedUnitOf(l) { const p = probeOf(l); return p.speed_down_mbps ? 'Mbps' : '' }
// 测速值对契约下行带宽的百分比（无契约或未测速返回 null，不显示）
function speedPctOf(l) {
  const p = probeOf(l)
  if (!p.speed_down_mbps || !l.contract_down_mbps) return null
  return Math.round((p.speed_down_mbps / l.contract_down_mbps) * 100)
}
function speedBarCls(l) {
  const pct = speedPctOf(l)
  if (pct === null) return ''
  if (pct >= 85) return 'bar-ok'
  if (pct >= 70) return 'bar-warn'
  return 'bar-bad'
}
// 心跳条：有数据用探针的 recent（时间正序），无数据铺灰色占位
function hbOf(l) {
  const r = probeOf(l).recent
  if (r && r.length) return r
  return Array(30).fill(null)
}
function hbClass(l, ok) {
  if (ok === null) return 'hb-none'
  return ok ? 'hb-ok' : 'hb-fail'
}
function isAlarming(l) { return ['down', 'floor_down', 'blind'].includes(l.status) }
function statusText(s) {
  return { online: '正常', down: '线路中断', floor_down: '楼层中断', degraded: '降质', speed_low: '带宽不达标', conflict: '数据冲突', blind: '监控盲区', unknown: '待数据' }[s || 'unknown']
}
function extVal(p) {
  const st = p.status || {}
  if (st.status === 'down') return '不可达'
  if (st.rtt_ms) return st.rtt_ms.toFixed(0) + 'ms'
  return statusText(st.status)
}
function since(t) { return dayjs(t).format('HH:mm') + ' 起' }

async function load() {
  try {
    const d = await api.get('/api/screen/overview')
    lines.value = d.lines || []
    alerts.value = d.alerts || []
    extProbes.value = d.external_probes || []
    extStatus.value = d.external_status || 'unknown'
    monitors.value = d.internal_monitors || []
    industries.value = d.industry_monitors || []
    unbound.value = d.unbound_probes || []
    stale.value = false
    lastRefresh.value = dayjs().format('HH:mm:ss')
  } catch {
    stale.value = true // 大屏静默重试，仅页脚提示
  }
}

onMounted(() => {
  load()
  timer = setInterval(load, 10000)
  clockTimer = setInterval(() => { clock.value = dayjs().format('HH:mm:ss') }, 1000)
  clock.value = dayjs().format('HH:mm:ss')
})
onUnmounted(() => { clearInterval(timer); clearInterval(clockTimer) })
</script>

<style scoped>
.scr {
  height: 100vh; display: flex; flex-direction: column; overflow: hidden;
  background: radial-gradient(1200px 600px at 75% -10%, #14263f 0%, transparent 60%), #0a1220;
  color: #d7e1ee; font-family: -apple-system, 'Segoe UI', 'Microsoft YaHei', sans-serif;
  padding: 0 2.4vmin;
}
/* ===== 头部 ===== */
.scr-head { display: flex; align-items: center; gap: 3vmin; padding: 1.8vmin 0 1.4vmin; border-bottom: 1px solid rgba(148, 163, 184, .14); }
.scr-brand { display: flex; align-items: center; gap: 1.2vmin; }
.scr-dot { width: 1.6vmin; height: 1.6vmin; min-width: 10px; min-height: 10px; border-radius: 50%; }
.scr-dot.st-online { background: #34d399; box-shadow: 0 0 12px #34d399; animation: breath 2.4s ease-in-out infinite; }
.scr-dot.st-down { background: #f87171; box-shadow: 0 0 12px #f87171; animation: breath 1s ease-in-out infinite; }
.scr-title { font-size: clamp(18px, 2.6vmin, 32px); font-weight: 700; color: #fff; letter-spacing: .05em; }
.scr-kpis { display: flex; gap: 1.4vmin; margin-left: auto; }
.kpi {
  display: flex; align-items: baseline; gap: .6vmin; padding: .8vmin 1.8vmin;
  border-radius: 1vmin; background: rgba(148, 163, 184, .07); border: 1px solid rgba(148, 163, 184, .12);
}
.kpi b { font-size: clamp(16px, 2.4vmin, 30px); font-variant-numeric: tabular-nums; }
.kpi i { font-style: normal; font-size: clamp(11px, 1.4vmin, 15px); color: #8fa3bd; }
.kpi-ok b { color: #34d399; } .kpi-bad b { color: #f87171; } .kpi-blind b { color: #a78bfa; } .kpi-alarm b { color: #fbbf24; }
.scr-clock { text-align: right; }
.scr-time { font-size: clamp(18px, 2.6vmin, 32px); font-weight: 600; color: #e6eef8; font-variant-numeric: tabular-nums; line-height: 1.1; }
.scr-date { font-size: clamp(11px, 1.5vmin, 16px); color: #8fa3bd; }

/* ===== 主体 ===== */
.scr-body { flex: 1; display: grid; grid-template-columns: 1fr minmax(280px, 24vw); gap: 2vmin; padding: 1.8vmin 0; min-height: 0; }
.scr-main { min-height: 0; overflow-y: auto; }
.line-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(300px, 100%), 1fr)); gap: 1.6vmin; align-content: start; }
.line-card {
  --st: #64748b;
  background: linear-gradient(180deg, rgba(148, 163, 184, .08), rgba(148, 163, 184, .03));
  border: 1px solid rgba(148, 163, 184, .14); border-radius: 1.2vmin;
  border-left: .55vmin solid var(--st);
  padding: 1.6vmin 1.8vmin; display: flex; flex-direction: column; gap: 1vmin; min-height: 15vmin;
}
.st-online { --st: #34d399; } .st-down { --st: #f87171; } .st-floor_down { --st: #fb7185; }
.st-degraded { --st: #fbbf24; } .st-speed_low { --st: #fb923c; } .st-conflict { --st: #f472b6; }
.st-blind { --st: #a78bfa; } .st-unknown { --st: #64748b; }
.line-card.alarming { animation: pulse 1.6s ease-in-out infinite; }
.st-down, .st-floor_down { background: linear-gradient(180deg, rgba(248, 113, 113, .10), rgba(148, 163, 184, .03)); }
.st-blind { background: linear-gradient(180deg, rgba(167, 139, 250, .10), rgba(148, 163, 184, .03)); }

.lc-head { display: flex; align-items: center; justify-content: space-between; gap: 1vmin; }
.lc-name { font-size: clamp(15px, 2vmin, 24px); font-weight: 700; color: #fff; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.lc-pill {
  flex: 0 0 auto; display: flex; align-items: center; gap: .5vmin;
  font-size: clamp(11px, 1.4vmin, 15px); font-weight: 600; color: var(--st);
  padding: .3vmin 1.1vmin; border-radius: 99px; border: 1px solid color-mix(in srgb, var(--st) 45%, transparent);
  background: color-mix(in srgb, var(--st) 12%, transparent);
}
.lc-pill-dot { width: .8vmin; height: .8vmin; min-width: 6px; min-height: 6px; border-radius: 50%; background: var(--st); }
.lc-sub { font-size: clamp(11px, 1.5vmin, 16px); color: #8fa3bd; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* 心跳条 */
.lc-hb { display: flex; gap: 2px; height: 1.4vmin; min-height: 7px; }
.hb-cell { flex: 1; border-radius: 1px; }
.hb-ok { background: #34d399; opacity: .85; }
.hb-fail { background: #f87171; }
.hb-none { background: rgba(148, 163, 184, .18); }

/* 指标 */
.lc-metrics { display: flex; gap: 2.6vmin; flex-wrap: wrap; }
.m { display: flex; align-items: baseline; gap: .5vmin; font-variant-numeric: tabular-nums; }
.m i { font-style: normal; font-size: clamp(10px, 1.3vmin, 14px); color: #7c8fab; }
.m b { font-size: clamp(15px, 2vmin, 24px); font-weight: 600; color: #e6eef8; }
.m u { text-decoration: none; font-size: clamp(10px, 1.3vmin, 14px); color: #7c8fab; }
.lc-bar { height: .7vmin; min-height: 4px; border-radius: 99px; background: rgba(148, 163, 184, .15); overflow: hidden; }
.lc-bar-fill { height: 100%; border-radius: 99px; transition: width .8s; }
.bar-ok { background: #34d399; } .bar-warn { background: #fbbf24; } .bar-bad { background: #f87171; }

/* ===== 右侧信息栏 ===== */
.scr-rail { display: flex; flex-direction: column; gap: 1.6vmin; min-height: 0; overflow-y: auto; }
.rail-sec {
  background: rgba(148, 163, 184, .05); border: 1px solid rgba(148, 163, 184, .12);
  border-radius: 1.2vmin; padding: 1.4vmin 1.6vmin; display: flex; flex-direction: column; gap: 1vmin;
}
.rail-grow { flex: 1; }
.rail-title { font-size: clamp(12px, 1.6vmin, 17px); font-weight: 700; color: #b9c8dc; display: flex; align-items: center; justify-content: space-between; gap: 1vmin; }
.rail-badge { font-size: clamp(10px, 1.3vmin, 14px); font-weight: 600; color: var(--st); }
.rail-row { display: flex; align-items: center; gap: 1vmin; font-size: clamp(12px, 1.5vmin, 16px); }
.rdot { width: .9vmin; height: .9vmin; min-width: 7px; min-height: 7px; border-radius: 50%; flex: 0 0 auto; }
.rdot.st-online { background: #34d399; box-shadow: 0 0 6px rgba(52, 211, 153, .7); }
.rdot.st-down { background: #f87171; box-shadow: 0 0 6px rgba(248, 113, 113, .7); }
.rdot.st-unknown { background: #64748b; }
.rname { color: #d7e1ee; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rval { margin-left: auto; color: #8fa3bd; font-variant-numeric: tabular-nums; flex: 0 0 auto; }
.rail-none, .rail-ok { color: #7c8fab; font-size: clamp(11px, 1.4vmin, 15px); }
.rail-ok { color: #34d399; }

/* 告警列表 */
.alarm-row { display: flex; align-items: baseline; gap: 1vmin; padding: .7vmin 0; border-bottom: 1px dashed rgba(148, 163, 184, .12); font-size: clamp(12px, 1.5vmin, 16px); }
.alarm-row:last-child { border-bottom: none; }
.sev { flex: 0 0 auto; font-size: clamp(10px, 1.3vmin, 14px); padding: .1vmin .8vmin; border-radius: .5vmin; }
.sev-P0 { color: #fff; background: #dc2626; } .sev-P1 { color: #fff; background: #ea580c; } .sev-P2 { color: #0a1220; background: #fbbf24; }
.alarm-t { color: #f3b0b0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.alarm-since { margin-left: auto; font-style: normal; color: #7c8fab; flex: 0 0 auto; font-variant-numeric: tabular-nums; }

/* ===== 空态/页脚 ===== */
.scr-empty { display: flex; flex-direction: column; align-items: center; justify-content: center; height: 100%; gap: 1vmin; }
.scr-empty-t { font-size: clamp(16px, 2.2vmin, 26px); color: #8fa3bd; font-weight: 600; }
.scr-empty-s { font-size: clamp(12px, 1.5vmin, 16px); color: #64748b; }
.scr-foot { display: flex; align-items: center; gap: 3vmin; padding: 1.2vmin 0 1.6vmin; border-top: 1px solid rgba(148, 163, 184, .14); font-size: clamp(11px, 1.4vmin, 15px); }
.foot-warn { color: #fbbf24; }
.foot-stale { color: #f87171; }
.foot-right { margin-left: auto; color: #64748b; font-variant-numeric: tabular-nums; }

@keyframes breath { 0%, 100% { opacity: 1; } 50% { opacity: .45; } }
@keyframes pulse { 0%, 100% { box-shadow: 0 0 0 0 rgba(248, 113, 113, 0); } 50% { box-shadow: 0 0 2.4vmin 0 color-mix(in srgb, var(--st) 35%, transparent); } }

/* 窄屏：右栏落到下方 */
@media (max-width: 1100px) {
  .scr-body { grid-template-columns: 1fr; overflow-y: auto; }
  .scr-kpis { display: none; }
}
</style>
