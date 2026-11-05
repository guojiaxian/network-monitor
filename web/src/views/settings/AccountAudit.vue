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
      <div class="row g-2 align-items-end">
        <div class="col-md-3"><input v-model="nu.username" class="form-control form-control-sm" placeholder="用户名" /></div>
        <div class="col-md-3"><input v-model="nu.password" type="password" class="form-control form-control-sm" placeholder="密码（≥8位）" /></div>
        <div class="col-md-2">
          <select v-model="nu.role" class="form-select form-select-sm">
            <option value="viewer">观察者（只读）</option>
            <option value="admin">管理员</option>
          </select>
        </div>
        <div class="col-md-2"><button class="btn btn-sm btn-primary w-100" @click="addUser">新增用户</button></div>
      </div>
      <div v-if="userMsg" class="small mt-2" :class="userOk ? 'text-success' : 'text-danger'">{{ userMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>操作审计日志（最近 100 条）</h6>
      <table class="table nw-table table-sm">
        <thead><tr><th style="width:160px">时间</th><th style="width:120px">用户</th><th>操作</th><th>结果</th></tr></thead>
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
import { ref, onMounted } from 'vue'
import { api } from '../../api'
import { fmtTime } from '../../utils'

const users = ref([]), nu = ref({ username: '', password: '', role: 'viewer' }), userMsg = ref(''), userOk = ref(false)
const audits = ref([])

const fmt = fmtTime

async function load() {
  ;[users.value, audits.value] = await Promise.all([
    api.get('/api/admin/users'), api.get('/api/admin/audit?limit=100'),
  ])
}

async function addUser() {
  userMsg.value = ''; userOk.value = false
  try {
    await api.post('/api/admin/users', { ...nu.value })
    userMsg.value = '已创建'; userOk.value = true
    nu.value = { username: '', password: '', role: 'viewer' }
    users.value = await api.get('/api/admin/users')
  } catch (e) { userMsg.value = e.message }
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
