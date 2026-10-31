<template>
  <div class="hb" :title="title">
    <i v-for="(b, i) in beats" :key="i"
       :class="b ? 'st-online' : 'st-down'"
       :style="{ '--st': b ? '#05c96b' : '#dc3545' }"></i>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  beats: { type: Array, default: () => [] }, // true=成功 false=失败
})

const okCount = computed(() => props.beats.filter(Boolean).length)
const title = computed(() => props.beats.length ? `最近 ${props.beats.length} 次探测：成功 ${okCount.value} 次` : '暂无探测数据')
</script>
