<template>
  <span class="badge badge-nw st-dot-wrap d-inline-flex align-items-center gap-2" :class="cls">
    <span class="st-dot" :class="{ pulse: isBad }"></span>{{ text }}
  </span>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({ status: { type: String, default: 'unknown' } })

const map = {
  online:     ['st-online', '正常'],
  down:       ['st-down', '线路中断'],
  floor_down: ['st-floor_down', '楼层中断'],
  degraded:   ['st-degraded', '降质'],
  speed_low:  ['st-speed_low', '带宽不达标'],
  conflict:   ['st-conflict', '数据冲突'],
  blind:      ['st-blind', '监控盲区'],
  unknown:    ['st-unknown', '未知'],
}
const cls = computed(() => (map[props.status] || map.unknown)[0])
const text = computed(() => (map[props.status] || map.unknown)[1])
const isBad = computed(() => ['down', 'floor_down', 'blind'].includes(props.status))
</script>
