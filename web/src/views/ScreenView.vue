<template>
  <div class="scr">
    <header class="scr-head">
      <div class="scr-brand">
        <span class="scr-dot" :class="globalCls"></span>
        <div class="scr-titles">
          <span class="scr-title">物探中心网络监控系统</span>
          <span class="scr-en">NetWatch · {{ isFloor ? '楼层网络监控大屏' : '出口线路监控大屏' }}</span>
        </div>
      </div>
      <div class="scr-kpis">
        <div class="kpi kpi-ok"><b>{{ okCount }}</b><i>正常</i></div>
        <div class="kpi kpi-bad"><b>{{ badCount }}</b><i>异常</i></div>
        <div class="kpi kpi-blind"><b>{{ blindCount }}</b><i>盲区</i></div>
      </div>
      <!-- 口径切换：跳转路由后地址栏即固定为该口径，可分别投屏/收藏 -->
      <button class="scr-switch" @click="toggleView"
              :title="isFloor ? '切换到线路口径大屏' : '切换到楼层口径大屏'">
        {{ isFloor ? '线路口径' : '楼层口径' }}
      </button>
      <div class="scr-clock">
        <div class="scr-time">{{ clock }}</div>
        <div class="scr-date">{{ dateCn }}</div>
      </div>
    </header>

    <!-- 顶部统计条（与总览顶部统计卡同口径，深色大屏风格）：
         监测对象 / 今日互联网连通率 / 在线线路 / 今日平均测速（不含专线） / 在线 IP（最近一次扫描） -->
    <div class="scr-stats">
      <div class="scr-stat" v-for="s in summaryCards" :key="s.label" :style="{ '--c': s.color }">
        <div class="ss-label">{{ s.label }}</div>
        <div class="ss-val" :style="{ color: s.color }">
          <CountUp :value="s.num" :decimals="s.decimals || 0" :suffix="s.suffix || ''" />
        </div>
        <div class="ss-sub">{{ s.sub }}</div>
      </div>
    </div>

    <div class="scr-body">
      <main class="scr-main" ref="mainRef">
        <div v-if="!cards.length" class="scr-empty">
          <div class="scr-empty-t">{{ isFloor ? '暂无楼层数据' : '暂无线路数据' }}</div>
          <div class="scr-empty-s">请在管理后台{{ isFloor ? '添加楼层' : '添加线路并绑定探针' }}</div>
        </div>
        <div v-else class="line-grid" :style="gridStyle">
          <!-- 楼层口径卡片：以楼层为单位聚合关联线路状态与本层在线 IP -->
          <template v-if="isFloor">
            <div v-for="f in floors" :key="'f' + f.id" class="line-card" :class="['st-' + (f.status || 'unknown'), { alarming: isAlarming(f) }]">
              <div class="lc-head">
                <div class="lc-name">{{ f.name }}</div>
                <div class="lc-pill"><span class="lc-pill-dot"></span>{{ statusText(f.status) }}</div>
              </div>
              <div class="lc-sub">{{ f.cidr || '未配置网段' }}<template v-if="floorLines(f)"> · {{ floorLines(f) }}</template></div>
              <HeartbeatGrid :cells="hbCellsOf(f)" :end="hbEndOf(f)" />
              <div class="lc-metrics">
                <div class="m">
                  <i>时延</i><b>{{ f.rtt_ms ? f.rtt_ms.toFixed(0) : '—' }}</b><u>ms</u>
                </div>
                <div class="m">
                  <i>丢包</i><b>{{ f.loss_pct != null ? f.loss_pct.toFixed(0) : '—' }}</b><u>%</u>
                </div>
                <div class="m">
                  <i>在线 IP</i><b>{{ f.online ?? '—' }}</b><u>{{ f.online != null ? ' / ' + f.total : '' }}</u>
                </div>
              </div>
              <div v-if="f.online != null && f.total" class="lc-bar" :title="'在线率 ' + Math.round(f.online * 100 / f.total) + '%'">
                <div class="lc-bar-fill bar-ratio" :style="{ width: Math.min(100, Math.round(f.online * 100 / f.total)) + '%' }"></div>
              </div>
            </div>
          </template>
          <template v-else>
          <div v-for="l in lines" :key="l.id" class="line-card" :class="['st-' + (l.status || 'unknown'), { alarming: isAlarming(l) }]">
            <div class="lc-head">
              <div class="lc-name">{{ l.name }}</div>
              <div class="lc-pill"><span class="lc-pill-dot"></span>{{ statusText(l.status) }}</div>
            </div>
            <div class="lc-sub">{{ l.floor_name || '未关联楼层' }}<template v-if="l.public_ip"> · {{ l.public_ip }}</template><template v-if="l.isp"> · {{ l.isp }}</template></div>
            <HeartbeatGrid :cells="hbCellsOf(l)" :end="hbEndOf(l)" />
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
            </div>
          </div>
          </template>
        </div>
      </main>

    </div>

    <footer class="scr-foot">
      <span v-if="unbound.length" class="foot-warn">{{ unbound.length }} 台探针未绑定线路</span>
      <span v-if="stale" class="foot-stale">连接中心失败，自动重试中…</span>
      <span class="foot-right">每 10s 自动刷新 · 上次 {{ lastRefresh }}</span>
    </footer>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import dayjs from 'dayjs'
import { api } from '../api'
import HeartbeatGrid from '../components/HeartbeatGrid.vue'
import CountUp from '../components/CountUp.vue'
import { minuteCells, applyStatusToCurrent } from '../utils'

// 大屏双口径：/screen = 线路（默认）、/screen/floor = 楼层。
// 同一组件按路由切换渲染；地址栏随口径变化，可分别投屏/收藏。
const route = useRoute(), router = useRouter()
const isFloor = computed(() => route.path.startsWith('/screen/floor'))
function toggleView() { router.push(isFloor.value ? '/screen' : '/screen/floor') }

const lines = ref([]), floors = ref([])
const lossThresh = ref(10) // 降级丢包阈值（接口下发，与 P2 告警同口径）
// 心跳格：当前分钟按线路/楼层状态兜底，活动告警期间不显示绿色
function hbCellsOf(x) { return applyStatusToCurrent(minuteCells(x.minutes, lossThresh.value), x.status) }
const unbound = ref([])
const todayStats = ref({ conn_rate: null, avg_speed: null }) // 今日聚合（后端 60s 缓存）
const clock = ref(''), stale = ref(false), lastRefresh = ref('')
const mainRef = ref(null)
const area = ref({ w: 0, h: 0 }) // 卡片区实时宽高（ResizeObserver 测量）
let timer = null, clockTimer = null, ro = null

// KPI 按口径统计：线路口径数线路、楼层口径数楼层
const cards = computed(() => isFloor.value ? floors.value : lines.value)
const okCount = computed(() => cards.value.filter(x => x.status === 'online').length)
const badCount = computed(() => cards.value.filter(x => ['down', 'floor_down', 'degraded', 'speed_low', 'conflict'].includes(x.status)).length)
const blindCount = computed(() => cards.value.filter(x => x.status === 'blind').length)
const globalCls = computed(() => (badCount.value || blindCount.value) ? 'st-down' : 'st-online')

// 顶部统计条：与总览顶部统计卡同口径（监测对象 / 今日连通率 / 在线线路 / 今日平均测速 / 在线 IP），
// 但配色用大屏深色系（绿 #34d399 / 黄 #fbbf24 / 红 #f87171 / 蓝 #60a5fa）。
const summaryCards = computed(() => {
  const total = lines.value.length // 大屏只展示互联网（内网/行业网不上屏），监测对象=宽带线路
  const online = lines.value.filter(x => x.status === 'online').length
  const bad = lines.value.filter(x => ['down', 'floor_down'].includes(x.status)).length
  const deg = lines.value.filter(x => ['degraded', 'speed_low', 'conflict'].includes(x.status)).length
  const blind = lines.value.filter(x => x.status === 'blind').length
  // 在线 IP：按 floor_id 去重（多线路关联同一楼层时不重复计数）
  const byFloor = new Map()
  for (const l of lines.value) {
    for (const f of l.online_ips || []) byFloor.set(f.floor_id, f)
  }
  let ip = 0
  for (const f of byFloor.values()) ip += f.up || 0
  const rate = todayStats.value.conn_rate
  const ratePct = rate == null ? null : rate * 100
  const sp = todayStats.value.avg_speed
  // 用户要求 5 张卡片样式统一（同第 2 张卡）：边框色与数字色都用同一主题绿
  const ACCENT = '#34d399'
  return [
    {
      label: '监测对象', num: total, color: ACCENT,
      sub: `互联网宽带线路 ${lines.value.length} 条`,
    },
    {
      label: '互联网连通率', num: ratePct, decimals: 1, suffix: '%', color: ACCENT,
      sub: ratePct == null ? '今日暂无探测样本' : '今日 00:00 至今 · 全部线路',
    },
    {
      label: '在线线路', num: online, suffix: `/${lines.value.length}`, color: ACCENT,
      sub: `降质 ${deg} · 中断 ${bad} · 盲区 ${blind}`,
    },
    {
      label: '平均测速', num: sp, decimals: sp != null && sp < 100 ? 1 : 0, suffix: ' Mbps', color: ACCENT,
      sub: sp == null ? '今日暂无测速数据' : '今日均值 · 线路 1-11',
    },
    {
      label: '在线 IP', num: ip, color: ACCENT,
      sub: byFloor.size ? `最近一次扫描 · ${byFloor.size} 个楼层` : '尚无扫描数据',
    },
  ]
})

// 一屏铺满（用户要求 2026-11-07：卡片均匀排列不滚动，如 12 条线路 = 3 行 × 4 列）：
// 按卡片数 N 与主区实测宽高选列数——在 round(sqrt(N·W/(H·R))) 的上下取整候选中，
// 选"格子宽高比最接近目标 R"的一个（12 张在 1920×1080 下 = 4 列 × 3 行，14 楼层 = 4×4）；
// 行数 = ceil(N/列)，行列均 1fr 拉伸填满主区，不足的格子留空。
// 格高下限 MINH：卡内五行内容（标题/副题/心跳/两行指标/占比条）随 vmin 等比缩放，
// 实测 ≈22.5vmin，取 23vmin 为下限——候选行列若使单格高度不足（内容会被裁剪），
// 逐次加列减行直到装得下（如 14 楼层在 1920/1366 均为 5 列 × 3 行，12 线路 = 4×3）。
// ≤1100px 窄屏媒体查询单列，回退 auto-fill 允许滚动。
const gridStyle = computed(() => {
  const n = cards.value.length
  const { w, h } = area.value
  if (!n || w < 100 || h < 100 || window.innerWidth <= 1100) return null
  const vmin = Math.min(window.innerWidth, window.innerHeight) / 100
  const R = 1.5         // 卡片目标宽高比
  const MINH = 22.8 * vmin // 单格最低高度（内容实测 22.5vmin + 余量；23 在统计条放大后会差 1px 触发多列重排）
  const gap = 1.6 * vmin // 与 .line-grid 的 1.6vmin 列/行间距一致
  const cellH = (rows) => (h - (rows - 1) * gap) / rows
  const s = Math.sqrt(n * w / (h * R))
  const cand = [...new Set([Math.max(1, Math.floor(s)), Math.max(1, Math.ceil(s))])]
  let cols = cand[0], best = Infinity
  for (const c of cand) {
    // 宽高比越接近 R 越好；末行有空格（不能整除）加 0.25 惩罚——优先能整除的列数，
    // 避免 12 条线路被选成 5 列出现 5+5+2 的末行大空档（去掉右栏后主区变宽时实测）
    const empty = Math.ceil(n / c) * c - n
    const d = Math.abs((w / c) / (h / Math.ceil(n / c)) - R) + (empty ? 0.35 : 0)
    if (d < best || (d === best && c > cols)) { cols = c; best = d }
  }
  let rows = Math.ceil(n / cols)
  while (rows > 1 && cellH(rows) < MINH && cols < n) { cols++; rows = Math.ceil(n / cols) }
  return {
    height: h + 'px',
    gridTemplateColumns: `repeat(${cols}, 1fr)`,
    gridTemplateRows: `repeat(${rows}, 1fr)`,
  }
})
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
// 方格心跳换用全站统一 HeartbeatGrid 组件（按卡片宽度自适应格数，1 格 = 1 分钟），
// x.minutes 为后端逐分钟样本并集 {t,n,f}，minuteCells 转稀疏格子。
// 右端锚点 = 该卡数据最新样本分钟：当前分钟的在途样本尚未落库，按墙钟锚定会让尾格间歇性假灰；
// 探测停滞时以墙钟（150s 前的已完成分钟）兜底，真实盲区仍会逐格变灰（卡片徽章 60s 先行变盲区）。
function hbEndOf(x) {
  const ms = x.minutes || []
  const newest = ms.length ? ms[ms.length - 1].t : 0
  return Math.max(newest, Math.floor(Date.now() / 60000) * 60 - 150)
}
function isAlarming(l) { return ['down', 'floor_down', 'blind'].includes(l.status) }
// 楼层卡副标题里的关联线路名（多线路顿号连接；无线路不显示）
function floorLines(f) { return (f.line_stats || []).map(l => l.name).join('、') }
function statusText(s) {
  return { online: '正常', down: '线路中断', floor_down: '楼层中断', degraded: '降质', speed_low: '带宽不达标', conflict: '数据冲突', blind: '监控盲区', unknown: '待数据' }[s || 'unknown']
}

async function load() {
  try {
    const d = await api.get('/api/screen/overview')
    lines.value = d.lines || []
    floors.value = d.floors || []
    todayStats.value = d.today_stats || { conn_rate: null, avg_speed: null }
    if (d.loss_thresh > 0) lossThresh.value = d.loss_thresh
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
  ro = new ResizeObserver(() => {
    area.value = { w: mainRef.value ? mainRef.value.clientWidth : 0, h: mainRef.value ? mainRef.value.clientHeight : 0 }
  })
  if (mainRef.value) ro.observe(mainRef.value)
})
onUnmounted(() => { clearInterval(timer); clearInterval(clockTimer); if (ro) ro.disconnect() })
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
.scr-titles { display: flex; flex-direction: column; line-height: 1.1; }
.scr-title { font-size: clamp(18px, 2.6vmin, 32px); font-weight: 700; color: #fff; letter-spacing: .05em; }
/* 英文名 + 大屏口径作为副标题（小号灰字，中文主标题在上） */
.scr-en { font-size: clamp(10px, 1.3vmin, 15px); font-weight: 500; color: #7c8fab; letter-spacing: .08em; margin-top: 3px; }
.scr-kpis { display: flex; gap: 1.4vmin; margin-left: auto; }
.kpi {
  display: flex; align-items: baseline; gap: .6vmin; padding: .8vmin 1.8vmin;
  border-radius: 1vmin; background: rgba(148, 163, 184, .07); border: 1px solid rgba(148, 163, 184, .12);
}
.kpi b { font-size: clamp(16px, 2.4vmin, 30px); font-variant-numeric: tabular-nums; }
.kpi i { font-style: normal; font-size: clamp(11px, 1.4vmin, 15px); color: #8fa3bd; }
.kpi-ok b { color: #34d399; } .kpi-bad b { color: #f87171; } .kpi-blind b { color: #a78bfa; }
.scr-clock { text-align: right; }
/* 口径切换按钮（深色 pill，与 KPI 卡同族） */
.scr-switch {
  flex: 0 0 auto; display: flex; align-items: center; gap: .6vmin;
  font-size: clamp(12px, 1.5vmin, 16px); font-weight: 600; color: #b9c8dc;
  background: rgba(148, 163, 184, .08); border: 1px solid rgba(148, 163, 184, .22);
  border-radius: 99px; padding: .8vmin 1.8vmin; cursor: pointer; transition: all .2s;
  font-family: inherit;
}
.scr-switch:hover { color: #fff; border-color: rgba(148, 163, 184, .5); background: rgba(148, 163, 184, .18); }
.scr-time { font-size: clamp(18px, 2.6vmin, 32px); font-weight: 600; color: #e6eef8; font-variant-numeric: tabular-nums; line-height: 1.1; }
.scr-date { font-size: clamp(11px, 1.5vmin, 16px); color: #8fa3bd; }

/* ===== 顶部统计条（与总览统计卡同口径，深色玻璃卡） ===== */
.scr-stats { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); gap: 1.2vmin; padding-top: 1.2vmin; }
.scr-stat {
  background: linear-gradient(180deg, rgba(148, 163, 184, .08), rgba(148, 163, 184, .03));
  border: 1px solid rgba(148, 163, 184, .14);
  border-left: .5vmin solid var(--c, #64748b);
  border-radius: 1.2vmin; padding: .9vmin 1.6vmin;
  display: flex; flex-direction: column; min-width: 0; overflow: hidden;
}
.ss-label { font-size: clamp(11px, 1.5vmin, 16px); color: #8fa3bd; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.ss-val { font-size: clamp(20px, 3vmin, 36px); font-weight: 700; line-height: 1.15; font-variant-numeric: tabular-nums; white-space: nowrap; }
/* 单位/分母用小号灰字，突出主数值（CountUp 内部节点，需 :deep 穿透 scoped） */
.ss-val :deep(.count-unit) { font-size: .55em; font-weight: 600; color: #8fa3bd; margin-left: .3vmin; }
.ss-sub { font-size: clamp(10px, 1.25vmin, 14px); color: #64748b; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

/* ===== 主体 ===== */
.scr-body { flex: 1; display: grid; grid-template-columns: 1fr; gap: 2vmin; padding: 1.8vmin 0; min-height: 0; }
.scr-main { min-height: 0; overflow: hidden; }
/* 兜底布局（窄屏 ≤1100px 或尚未量得主区尺寸时）；正常态由 gridStyle 精确铺满一屏 */
.line-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(300px, 100%), 1fr)); gap: 1.6vmin; align-content: start; }
.line-card {
  --st: #64748b;
  background: linear-gradient(180deg, rgba(148, 163, 184, .08), rgba(148, 163, 184, .03));
  border: 1px solid rgba(148, 163, 184, .14); border-radius: 1.2vmin;
  border-left: .55vmin solid var(--st);
  padding: 1.6vmin 1.8vmin; display: flex; flex-direction: column; gap: .8vmin; min-height: 0; overflow: hidden;
}
.st-online { --st: #34d399; } .st-down { --st: #f87171; } .st-floor_down { --st: #fb7185; }
.st-degraded { --st: #fbbf24; } .st-speed_low { --st: #fb923c; } .st-conflict { --st: #f472b6; }
.st-blind { --st: #a78bfa; } .st-unknown { --st: #64748b; }
.line-card.alarming { animation: pulse 1.6s ease-in-out infinite; }
.st-down, .st-floor_down { background: linear-gradient(180deg, rgba(248, 113, 113, .10), rgba(148, 163, 184, .03)); }
.st-blind { background: linear-gradient(180deg, rgba(167, 139, 250, .10), rgba(148, 163, 184, .03)); }

/* 头部/副题/占比条不可压缩：格高不足时由指标区（flex 收缩）让位，副题绝不允许消失 */
.lc-head { display: flex; align-items: center; justify-content: space-between; gap: 1vmin; flex-shrink: 0; }
.lc-name { font-size: clamp(15px, 2vmin, 24px); font-weight: 700; color: #fff; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.lc-pill {
  flex: 0 0 auto; display: flex; align-items: center; gap: .5vmin;
  font-size: clamp(11px, 1.4vmin, 15px); font-weight: 600; color: var(--st);
  padding: .3vmin 1.1vmin; border-radius: 99px; border: 1px solid color-mix(in srgb, var(--st) 45%, transparent);
  background: color-mix(in srgb, var(--st) 12%, transparent);
}
.lc-pill-dot { width: .8vmin; height: .8vmin; min-width: 6px; min-height: 6px; border-radius: 50%; background: var(--st); }
.lc-sub { font-size: clamp(11px, 1.5vmin, 16px); color: #8fa3bd; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex-shrink: 0; }

/* 指标：弹性占据卡片剩余高度、行内容垂直居中——顶部（标题/副题/心跳）在同行卡片间严格对齐，
   多余空间不会堆在卡片底部 */
.lc-metrics { display: flex; gap: 2.6vmin; flex-wrap: wrap; flex: 1 1 auto; align-content: center; }
.m { display: flex; align-items: baseline; gap: .5vmin; font-variant-numeric: tabular-nums; }
.m i { font-style: normal; font-size: clamp(10px, 1.3vmin, 14px); color: #7c8fab; }
.m b { font-size: clamp(15px, 2vmin, 24px); font-weight: 600; color: #e6eef8; }
.m u { text-decoration: none; font-size: clamp(10px, 1.3vmin, 14px); color: #7c8fab; }
.lc-bar { height: .7vmin; min-height: 4px; border-radius: 99px; background: rgba(148, 163, 184, .15); overflow: hidden; flex-shrink: 0; }
.lc-bar-fill { height: 100%; border-radius: 99px; transition: width .8s; }
/* 楼层卡在线率占比条：纯占比展示（在线率随办公时段波动大，不做好坏着色） */
.bar-ratio { background: #60a5fa; }

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
  .scr-stats { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
</style>
