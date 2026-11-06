<template>
  <teleport to="body">
    <div v-if="show" class="modal-backdrop fade show"></div>
    <div v-if="show" class="modal fade show d-block" tabindex="-1" style="overflow-y:auto" @click.self="$emit('close')">
      <div class="modal-dialog modal-dialog-centered" :class="wide ? 'modal-lg' : ''">
        <div class="modal-content">
          <div class="modal-header py-2">
            <h6 class="modal-title">{{ title }}</h6>
            <button type="button" class="btn-close" @click="$emit('close')"></button>
          </div>
          <div class="modal-body"><slot /></div>
          <div class="modal-footer py-2">
            <slot name="footer">
              <button class="btn btn-sm btn-outline-secondary" @click="$emit('close')">取消</button>
              <button class="btn btn-sm btn-primary" @click="$emit('ok')">{{ okText || '确定' }}</button>
            </slot>
          </div>
        </div>
      </div>
    </div>
  </teleport>
</template>

<script setup>
// 统一的"点击新增 → 弹窗 → 确定"交互组件（Bootstrap 5 样式，纯 v-if 渲染，无 JS 依赖）
defineProps({ show: Boolean, title: String, okText: String, wide: Boolean })
defineEmits(['close', 'ok'])
</script>
