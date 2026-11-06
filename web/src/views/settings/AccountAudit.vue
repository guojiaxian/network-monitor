<template>
  <div>
    <div class="nw-card mb-3">
      <h6>用户管理</h6>
      <table class="table nw-table table-sm align-middle mb-2">
        <thead><tr><th>用户名</th><th>角色</th><th style="width:100px"></th></tr></thead>
        <tbody>
          <tr v-for="u in users" :key="u.id">
            <td>{{ u.username }}</td>
            <td><span class="badge" :class="u.role === 'admin' ? 'bg-dark' : 'bg-secondary'">{{ u.role === 'admin' ? '管理员' : '观察者' }}</span></td>
            <td class="text-end"><button class="btn btn-sm btn-outline-danger" @click="delUser(u)">删除</button></td>
          </tr>
        </tbody>
      </table>
      <div class="d-flex align-items-center mb-1">
        <span class="text-secondary small">角色决定权限：管理员可执行全部写操作，观察者只读。</span>
        <button class="btn btn-sm btn-primary ms-auto" @click="openUser()">＋ 新增用户</button>
      </div>
      <div v-if="userMsg" class="small" :class="userOk ? 'text-success' : 'text-danger'">{{ userMsg }}</div>
    </div>

    <!-- 新增用户弹窗 -->
    <ModalBox :show="nuModal.show" title="新增用户" @close="nuModal.show = false" @ok="addUser">
      <div class="mb-2"><label class="form-label small mb-1">用户名 <span class="text-danger">*</span></label>
        <input v-model="nuModal.username" class="form-control form-control-sm" placeholder="如 zhiban01" /></div>
      <div class="mb-2"><label class="form-label small mb-1">密码 <span class="text-danger">*</span>（≥8 位）</label>
        <input v-model="nuModal.password" type="password" class="form-control form-control-sm" autocomplete="new-password" /></div>
      <div><label class="form-label small mb-1">角色</label>
        <select v-model="nuModal.role" class="form-select form-select-sm">
          <option value="viewer">观察者（只读）</option>
          <option value="admin">管理员</option>
        </select></div>
      <div v-if="nuModal.err" class="text-danger small mt-2">{{ nuModal.err }}</div>
    </ModalBox>

    <div class="nw-card mb-3">
      <h6>操作审计日志（最近 100 条）</h6>
      <table class="table nw-table table-sm table-fixed">
        <thead><tr><th style="width:14%">时间</th><th style="width:10%">用户</th><th>操作</th><th style="width:30%">结果</th></tr></thead>
        <tbody>
          <tr v-for="a in audits" :key="a.id">
            <td class="small text-secondary">{{ fmt(a.created_at) }}</td>
            <td>{{ a.username }}</td>
            <td><code class="small">{{ a.action }}</code></td>
            <td class="small" :style="{ color: a.detail.includes('status=2') ? '#05c96b' : '#dc3545' }">{{ a.detail }}</td>
          </tr>
          <tr v-if="!audits.length"><td colspan="4" class="text-secondary small">暂无记录（变更操作会自动记录）</td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api } from '../../api'
import ModalBox from '../../components/ModalBox.vue'
import { fmtTime } from '../../utils'

const users = ref([]), userMsg = ref(''), userOk = ref(false)
const audits = ref([])
const nuModal = reactive({ show: false, username: '', password: '', role: 'viewer', err: '' })

function openUser() { Object.assign(nuModal, { show: true, username: '', password: '', role: 'viewer', err: '' }) }

const fmt = fmtTime

async function load() {
  ;[users.value, audits.value] = await Promise.all([
    api.get('/api/admin/users'), api.get('/api/admin/audit?limit=100'),
  ])
}

async function addUser() {
  nuModal.err = ''
  if (!nuModal.username.trim()) { nuModal.err = '请填写用户名'; return }
  if ((nuModal.password || '').length < 8) { nuModal.err = '密码至少 8 位'; return }
  try {
    await api.post('/api/admin/users', { username: nuModal.username.trim(), password: nuModal.password, role: nuModal.role })
    nuModal.show = false
    userMsg.value = '已创建'; userOk.value = true
    users.value = await api.get('/api/admin/users')
  } catch (e) { nuModal.err = e.message }
}

async function delUser(u) {
  if (!confirm(`删除用户 ${u.username}？`)) return
  try {
    await api.del('/api/admin/users/' + u.id)
    users.value = await api.get('/api/admin/users')
  } catch (e) { alert(e.message) }
}

onMounted(load)
</script>
