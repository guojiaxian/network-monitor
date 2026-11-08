<template>
  <ModalBox :show="show" title="修改密码" @close="$emit('close')" @ok="submit">
    <div class="mb-2"><label class="form-label small mb-1">旧密码 <span class="text-danger">*</span></label>
      <input v-model="f.old" type="password" class="form-control form-control-sm" autocomplete="current-password" @keyup.enter="submit" /></div>
    <div class="mb-2"><label class="form-label small mb-1">新密码 <span class="text-danger">*</span>（至少 8 位）</label>
      <input v-model="f.p1" type="password" class="form-control form-control-sm" autocomplete="new-password" @keyup.enter="submit" /></div>
    <div><label class="form-label small mb-1">确认新密码 <span class="text-danger">*</span></label>
      <input v-model="f.p2" type="password" class="form-control form-control-sm" autocomplete="new-password" @keyup.enter="submit" /></div>
    <div v-if="err" class="text-danger small mt-2">{{ err }}</div>
  </ModalBox>
</template>

<script setup>
// 修改自己密码弹窗（顶栏用户菜单入口）：校验旧密码 + 两次新密码一致。
// 成功后其他设备/浏览器的会话被踢下线，当前会话保持登录
import { reactive, ref, watch } from 'vue'
import { api } from '../api'
import ModalBox from './ModalBox.vue'

const props = defineProps({ show: Boolean })
const emit = defineEmits(['close', 'saved'])
const f = reactive({ old: '', p1: '', p2: '' })
const err = ref('')

// 每次打开时清空表单，避免残留上次输入
watch(() => props.show, v => { if (v) { f.old = f.p1 = f.p2 = ''; err.value = '' } })

async function submit() {
  err.value = ''
  if (!f.old) { err.value = '请输入旧密码'; return }
  if ((f.p1 || '').length < 8) { err.value = '新密码至少 8 位'; return }
  if (f.p1 !== f.p2) { err.value = '两次输入的新密码不一致'; return }
  try {
    await api.put('/api/admin/me/password', { old_password: f.old, new_password: f.p1 })
    emit('saved')
    emit('close')
  } catch (e) { err.value = e.message }
}
</script>
