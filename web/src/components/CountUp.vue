<template>
  <span v-if="value == null" class="count-none">—</span>
  <span v-else class="count-num">{{ shown }}<span v-if="suffix" class="count-unit">{{ suffix }}</span></span>
</template>

<script setup>
// 数字滚动动画（顶部统计卡用）：
//   - 首次挂载（含页面刷新）：从 0 涨到目标值；
//   - 之后数值有变化：从当前显示值平滑过渡到新值（涨/跌都动）；
//   - 数值没变化（10s 轮询但数据未变）：不重播，避免每次刷新都归零。
// 动画用整数/小数格式化（decimals 位），tabular-nums 由样式保证，数字跳动时不抖动宽度。
import { ref, watch, onBeforeUnmount } from 'vue'

const props = defineProps({
  value: { type: Number, default: null },  // null = 无数据显示 —
  decimals: { type: Number, default: 0 },
  suffix: { type: String, default: '' },
  duration: { type: Number, default: 700 },
})

const shown = ref('—')
let raf = 0
let reduceMotion = false
try { reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches } catch (e) { /* 无 matchMedia 时按普通动画 */ }

function fmt(v) { return v.toFixed(props.decimals) }

function play() {
  cancelAnimationFrame(raf)
  if (props.value == null || !isFinite(props.value)) { shown.value = '—'; return }
  const to = props.value
  if (reduceMotion) { shown.value = fmt(to); return }
  // 首次挂载时 shown 为 '—' → 从 0 起涨；之后从当前显示值过渡
  const from = parseFloat(shown.value)
  const begin = Number.isFinite(from) ? from : 0
  const t0 = performance.now()
  const step = (t) => {
    // rAF 时间戳可能略早于调度时刻（帧开始时间），需同时钳下界，否则首帧会出现负数
    const p = Math.min(1, Math.max(0, (t - t0) / props.duration))
    const eased = 1 - Math.pow(1 - p, 3) // easeOutCubic：先快后慢，收尾自然
    shown.value = fmt(begin + (to - begin) * eased)
    if (p < 1) raf = requestAnimationFrame(step)
  }
  raf = requestAnimationFrame(step)
}

watch(() => props.value, play, { immediate: true })
onBeforeUnmount(() => cancelAnimationFrame(raf))
</script>
