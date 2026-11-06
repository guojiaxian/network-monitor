<template>
  <div class="d-flex flex-wrap gap-1 align-items-center">
    <span v-for="(t, i) in items" :key="t" class="badge bg-light text-dark border d-inline-flex align-items-center gap-1 py-1">
      <span class="font-monospace">{{ t }}</span>
      <a href="javascript:void(0)" class="text-danger text-decoration-none fw-bold" title="删除" @click="$emit('remove', items, i)">×</a>
    </span>
    <button class="btn btn-sm btn-outline-primary py-0" @click="$emit('add', addTitle, addPlaceholder, items)">＋ 新增</button>
  </div>
</template>

<script setup>
// 探测目标列表小控件：badge 列表 + ＋新增 按钮（emit add 触发父级弹窗）
defineProps({
  items: { type: Array, default: () => [] },
  addTitle: String,
  addPlaceholder: String,
})
defineEmits(['add', 'remove'])
</script>
