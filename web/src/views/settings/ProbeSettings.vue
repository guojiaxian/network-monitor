<template>
  <div>
    <div class="nw-card mb-3">
      <h6>探测配置（内网组·下发到全部楼层探针）</h6>
      <div class="row g-2">
        <div class="col-md-2"><label class="form-label small mb-1">Ping 间隔(秒)</label><input v-model.number="ac.interval_ping_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">TCP 间隔(秒)</label><input v-model.number="ac.interval_tcp_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">DNS 间隔(秒)</label><input v-model.number="ac.interval_dns_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">HTTP 间隔(秒)</label><input v-model.number="ac.interval_http_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">超时(秒)</label><input v-model.number="ac.timeout_s" type="number" class="form-control form-control-sm" /></div>
      </div>
      <div class="row g-3 mt-0">
        <div class="col-md-6">
          <label class="form-label small mb-1">ICMP 目标（IP）</label>
          <target-list :items="ac.icmp_targets" add-title="新增 ICMP 目标" add-placeholder="IP，如 223.5.5.5" @add="openTarget" @edit="editTarget" @remove="removeItem" />
        </div>
        <div class="col-md-6">
          <label class="form-label small mb-1">TCP 目标（host:port）</label>
          <target-list :items="ac.tcp_targets" add-title="新增 TCP 目标" add-placeholder="host:port，如 www.qq.com:443" @add="openTarget" @edit="editTarget" @remove="removeItem" />
        </div>
        <div class="col-md-4">
          <label class="form-label small mb-1">DNS 服务器</label>
          <target-list :items="ac.dns_servers" add-title="新增 DNS 服务器" add-placeholder="IP，如 223.5.5.5" @add="openTarget" @edit="editTarget" @remove="removeItem" />
        </div>
        <div class="col-md-4"><label class="form-label small mb-1">解析测试域名</label><input v-model="ac.dns_domain" class="form-control form-control-sm" /></div>
        <div class="col-md-4">
          <label class="form-label small mb-1">HTTP 目标（URL）</label>
          <target-list :items="ac.http_targets" add-title="新增 HTTP 目标" add-placeholder="URL，如 https://www.baidu.com" @add="openTarget" @edit="editTarget" @remove="removeItem" />
        </div>
        <div class="col-md-4"><label class="form-label small mb-1">注册令牌（探针接入用）</label><input v-model="enrollToken" class="form-control form-control-sm font-monospace" /></div>
        <div class="col-md-8 offset-md-4 text-end align-self-end">
          <span class="text-secondary small me-2">修改后约 60 秒内下发到全部探针</span>
          <button class="btn btn-sm btn-primary" @click="saveAC">保存并下发</button>
        </div>
      </div>
      <div v-if="acMsg" class="small mt-2" :class="acOk ? 'text-success' : 'text-danger'">{{ acMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>外网探测组（阿里云等外网服务器 · 探测单位官网）</h6>
      <p class="text-secondary small mb-2">在外网服务器上安装同一探针程序并切到"外网组"后，按此配置探测：<b>ping 官网域名 + HTTPS 访问官网</b>。全部外网探针访问失败 → P1"网站从外网不可达"；HTTPS 证书剩余 &lt;15 天 → P2 提醒。</p>
      <div class="row g-3 mt-0">
        <div class="col-md-4">
          <label class="form-label small mb-1">HTTPS 目标（官网 URL）</label>
          <target-list :items="ext.http_targets" add-title="新增 HTTPS 目标（官网）" add-placeholder="https://www.单位域名.cn" @add="openTarget" @edit="editTarget" @remove="removeItem" />
        </div>
        <div class="col-md-4">
          <label class="form-label small mb-1">ICMP 目标（域名或 IP）</label>
          <target-list :items="ext.icmp_targets" add-title="新增外网组 ICMP 目标" add-placeholder="域名或 IP，如 www.单位域名.cn" @add="openTarget" @edit="editTarget" @remove="removeItem" />
        </div>
        <div class="col-md-2"><label class="form-label small mb-1">Ping 间隔(秒)</label><input v-model.number="ext.interval_ping_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">HTTP 间隔(秒)</label><input v-model.number="ext.interval_http_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">超时(秒)</label><input v-model.number="ext.timeout_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-10 text-end align-self-end">
          <span class="text-secondary small me-2">保存后 30 秒内下发给全部外网组探针</span>
          <button class="btn btn-sm btn-primary" @click="saveExt">保存外网组配置</button>
        </div>
      </div>
      <div v-if="extMsg" class="small mt-2" :class="extOk ? 'text-success' : 'text-danger'">{{ extMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>内网服务监控（内→内 · 中心服务器内置探测）</h6>
      <p class="text-secondary small mb-2">类似 Uptime Kuma：中心服务器自己作为探针，监控内网服务（OA、业务系统、服务器、交换机）。连续 3 次失败触发 P2 告警，恢复自动解除。</p>
      <div class="d-flex align-items-center mb-2">
        <div class="form-check">
          <input id="im1" v-model="im.enabled" type="checkbox" class="form-check-input" />
          <label for="im1" class="form-check-label">启用</label>
        </div>
        <button class="btn btn-sm btn-primary ms-auto" @click="saveIM">保存</button>
      </div>
      <table class="table nw-table table-sm align-middle" v-if="(im.items || []).length">
        <thead><tr><th>名称</th><th style="width:90px">类型</th><th>目标</th><th style="width:90px">间隔(秒)</th><th style="width:60px"></th></tr></thead>
        <tbody>
          <tr v-for="(it, i) in im.items" :key="i">
            <td>{{ it.name }}</td><td><span class="badge bg-secondary">{{ it.type }}</span></td>
            <td><code>{{ it.target }}</code></td><td>{{ it.interval_s }}</td>
            <td><a href="javascript:void(0)" class="text-primary text-decoration-none me-2" @click="openIM(it, i)">编辑</a><a href="javascript:void(0)" class="text-danger text-decoration-none" @click="im.items.splice(i, 1)">删除</a></td>
          </tr>
        </tbody>
      </table>
      <p v-else class="text-secondary small mb-2">暂无监控目标。</p>
      <button class="btn btn-sm btn-outline-primary" @click="openIM()">＋ 新增监控目标</button>
      <div v-if="imMsg" class="small mt-2" :class="imOk ? 'text-success' : 'text-danger'">{{ imMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>行业网监测（中心 → 行业网 · 仅 ICMP ping）</h6>
      <p class="text-secondary small mb-2">中心服务器对行业网指定 IP <b>仅做 ICMP ping</b>，不做 TCP/HTTP 等任何其他探测（以免被行业网封禁）。IP 须为合法地址（如 10.5.x.x）。请确保中心服务器可路由到行业网网段。连续 3 次失败触发 P2"行业网不可达"，恢复自动解除。</p>
      <div class="d-flex align-items-center mb-2">
        <div class="form-check">
          <input id="ind1" v-model="ind.enabled" type="checkbox" class="form-check-input" />
          <label for="ind1" class="form-check-label">启用</label>
        </div>
        <button class="btn btn-sm btn-primary ms-auto" @click="saveInd">保存</button>
      </div>
      <table class="table nw-table table-sm align-middle" v-if="(ind.items || []).length">
        <thead><tr><th>名称</th><th>监测 IP</th><th style="width:90px">间隔(秒)</th><th style="width:60px"></th></tr></thead>
        <tbody>
          <tr v-for="(it, i) in ind.items" :key="i">
            <td>{{ it.name }}</td><td><code>{{ it.target }}</code></td><td>{{ it.interval_s }}</td>
            <td><a href="javascript:void(0)" class="text-primary text-decoration-none me-2" @click="openInd(it, i)">编辑</a><a href="javascript:void(0)" class="text-danger text-decoration-none" @click="ind.items.splice(i, 1)">删除</a></td>
          </tr>
        </tbody>
      </table>
      <p v-else class="text-secondary small mb-2">暂无监测目标。</p>
      <button class="btn btn-sm btn-outline-primary" @click="openInd()">＋ 新增监测 IP</button>
      <div v-if="indMsg" class="small mt-2" :class="indOk ? 'text-success' : 'text-danger'">{{ indMsg }}</div>
    </div>

    <!-- 新增探测目标（单输入，六类目标共用） -->
    <ModalBox :show="tModal.show" :title="tModal.editIdx >= 0 ? '修改目标' : tModal.title" @close="tModal.show = false" @ok="confirmTarget">
      <input v-model="tModal.val" class="form-control form-control-sm font-monospace" :placeholder="tModal.placeholder"
        @keyup.enter="confirmTarget" />
      <div v-if="tModal.err" class="text-danger small mt-2">{{ tModal.err }}</div>
    </ModalBox>

    <!-- 新增内网服务监控目标 -->
    <ModalBox :show="imModal.show" :title="imModal.idx >= 0 ? '编辑内网服务监控目标' : '新增内网服务监控目标'" @close="imModal.show = false" @ok="confirmIM">
      <div class="row g-2">
        <div class="col-md-6"><label class="form-label small mb-1">名称 <span class="text-danger">*</span></label>
          <input v-model="imModal.name" class="form-control form-control-sm" placeholder="如 OA系统" /></div>
        <div class="col-md-6"><label class="form-label small mb-1">类型</label>
          <select v-model="imModal.type" class="form-select form-select-sm">
            <option value="http">http</option><option value="tcp">tcp</option><option value="icmp">icmp</option>
          </select></div>
        <div class="col-md-8"><label class="form-label small mb-1">目标 <span class="text-danger">*</span></label>
          <input v-model="imModal.target" class="form-control form-control-sm font-monospace" placeholder="http: URL / tcp: host:port / icmp: IP" /></div>
        <div class="col-md-4"><label class="form-label small mb-1">间隔(秒)</label>
          <input v-model.number="imModal.interval_s" type="number" class="form-control form-control-sm" placeholder="30" /></div>
      </div>
      <div v-if="imModal.err" class="text-danger small mt-2">{{ imModal.err }}</div>
    </ModalBox>

    <!-- 新增行业网监测 IP -->
    <ModalBox :show="indModal.show" :title="indModal.idx >= 0 ? '编辑行业网监测 IP' : '新增行业网监测 IP（仅 ICMP）'" @close="indModal.show = false" @ok="confirmInd">
      <div class="row g-2">
        <div class="col-md-6"><label class="form-label small mb-1">名称 <span class="text-danger">*</span></label>
          <input v-model="indModal.name" class="form-control form-control-sm" placeholder="如 行业网核心交换" /></div>
        <div class="col-md-6"><label class="form-label small mb-1">监测 IP <span class="text-danger">*</span></label>
          <input v-model="indModal.target" class="form-control form-control-sm font-monospace" placeholder="10.5.0.1" /></div>
        <div class="col-md-6"><label class="form-label small mb-1">间隔(秒)</label>
          <input v-model.number="indModal.interval_s" type="number" class="form-control form-control-sm" placeholder="30" /></div>
      </div>
      <div v-if="indModal.err" class="text-danger small mt-2">{{ indModal.err }}</div>
    </ModalBox>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api } from '../../api'
import ModalBox from '../../components/ModalBox.vue'
import TargetList from '../../components/TargetList.vue'

const ac = ref({ icmp_targets: [], tcp_targets: [], dns_servers: [], http_targets: [] })
const enrollToken = ref(''), acMsg = ref(''), acOk = ref(false)
const ext = ref({ http_targets: [], icmp_targets: [] }), extMsg = ref(''), extOk = ref(false)
const im = ref({ enabled: false, items: [] }), imMsg = ref(''), imOk = ref(false)
const ind = ref({ enabled: false, items: [] }), indMsg = ref(''), indOk = ref(false)

const tModal = reactive({ show: false, title: '', placeholder: '', arr: null, val: '', err: '', editIdx: -1 })
const imModal = reactive({ show: false, name: '', type: 'http', target: '', interval_s: 30, err: '', idx: -1 })
const indModal = reactive({ show: false, name: '', target: '', interval_s: 30, err: '', idx: -1 })

async function load() {
  const s = await api.get('/api/admin/settings')
  ac.value = s.agent_config || {}
  for (const k of ['icmp_targets', 'tcp_targets', 'dns_servers', 'http_targets'])
    if (!Array.isArray(ac.value[k])) ac.value[k] = []
  enrollToken.value = s['agent.enroll_token'] ? JSON.parse(JSON.stringify(s['agent.enroll_token'])) : ''
  ext.value = s.agent_config_external || {}
  for (const k of ['http_targets', 'icmp_targets'])
    if (!Array.isArray(ext.value[k])) ext.value[k] = []
  im.value = s.internal_monitors || {}
  if (!Array.isArray(im.value.items)) im.value.items = []
  ind.value = s.industry_monitors || {}
  if (!Array.isArray(ind.value.items)) ind.value.items = []
}

// ---- 目标弹窗（新增 + 点击 badge 编辑） ----
function openTarget(title, placeholder, arr) {
  Object.assign(tModal, { show: true, title, placeholder, arr, val: '', err: '', editIdx: -1 })
}
function editTarget(arr, i) {
  Object.assign(tModal, { show: true, title: '修改目标', placeholder: '', arr, val: arr[i], err: '', editIdx: i })
}
function confirmTarget() {
  const v = (tModal.val || '').trim()
  if (!v) { tModal.err = '请输入目标'; return }
  if (tModal.arr.includes(v) && tModal.arr[tModal.editIdx] !== v) { tModal.err = '该目标已存在'; return }
  if (tModal.editIdx >= 0) tModal.arr.splice(tModal.editIdx, 1, v)
  else tModal.arr.push(v)
  tModal.show = false
}
function removeItem(arr, i) { arr.splice(i, 1) }

// ---- 内网监控/行业网弹窗 ----
function openIM(it, idx) {
  Object.assign(imModal, it ? { show: true, name: it.name, type: it.type, target: it.target, interval_s: it.interval_s, err: '', idx }
                           : { show: true, name: '', type: 'http', target: '', interval_s: 30, err: '', idx: -1 })
}
function confirmIM() {
  imModal.err = ''
  if (!imModal.name.trim()) { imModal.err = '请填写名称'; return }
  if (!imModal.target.trim()) { imModal.err = '请填写目标'; return }
  const row = { name: imModal.name.trim(), type: imModal.type, target: imModal.target.trim(), interval_s: imModal.interval_s || 30 }
  if (imModal.idx >= 0) im.value.items.splice(imModal.idx, 1, row)
  else im.value.items.push(row)
  imModal.show = false
}
function openInd(it, idx) {
  Object.assign(indModal, it ? { show: true, name: it.name, target: it.target, interval_s: it.interval_s, err: '', idx }
                             : { show: true, name: '', target: '', interval_s: 30, err: '', idx: -1 })
}
function confirmInd() {
  indModal.err = ''
  if (!indModal.name.trim()) { indModal.err = '请填写名称'; return }
  if (!indModal.target.trim()) { indModal.err = '请填写监测 IP'; return }
  const row = { name: indModal.name.trim(), target: indModal.target.trim(), interval_s: indModal.interval_s || 30 }
  if (indModal.idx >= 0) ind.value.items.splice(indModal.idx, 1, row)
  else ind.value.items.push(row)
  indModal.show = false
}

function bump(v) { v.version = (v.version || 0) + 1; return v }

async function saveAC() {
  acMsg.value = ''; acOk.value = false
  try {
    const body = { agent_config: bump(JSON.parse(JSON.stringify(ac.value))) }
    if (enrollToken.value) body['agent.enroll_token'] = enrollToken.value
    await api.put('/api/admin/settings', body)
    acMsg.value = '已保存，探针将在 60 秒内应用新配置'; acOk.value = true
  } catch (e) { acMsg.value = e.message }
}

async function saveExt() {
  extMsg.value = ''; extOk.value = false
  try {
    const c = bump(JSON.parse(JSON.stringify(ext.value)))
    c.skip_gateway = true
    await api.put('/api/admin/settings', { agent_config_external: c })
    extMsg.value = '已保存'; extOk.value = true
  } catch (e) { extMsg.value = e.message }
}

async function saveIM() {
  imMsg.value = ''; imOk.value = false
  try {
    const items = (im.value.items || []).filter(x => x.name && x.target)
    await api.put('/api/admin/settings', { internal_monitors: { enabled: im.value.enabled, items } })
    imMsg.value = `已保存 ${items.length} 个目标，30 秒内生效`; imOk.value = true
  } catch (e) { imMsg.value = e.message }
}

async function saveInd() {
  indMsg.value = ''; indOk.value = false
  try {
    const items = (ind.value.items || []).filter(x => x.name && x.target)
    await api.put('/api/admin/settings', { industry_monitors: { enabled: ind.value.enabled, items } })
    indMsg.value = `已保存 ${items.length} 个目标，30 秒内生效（仅 ICMP ping）`; indOk.value = true
  } catch (e) { indMsg.value = e.message }
}

onMounted(load)
</script>
