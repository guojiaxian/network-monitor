<template>
  <div>
    <div ref="box" class="hbg" :style="{ '--sz': size + 'px', '--gap': gap + 'px' }">
      <i v-for="(c, i) in bars" :key="c.t" :class="'hbg-' + c.s" :title="axis ? undefined : c.title"
         @mouseenter="onHover(i)" @mouseleave="onHover(-1)"></i>
      <div v-if="axis && hoverIdx >= 0 && bars[hoverIdx]" class="hbg-tip" :style="tipStyle">{{ bars[hoverIdx].title }}</div>
    </div>
    <div v-if="axis" class="hbg-axis" aria-hidden="true">
      <span v-for="(c, i) in bars" :key="i" class="hbg-tick"><b>{{ tickLabel(i) }}</b></span>
    </div>
    <div v-if="legend" class="d-flex flex-wrap gap-3 mt-2 small text-secondary">
      <span class="d-inline-flex align-items-center gap-1"><i class="hbg-dot hbg-ok"></i>全部成功</span>
      <span class="d-inline-flex align-items-center gap-1"><i class="hbg-dot hbg-partial"></i>存在失败已恢复</span>
      <span class="d-inline-flex align-items-center gap-1"><i class="hbg-dot hbg-degraded"></i>降级（丢包超阈值）</span>
      <span class="d-inline-flex align-items-center gap-1"><i class="hbg-dot hbg-fail"></i>全部失败</span>
      <span class="d-inline-flex align-items-center gap-1"><i class="hbg-dot hbg-nodata"></i>暂停探测</span>
    </div>
  </div>
</template>

<script setup>
// 方格心跳图（全站统一规则）：1 格 = 1 分钟的探测汇总，正方形格子单行排列；
// 格子数按容器宽度自适应，放不下时最左边的格子移出（滑动窗口，右端最新）。
// 状态语义（用户定义 2026-11-07；degraded 为 2026-11-07 晚新增）：
//   深绿 ok      窗口内所有探测全部成功
//   橙 partial 窗口内至少一次未连通但后续恢复（不再表示"部分目标成功"）
//   黄 degraded 降级：该分钟按降质告警同款滚动窗口的各目标丢包率下中位数超阈值
//              （与 P2 告警同口径——单目标全断不标黄也不告警）
//   红   fail    窗口内所有探测均失败
//   灰   nodata  该分钟无任何探测（暂停探测/盲区）
// "一次探测失败" = 该次所有目标都失败（只要有一个目标成功即视为联通），
// 由数据方按分钟聚合判定（探针引擎逐分钟样本计数 / VM avg_over_time 逐分钟均值）。
// axis（仅详情页开启）：方格下方时间轴刻度 + 悬停浮出"时间区间 · 状态"提示。
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import dayjs from 'dayjs'

const props = defineProps({
  cells: { type: Array, default: () => [] }, // 稀疏格子 [{t(unix秒,对齐60), s(ok|partial|degraded|fail)}] 时间升序
  size: { type: Number, default: 15 },       // 格子边长 px（正方形）
  gap: { type: Number, default: 3 },
  end: { type: Number, default: 0 },         // 右端锚点（unix 秒，缺省当前时刻；VM 数据传 查询终点-90s 避免在途分钟灰格）
  legend: { type: Boolean, default: false }, // 图例行（详情页开启）
  axis: { type: Boolean, default: false },   // 时间轴 + 悬停浮层（详情页开启）
})

const LABEL = { ok: '全部成功', partial: '存在失败已恢复', degraded: '降级（丢包超阈值）', fail: '全部失败', nodata: '暂停探测' }

const box = ref(null)
const cap = ref(40)
const hoverIdx = ref(-1)
let ro = null
function measure() {
  const w = box.value ? box.value.clientWidth : 0
  if (w > 0) cap.value = Math.max(1, Math.floor((w + props.gap) / (props.size + props.gap)))
}
onMounted(() => {
  measure()
  ro = new ResizeObserver(measure)
  if (box.value) ro.observe(box.value)
})
onBeforeUnmount(() => ro && ro.disconnect())

const index = computed(() => {
  const m = new Map()
  for (const c of props.cells) m.set(c.t, c.s)
  return m
})
const bars = computed(() => {
  const endMin = Math.floor((props.end || Date.now() / 1000) / 60)
  const out = []
  for (let t = (endMin - cap.value + 1) * 60; t <= endMin * 60; t += 60) {
    const s = index.value.get(t) || 'nodata'
    const st = dayjs(t * 1000)
    out.push({ t, s, title: `${st.format('HH:mm')}–${st.add(1, 'minute').format('HH:mm')} · ${LABEL[s]}` })
  }
  return out
})

// 时间轴刻度：约 7 个刻度均匀取样，标签绝对定位在对应格子下方（可越出格宽不重叠）
const tickLabel = (i) => (i % Math.ceil(bars.value.length / 7) === 0 ? dayjs(bars.value[i].t * 1000).format('HH:mm') : '')

// 悬停浮层：跟随格子水平位置，左右夹在容器内
const tipStyle = computed(() => {
  const boxW = box.value ? box.value.clientWidth : 0
  const x = hoverIdx.value * (props.size + props.gap)
  return { left: Math.max(0, Math.min(x - 30, boxW - 150)) + 'px' }
})
const onHover = (i) => { hoverIdx.value = i }
</script>
