<template>
  <div>
    <h4 class="mb-3">探针管理</h4>
    <div class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">探针版本升级</h6>
        <span class="text-secondary small ms-3">上传新版探针二进制后，可一键让探针自更新（自动下载校验替换并重启）</span>
      </div>
      <div class="row g-2 align-items-end mb-2">
        <div class="col-md-3"><label class="form-label small mb-1">版本号</label><input v-model="rel.version" class="form-control form-control-sm" placeholder="如 0.2" /></div>
        <div class="col-md-4"><label class="form-label small mb-1">二进制文件</label><input type="file" class="form-control form-control-sm" @change="relFile = $event.target.files[0]" /></div>
        <div class="col-md-2"><button class="btn btn-sm btn-outline-primary w-100" :disabled="!rel.version || !relFile" @click="uploadRel">上传发布</button></div>
        <div class="col-md-3">
          <label class="form-label small mb-1">目标版本</label>
          <select v-model.number="selRelease" class="form-select form-select-sm">
            <option v-for="r in releases" :key="r.id" :value="r.id">{{ r.version }}（{{ r.arch }}，{{ (r.size_bytes/1048576).toFixed(1) }}MB）</option>
          </select>
        </div>
      </div>
      <table class="table nw-table table-sm align-middle">
        <thead><tr><th>探针 ID</th><th>主机名</th><th>当前版本</th><th>待升级到</th><th>状态</th><th>升级</th></tr></thead>
        <tbody>
          <tr v-for="p in probes" :key="p.probe.id">
            <td><code>{{ p.probe.id.slice(0,8) }}</code></td>
            <td>{{ p.probe.name || p.probe.hostname }}</td>
            <td>{{ p.probe.version || '—' }}</td>
            <td>{{ p.probe.pending_version || '—' }}</td>
            <td><StatusBadge :status="p.status && p.status.status || 'unknown'" /></td>
            <td>
              <button class="btn btn-sm btn-outline-warning" :disabled="!selRelease" @click="upgrade(p)">升级</button>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-if="upMsg" class="small" :class="upOk ? 'text-success' : 'text-danger'">{{ upMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">SSH 运维凭据批量导入</h6>
        <span class="text-secondary small ms-3">导入后无需再逐台登录探针 VM——测试、重启服务、远程命令均在后台完成</span>
      </div>
      <p class="text-secondary small mb-1">每行：<code>匹配键,SSH主机,用户,密码[,端口]</code>。匹配键 = 探针 ID 前缀或注册主机名；# 开头为注释。密码将以 AES-256-GCM 加密存储。</p>
      <textarea v-model="sshCsv" class="form-control font-monospace" rows="4"
        placeholder="# 例（按注册主机名匹配）&#10;probe-01,10.8.25.10,root,P@ssw0rd,22"></textarea>
      <div class="d-flex gap-2 mt-2 align-items-center">
        <button class="btn btn-sm btn-outline-primary" :disabled="!sshCsv" @click="importSSH">导入</button>
        <span v-if="importSSHMsg" class="small" :class="importSSHOok ? 'text-success' : 'text-danger'">{{ importSSHMsg }}</span>
      </div>
    </div>

    <div class="nw-card mb-3">
      <h6>探针列表</h6>
      <table class="table nw-table table-sm align-middle">
        <thead>
          <tr>
            <th>探针 ID</th><th>名称</th><th>版本</th><th>出口源 IP</th><th>默认网关</th>
            <th>状态</th><th>最后心跳</th><th style="width:130px">分组</th><th style="width:180px">绑定线路</th><th style="width:110px"></th>
          </tr>
        </thead>
        <tbody>
          <template v-for="p in probes" :key="p.probe.id">
          <tr>
            <td><code>{{ p.probe.id.slice(0, 8) }}</code></td>
            <td>
              <span v-if="editingId !== p.probe.id">{{ p.probe.name || p.probe.hostname }}</span>
              <input v-else v-model="editForm.name" class="form-control form-control-sm" style="min-width:120px" placeholder="如 1楼-东配线间" />
            </td>
            <td>{{ p.probe.version || '—' }}</td>
            <td><code>{{ p.probe.source_ip || '—' }}</code></td>
            <td><code>{{ p.probe.gateway || '—' }}</code></td>
            <td><StatusBadge :status="p.status && p.status.status || 'unknown'" /></td>
            <td class="small text-secondary">{{ fmtTime(p.probe.last_seen) }}</td>
            <td>
              <select class="form-select form-select-sm" :value="p.probe.group || 'internal'" @change="setGroup(p, $event.target.value)" title="internal=楼层内网组（绑线路）；external=外网服务器组（探测官网）">
                <option value="internal">内网组</option>
                <option value="external">外网组</option>
              </select>
            </td>
            <td>
              <select v-if="(p.probe.group || 'internal') === 'internal'" class="form-select form-select-sm" :value="p.probe.line_id || ''" @change="assign(p, $event)">
                <option value="">未绑定</option>
                <option v-for="l in lines" :key="l.id" :value="l.id">{{ l.name }}</option>
              </select>
              <span v-else class="small text-secondary">探测官网</span>
            </td>
            <td class="text-end">
              <button class="btn btn-sm btn-outline-secondary me-1" title="名称与 SSH 运维设置" @click="toggleEdit(p)">⚙</button>
              <button class="btn btn-sm btn-outline-danger" @click="del(p)">删除</button>
            </td>
          </tr>
          <tr v-if="editingId === p.probe.id">
            <td :colspan="10" class="bg-light">
              <div class="row g-2 align-items-end py-1">
                <div class="col-md-2"><label class="form-label small mb-1">探针名称</label><input v-model="editForm.name" class="form-control form-control-sm" /></div>
                <div class="col-md-2"><label class="form-label small mb-1">SSH 主机（管理 IP）</label><input v-model="editForm.ssh_host" class="form-control form-control-sm" placeholder="10.8.25.10" /></div>
                <div class="col-md-1"><label class="form-label small mb-1">端口</label><input v-model.number="editForm.ssh_port" type="number" class="form-control form-control-sm" /></div>
                <div class="col-md-1"><label class="form-label small mb-1">用户</label><input v-model="editForm.ssh_user" class="form-control form-control-sm" placeholder="root" /></div>
                <div class="col-md-2"><label class="form-label small mb-1">密码（留空=不改）</label><input v-model="editForm.ssh_pass" type="password" class="form-control form-control-sm" autocomplete="new-password" /></div>
                <div class="col-md-4 text-end">
                  <router-link :to="`/probes/${p.probe.id}/term`" class="btn btn-sm btn-dark me-1" title="网页终端">🖥 终端</router-link>
                  <button class="btn btn-sm btn-primary me-1" @click="saveMeta(p)">保存</button>
                  <button class="btn btn-sm btn-outline-success me-1" :disabled="!p.probe.ssh_configured" @click="sshOp(p, 'test')">测试连接</button>
                  <button class="btn btn-sm btn-outline-warning me-1" :disabled="!p.probe.ssh_configured" @click="sshOp(p, 'restart-agent')">重启探针服务</button>
                  <button class="btn btn-sm btn-outline-dark" :disabled="!p.probe.ssh_configured" @click="sshExec(p)">远程命令</button>
                </div>
                <div class="col-12" v-if="opMsg[p.probe.id]" :class="opOk[p.probe.id] ? 'text-success' : 'text-danger'" style="white-space:pre-wrap;font-size:12px">{{ opMsg[p.probe.id] }}</div>
              </div>
            </td>
          </tr>
          </template>
          <tr v-if="!probes.length">
            <td colspan="10" class="text-secondary py-4 text-center">
              暂无探针注册。请在楼层 VM 上执行：<code>deploy/install-agent.sh http://&lt;中心IP&gt;:8080 &lt;注册令牌&gt;</code>
            </td>
          </tr>
        </tbody>
      </table>
      <p class="text-secondary small mb-0">
        新探针以注册令牌自动注册后出现在这里；请将其绑定到对应线路，总览页即按线路呈现状态。
      </p>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import dayjs from 'dayjs'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge.vue'

const probes = ref([]), lines = ref([])
const releases = ref([]), rel = ref({ version: '' }), relFile = ref(null), selRelease = ref(0)
const upMsg = ref(''), upOk = ref(false)
const editingId = ref(''), editForm = ref({})
const opMsg = ref({}), opOk = ref({})
const sshCsv = ref(''), importSSHMsg = ref(''), importSSHOok = ref(false)

function fmtTime(t) { return t ? dayjs(t).format('MM-DD HH:mm:ss') : '—' }

function toggleEdit(p) {
  if (editingId.value === p.probe.id) { editingId.value = ''; return }
  editingId.value = p.probe.id
  editForm.value = {
    name: p.probe.name || p.probe.hostname || '',
    ssh_host: p.probe.ssh_host || '', ssh_port: p.probe.ssh_port || 22,
    ssh_user: p.probe.ssh_user || 'root', ssh_pass: '',
  }
}

async function saveMeta(p) {
  try {
    const body = { name: editForm.value.name, ssh_host: editForm.value.ssh_host,
      ssh_port: editForm.value.ssh_port, ssh_user: editForm.value.ssh_user }
    if (editForm.value.ssh_pass) body.ssh_pass = editForm.value.ssh_pass
    await api.put(`/api/admin/probes/${p.probe.id}/meta`, body)
    editingId.value = ''
    await load()
  } catch (e) { alert(e.message) }
}

async function sshOp(p, op) {
  opMsg.value = { ...opMsg.value, [p.probe.id]: '执行中…' }
  try {
    const d = await api.post(`/api/admin/probes/${p.probe.id}/ssh/${op}`, {})
    opMsg.value = { ...opMsg.value, [p.probe.id]: (d.output || '').trim() || '成功' }
    opOk.value = { ...opOk.value, [p.probe.id]: true }
  } catch (e) {
    opMsg.value = { ...opMsg.value, [p.probe.id]: e.message }
    opOk.value = { ...opOk.value, [p.probe.id]: false }
  }
}

async function sshExec(p) {
  const cmd = prompt('输入要在该探针上执行的命令（管理员操作，将记录审计日志）：')
  if (!cmd) return
  opMsg.value = { ...opMsg.value, [p.probe.id]: '执行中…' }
  try {
    const d = await api.post(`/api/admin/probes/${p.probe.id}/ssh/exec`, { command: cmd })
    opMsg.value = { ...opMsg.value, [p.probe.id]: (d.output || '').trim() || '（无输出）' }
    opOk.value = { ...opOk.value, [p.probe.id]: true }
  } catch (e) {
    opMsg.value = { ...opMsg.value, [p.probe.id]: e.message }
    opOk.value = { ...opOk.value, [p.probe.id]: false }
  }
}

async function setGroup(p, group) {
  try {
    await api.post(`/api/admin/probes/${p.probe.id}/group`, { group })
    await load()
  } catch (e) { alert(e.message) }
}

async function importSSH() {
  importSSHMsg.value = ''; importSSHOok.value = false
  try {
    const r = await fetch('/api/admin/probes/ssh/import', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'text/csv' }, body: sshCsv.value,
    })
    const d = await r.json()
    if (!r.ok) throw new Error(d.error || '导入失败')
    importSSHOok.value = true
    importSSHMsg.value = `成功导入 ${d.imported} 台` + (d.skipped && d.skipped.length ? `；跳过：${d.skipped.join('；')}` : '')
    await load()
  } catch (e) { importSSHMsg.value = e.message }
}

async function load() {
  ;[probes.value, lines.value, releases.value] = await Promise.all([
    api.get('/api/admin/probes'), api.get('/api/admin/lines'), api.get('/api/admin/agent-releases'),
  ])
}

async function uploadRel() {
  upMsg.value = ''; upOk.value = false
  try {
    const fd = new FormData()
    fd.append('file', relFile.value)
    fd.append('version', rel.value.version)
    const r = await fetch('/api/admin/agent-releases', { method: 'POST', credentials: 'same-origin', body: fd })
    const d = await r.json()
    if (!r.ok) throw new Error(d.error || '上传失败')
    upMsg.value = `发布 ${d.version} 上传成功（sha256 ${d.sha256.slice(0, 12)}…）`; upOk.value = true
    rel.value = { version: '' }; relFile.value = null
    await load()
  } catch (e) { upMsg.value = e.message }
}

async function upgrade(p) {
  if (!confirm(`将探针 ${p.probe.id.slice(0, 8)}（当前 ${p.probe.version || '—'}）升级到所选版本？`)) return
  try {
    await api.post('/api/admin/probes/upgrade', { probe_ids: [p.probe.id], release_id: selRelease.value })
    upMsg.value = '升级任务已下发，探针将在 30 秒内开始自更新'; upOk.value = true
    setTimeout(load, 5000)
  } catch (e) { upMsg.value = e.message }
}

async function assign(p, ev) {
  const v = ev.target.value
  await api.post(`/api/admin/probes/${p.probe.id}/assign`, { line_id: v ? Number(v) : null })
  await load()
}

async function del(p) {
  if (!confirm(`删除探针 ${p.probe.id.slice(0, 8)}？该探针需重新注册才能接入。`)) return
  await api.del('/api/admin/probes/' + p.probe.id)
  await load()
}

onMounted(load)
</script>
