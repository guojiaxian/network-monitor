<template>
  <div class="scr">
    <header class="scr-head">
      <div class="scr-title">🛰️ NetWatch · 出口线路监控大屏</div>
      <div class="scr-stats">
        <span class="ok">正常 {{ okCount }}</span>
        <span class="bad">异常 {{ badCount }}</span>
        <span class="blind">盲区 {{ blindCount }}</span>
        <span>/ 共 {{ lines.length }} 条</span>
      </div>
      <div class="scr-clock">{{ clock }}</div>
    </header>

    <div class="scr-grid" :style="{ '--cols': cols }">
      <div v-for="l in lines" :key="l.id" class="scr-cell" :class="'st-' + (l.status || 'unknown')">
        <div class="scr-line">{{ l.name }}</div>
        <div class="scr-floor">{{ l.floor_name || '未关联楼层' }} · {{ l.public_ip || '—' }}</div>
        <div class="scr-status">{{ statusText(l.status) }}</div>
        <div class="scr-metrics">
          <span>时延 {{ rttOf(l) }}<i>ms</i></span>
          <span>丢包 {{ lossOf(l) }}<i>%</i></span>
          <span>测速 {{ speedOf(l) }}</span>
        </div>
      </div>
    </div>

    <footer class="scr-foot">
      <span class="scr-alarm-title">当前告警</span>
      <div class="scr-alarms">
        <div v-for="a in alerts" :key="a.id" class="scr-alarm">
          <b :class="'sev-' + a.severity">{{ a.severity }}</b>
          {{ a.title }}
          <em>{{ since(a.starts_at) }}</em>
        </div>
        <div v-if="!alerts.length" class="scr-alarm none">— 暂无告警 —</div>
      </div>
    </footer>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import dayjs from 'dayjs'
import { api } from '../api'

const lines = ref([]), alerts = ref([])
const clock = ref('')
let timer = null, clockTimer = null

const cols = computed(() => {
  const n = lines.value.length || 12
  if (n <= 4) return 4
  if (n <= 8) return 4
  return 4
})
const okCount = computed(() => lines.value.filter(l => l.status === 'online').length)
const badCount = computed(() => lines.value.filter(l => ['down', 'floor_down', 'degraded', 'speed_low'].includes(l.status)).length)
const blindCount = computed(() => lines.value.filter(l => l.status === 'blind').length)

function probeOf(l) { return (l.statuses && l.statuses[0]) || {} }
function rttOf(l) { const p = probeOf(l); return p.rtt_ms ? p.rtt_ms.toFixed(0) : '—' }
function lossOf(l) { const p = probeOf(l); return p.loss_pct != null ? p.loss_pct.toFixed(0) : '—' }
function speedOf(l) {
  const p = probeOf(l)
  if (!p.speed_down_mbps) return '—'
  return p.speed_down_mbps >= 100 ? Math.round(p.speed_down_mbps) + 'M' : p.speed_down_mbps.toFixed(1) + 'M'
}
function statusText(s) {
  return { online: '● 正常', down: '● 线路中断', floor_down: '● 楼层中断', degraded: '● 降质', speed_low: '● 带宽不达标', conflict: '● 数据冲突', blind: '● 监控盲区', unknown: '● 未知' }[s || 'unknown']
}
function since(t) { return dayjs(t).format('HH:mm') + ' 起' }

async function load() {
  try {
    const d = await api.get('/api/screen/overview')
    lines.value = d.lines || []
    alerts.value = d.alerts || []
  } catch { /* 大屏静默重试 */ }
}

onMounted(() => {
  load()
  timer = setInterval(load, 10000)
  clockTimer = setInterval(() => { clock.value = dayjs().format('YYYY-MM-DD HH:mm:ss dddd') }, 1000)
  clock.value = dayjs().format('YYYY-MM-DD HH:mm:ss dddd')
})
onUnmounted(() => { clearInterval(timer); clearInterval(clockTimer) })
</script>

<style scoped>
.scr {
  min-height: 100vh; background: #0d1421; color: #dfe6ef;
  display: flex; flex-direction: column; padding: 2vmin 3vmin; gap: 2vmin;
  font-family: -apple-system, 'Segoe UI', 'Microsoft YaHei', sans-serif;
}
.scr-head { display: flex; align-items: baseline; gap: 3vmin; }
.scr-title { font-size: clamp(18px, 2.6vmin, 34px); font-weight: 700; color: #fff; }
.scr-stats { font-size: clamp(13px, 1.8vmin, 22px); display: flex; gap: 2vmin; }
.scr-stats .ok { color: #2ecc71; } .scr-stats .bad { color: #e74c3c; } .scr-stats .blind { color: #a78bfa; }
.scr-clock { margin-left: auto; font-size: clamp(13px, 1.8vmin, 22px); color: #8fa3bd; font-variant-numeric: tabular-nums; }

.scr-grid {
  flex: 1; display: grid; gap: 1.6vmin;
  grid-template-columns: repeat(var(--cols), 1fr);
  grid-auto-rows: 1fr;
}
.scr-cell {
  background: #141d2e; border: 1px solid #223047; border-radius: 1.2vmin;
  border-left: .8vmin solid var(--st, #4b5a70);
  padding: 1.6vmin 2vmin; display: flex; flex-direction: column; justify-content: center; gap: .6vmin;
}
.st-online     { --st: #2ecc71; } .st-down       { --st: #e74c3c; }
.st-floor_down { --st: #e74c3c; } .st-degraded   { --st: #f1c40f; }
.st-speed_low  { --st: #e67e22; } .st-blind      { --st: #a78bfa; }
.st-unknown    { --st: #4b5a70; }
.scr-cell:has(.scr-status) .scr-status { color: var(--st); }
.scr-line { font-size: clamp(15px, 2.2vmin, 30px); font-weight: 700; color: #fff; }
.scr-floor { font-size: clamp(11px, 1.5vmin, 18px); color: #8fa3bd; }
.scr-status { font-size: clamp(13px, 1.9vmin, 24px); font-weight: 600; }
.scr-metrics { display: flex; gap: 2.4vmin; font-size: clamp(12px, 1.7vmin, 22px); color: #c3cedd; font-variant-numeric: tabular-nums; }
.scr-metrics i { font-style: normal; color: #6d7f95; font-size: .8em; margin-left: .2em; }

.scr-foot { display: flex; align-items: center; gap: 2vmin; min-height: 5vmin; }
.scr-alarm-title { color: #8fa3bd; font-size: clamp(12px, 1.6vmin, 18px); flex: 0 0 auto; }
.scr-alarms { display: flex; gap: 3vmin; overflow: hidden; }
.scr-alarm { font-size: clamp(12px, 1.7vmin, 20px); white-space: nowrap; }
.scr-alarm em { font-style: normal; color: #6d7f95; margin-left: 1vmin; }
.scr-alarm.none { color: #55677e; }
.sev-P0 { color: #e74c3c; } .sev-P1 { color: #e67e22; } .sev-P2 { color: #f1c40f; }
</style>
