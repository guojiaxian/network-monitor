<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <h4 class="mb-0">探针管理</h4>
      <span class="text-secondary small ms-3">绑定线路、分组、SSH 运维与版本升级；探针健康状态在 <router-link to="/probes">探针状态</router-link> 页查看</span>
    </div>

    <div class="nw-card mb-3">
      <h6>探针列表</h6>
      <table class="table nw-table table-sm align-middle">
        <thead>
          <tr>
            <th>探针 ID</th><th>名称</th><th>分组</th><th>绑定线路</th><th>版本</th><th>状态</th><th style="width:90px"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in probes" :key="p.probe.id">
            <td><code>{{ p.probe.id.slice(0, 8) }}</code></td>
            <td>{{ p.probe.name || p.probe.hostname }}</td>
            <td><span class="badge" :class="(p.probe.group || 'internal') === 'external' ? 'bg-success' : 'bg-primary'">{{ (p.probe.group || 'internal') === 'external' ? '外网组' : '内网组' }}</span></td>
            <td>{{ lineName(p) }}</td>
            <td>{{ p.probe.version || '—' }}<span v-if="p.probe.pending_version" class="text-warning small"> → {{ p.probe.pending_version }}</span></td>
            <td><StatusBadge :status="p.status && p.status.status || 'unknown'" /></td>
            <td class="text-end">
              <button class="btn btn-sm btn-outline-secondary" @click="openOps(p)">⚙ 编辑 / 运维</button>
            </td>
          </tr>
          <tr v-if="!probes.length">
            <td colspan="7" class="text-secondary py-4 text-center">
              暂无探针注册。请在楼层 VM 上执行：<code>deploy/install-agent.sh http://&lt;中心IP&gt; &lt;注册令牌&gt;</code>
            </td>
          </tr>
        </tbody>
      </table>
      <p class="text-secondary small mb-0">
        新探针以注册令牌自动注册后出现在这里；点击「⚙ 编辑 / 运维」绑定线路、配置 SSH 凭据或执行远程操作。
      </p>
    </div>

    <!-- 探针编辑/运维弹窗 -->
    <ModalBox :show="pModal.show" :title="`探针编辑 / 运维 — ${pModal.disp}`" wide @close="pModal.show = false" @ok="saveMeta">
      <div class="row g-2">
        <div class="col-md-6"><label class="form-label small mb-1">探针名称</label><input v-model="pModal.name" class="form-control form-control-sm" /></div>
        <div class="col-md-3">
          <label class="form-label small mb-1">分组</label>
          <select v-model="pModal.group" class="form-select form-select-sm" title="internal=楼层内网组（绑线路）；external=外网服务器组（探测官网）">
            <option value="internal">内网组</option>
            <option value="external">外网组</option>
          </select>
        </div>
        <div class="col-md-3" v-if="pModal.group === 'internal'">
          <label class="form-label small mb-1">绑定线路</label>
          <select v-model.number="pModal.line_id" class="form-select form-select-sm">
            <option :value="0">未绑定</option>
            <option v-for="l in lines" :key="l.id" :value="l.id">{{ l.name }}</option>
          </select>
        </div>
        <div class="col-md-12"><hr class="my-1" /></div>
        <div class="col-md-5"><label class="form-label small mb-1">SSH 主机（管理 IP）</label><input v-model="pModal.ssh_host" class="form-control form-control-sm font-monospace" placeholder="10.8.25.10" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">端口</label><input v-model.number="pModal.ssh_port" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">用户</label><input v-model="pModal.ssh_user" class="form-control form-control-sm" placeholder="root" /></div>
        <div class="col-md-3"><label class="form-label small mb-1">密码（留空=不改）</label><input v-model="pModal.ssh_pass" type="password" class="form-control form-control-sm" autocomplete="new-password" /></div>
        <div class="col-12 text-danger small" v-if="pModal.err">{{ pModal.err }}</div>

        <div class="col-12"><hr class="my-1" /></div>
        <div class="col-12 d-flex flex-wrap gap-2 align-items-center">
          <button class="btn btn-sm btn-outline-success" :disabled="!pModal.sshConfigured" @click="sshOp('test')">测试连接</button>
          <button class="btn btn-sm btn-outline-warning" :disabled="!pModal.sshConfigured" @click="sshOp('restart-agent')">重启探针服务</button>
          <button class="btn btn-sm btn-outline-dark" :disabled="!pModal.sshConfigured" @click="sshExec()">远程命令</button>
          <router-link :to="`/probes/${pModal.id}/term`" class="btn btn-sm btn-dark" title="网页终端">🖥 网页终端</router-link>
          <span class="vr"></span>
          <select v-model.number="selRelease" class="form-select form-select-sm w-auto" title="选择目标版本">
            <option :value="0">升级到…</option>
            <option v-for="r in releases" :key="r.id" :value="r.id">{{ r.version }}（{{ r.arch }}）</option>
          </select>
          <button class="btn btn-sm btn-outline-warning" :disabled="!selRelease" @click="upgrade()">升级</button>
          <button class="btn btn-sm btn-outline-danger ms-auto" @click="del()">删除探针</button>
        </div>
        <div class="col-12" v-if="opMsg" :class="opOk ? 'text-success' : 'text-danger'" style="white-space:pre-wrap;font-size:12px">{{ opMsg }}</div>
      </div>
    </ModalBox>

    <div class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">版本发布与升级</h6>
        <span class="text-secondary small ms-3">上传新版探针二进制后，在探针「⚙ 编辑 / 运维」弹窗中选择版本一键升级（自动下载校验替换并重启）</span>
      </div>
      <div class="row g-2 align-items-end mb-2">
        <div class="col-md-3"><label class="form-label small mb-1">版本号</label><input v-model="rel.version" class="form-control form-control-sm" placeholder="如 0.2" /></div>
        <div class="col-md-4"><label class="form-label small mb-1">二进制文件</label><input type="file" class="form-control form-control-sm" @change="relFile = $event.target.files[0]" /></div>
        <div class="col-md-2"><button class="btn btn-sm btn-outline-primary w-100" :disabled="!rel.version || !relFile" @click="uploadRel">上传发布</button></div>
      </div>
      <table class="table nw-table table-sm align-middle mb-1" v-if="releases.length">
        <thead><tr><th>已发布版本</th><th>架构</th><th>大小</th><th>上传时间</th></tr></thead>
        <tbody>
          <tr v-for="r in releases" :key="r.id">
            <td><code>{{ r.version }}</code></td>
            <td>{{ r.arch }}</td>
            <td>{{ (r.size_bytes/1048576).toFixed(1) }}MB</td>
            <td class="small text-secondary">{{ fmtTime(r.created_at) }}</td>
          </tr>
        </tbody>
      </table>
      <div v-if="upMsg" class="small" :class="upOk ? 'text-success' : 'text-danger'">{{ upMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">SSH 运维凭据批量导入</h6>
        <button class="btn btn-sm btn-outline-primary ms-auto" @click="showImport = !showImport">{{ showImport ? '收起' : '展开导入' }}</button>
      </div>
      <template v-if="showImport">
        <p class="text-secondary small mb-1">每行：<code>匹配键,SSH主机,用户,密码[,端口]</code>。匹配键 = 探针 ID 前缀或注册主机名；# 开头为注释。密码将以 AES-256-GCM 加密存储。</p>
        <textarea v-model="sshCsv" class="form-control font-monospace" rows="4"
          placeholder="# 例（按注册主机名匹配）&#10;probe-01,10.8.25.10,root,P@ssw0rd,22"></textarea>
        <div class="d-flex gap-2 mt-2 align-items-center">
          <button class="btn btn-sm btn-outline-primary" :disabled="!sshCsv" @click="importSSH">导入</button>
          <span v-if="importSSHMsg" class="small" :class="importSSHOok ? 'text-success' : 'text-danger'">{{ importSSHMsg }}</span>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import ModalBox from '../components/ModalBox.vue'
import { fmtTime } from '../utils'

const probes = ref([]), lines = ref([])
const releases = ref([]), rel = ref({ version: '' }), relFile = ref(null), selRelease = ref(0)
const upMsg = ref(''), upOk = ref(false)
const opMsg = ref(''), opOk = ref(false)
const sshCsv = ref(''), importSSHMsg = ref(''), importSSHOok = ref(false)
const showImport = ref(false)
const pModal = ref({ show: false })

const selVer = computed(() => {
  const r = releases.value.find(x => x.id === selRelease.value)
  return r ? r.version : ''
})

function lineName(p) {
  if ((p.probe.group || 'internal') === 'external') return '探测官网'
  const l = lines.value.find(x => x.id === p.probe.line_id)
  return l ? l.name : '未绑定'
}

// ---- 编辑/运维弹窗：名称/分组/绑定/SSH 统一"确定"保存；操作按钮在弹窗内 ----
function openOps(p) {
  pModal.value = {
    show: true, id: p.probe.id, disp: p.probe.name || p.probe.hostname,
    name: p.probe.name || p.probe.hostname || '',
    group: p.probe.group || 'internal',
    line_id: p.probe.line_id || 0,
    ssh_host: p.probe.ssh_host || '', ssh_port: p.probe.ssh_port || 22,
    ssh_user: p.probe.ssh_user || 'root', ssh_pass: '',
    sshConfigured: p.probe.ssh_configured,
    origGroup: p.probe.group || 'internal', origLine: p.probe.line_id || 0,
    err: '',
  }
  opMsg.value = ''
}

async function saveMeta() {
  const m = pModal.value
  m.err = ''
  if (!m.name.trim()) { m.err = '探针名称不能为空'; return }
  try {
    const body = { name: m.name.trim(), ssh_host: m.ssh_host,
      ssh_port: m.ssh_port, ssh_user: m.ssh_user }
    if (m.ssh_pass) body.ssh_pass = m.ssh_pass
    await api.put(`/api/admin/probes/${m.id}/meta`, body)
    if (m.group !== m.origGroup) await api.post(`/api/admin/probes/${m.id}/group`, { group: m.group })
    if (m.group === 'internal' && (m.line_id || 0) !== (m.origLine || 0))
      await api.post(`/api/admin/probes/${m.id}/assign`, { line_id: m.line_id || null })
    m.show = false
    await load()
  } catch (e) { m.err = e.message }
}

async function sshOp(op) {
  opMsg.value = '执行中…'
  try {
    const d = await api.post(`/api/admin/probes/${pModal.value.id}/ssh/${op}`, {})
    opMsg.value = (d.output || '').trim() || '成功'
    opOk.value = true
  } catch (e) { opMsg.value = e.message; opOk.value = false }
}

async function sshExec() {
  const cmd = prompt('输入要在该探针上执行的命令（管理员操作，将记录审计日志）：')
  if (!cmd) return
  opMsg.value = '执行中…'
  try {
    const d = await api.post(`/api/admin/probes/${pModal.value.id}/ssh/exec`, { command: cmd })
    opMsg.value = (d.output || '').trim() || '（无输出）'
    opOk.value = true
  } catch (e) { opMsg.value = e.message; opOk.value = false }
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

async function upgrade() {
  const m = pModal.value
  if (!selRelease.value) return
  const p = probes.value.find(x => x.probe.id === m.id)
  if (!confirm(`将探针 ${m.id.slice(0, 8)}（当前 ${p ? p.probe.version || '—' : '—'}）升级到 ${selVer.value}？`)) return
  try {
    await api.post('/api/admin/probes/upgrade', { probe_ids: [m.id], release_id: selRelease.value })
    opMsg.value = '升级任务已下发，探针将在 30 秒内开始自更新'; opOk.value = true
    setTimeout(load, 5000)
  } catch (e) { opMsg.value = e.message; opOk.value = false }
}

async function del() {
  const m = pModal.value
  if (!confirm(`删除探针 ${m.id.slice(0, 8)}？该探针需重新注册才能接入。`)) return
  await api.del('/api/admin/probes/' + m.id)
  m.show = false
  await load()
}

onMounted(load)
</script>
