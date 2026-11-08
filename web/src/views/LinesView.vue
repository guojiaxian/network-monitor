<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <div>
        <h4 class="mb-0">🌐 线路配置</h4>
        <div class="text-secondary small mt-1">宽带线路清单、契约带宽与楼层关联；出口公网 IP 由绑定探针自动探测回填</div>
      </div>
    </div>

    <div class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">宽带线路</h6>
        <button class="btn btn-sm btn-primary ms-auto" @click="openLine()">＋ 新增线路</button>
      </div>
      <table class="table nw-table table-sm align-middle mb-2 table-fixed">
        <thead><tr>
          <th style="width:14%">线路名</th><th style="width:14%">子网 CIDR</th><th style="width:13%">出口公网 IP</th>
          <th style="width:16%">ISP / 归属地</th><th style="width:13%">契约带宽(下/上 Mbps)</th><th style="width:15%">楼层</th><th style="width:230px"></th>
        </tr></thead>
        <tbody>
          <tr v-for="l in lines" :key="l.id">
            <td><router-link :to="'/lines/' + l.id">{{ l.name }}</router-link></td>
            <td><code>{{ l.cidr || '—' }}</code></td>
            <td><code>{{ l.public_ip || '—' }}</code></td>
            <td class="small">{{ l.isp || '—' }}<span v-if="l.geo" class="text-secondary"> · {{ l.geo }}</span></td>
            <td>{{ l.contract_down_mbps || '—' }} / {{ l.contract_up_mbps || '—' }}</td>
            <td>{{ l.floor_name || '—' }}</td>
            <td class="text-end">
              <button class="btn btn-sm btn-outline-success me-1" :disabled="speeding === l.id" @click="speedNow(l)">
                {{ speeding === l.id ? '已排程…' : '立即测速' }}
              </button>
              <button class="btn btn-sm btn-outline-primary me-1" @click="openLine(l)">编辑</button>
              <button class="btn btn-sm btn-outline-danger" @click="delLine(l)">删除</button>
            </td>
          </tr>
          <tr v-if="!lines.length"><td colspan="7" class="text-secondary">暂无线路，点击右上角「＋ 新增线路」或用下方 CSV 批量导入</td></tr>
        </tbody>
      </table>
    </div>

    <div class="nw-card">
      <h6>CSV 批量导入</h6>
      <p class="text-secondary small mb-1">每行：楼层名,子网CIDR,线路名,公网IP,下行Mbps,上行Mbps（后三列可省略；楼层不存在时自动创建）</p>
      <textarea v-model="csv" class="form-control font-monospace" rows="4"
        placeholder="1楼,10.8.25.0/24,联通1号线,,1000,50"></textarea>
      <button class="btn btn-outline-primary btn-sm mt-2" :disabled="!csv" @click="doImport">导入</button>
      <span v-if="importMsg" class="small ms-2" :class="importOk ? 'text-success' : 'text-danger'">{{ importMsg }}</span>
      <span v-if="speedMsg" class="small ms-2 text-success">{{ speedMsg }}</span>
    </div>

    <!-- 新增/编辑线路 -->
    <ModalBox :show="lModal.show" :title="lModal.id ? '编辑线路' : '新增线路'" @close="lModal.show = false" @ok="saveLine">
      <div class="row g-2">
        <div class="col-md-6"><label class="form-label small mb-1">线路名 <span class="text-danger">*</span></label>
          <input v-model="lModal.name" class="form-control form-control-sm" placeholder="如 联通3号线" /></div>
        <div class="col-12"><label class="form-label small mb-1">关联楼层（可多选，对应"分配情况"）</label>
          <div class="d-flex flex-wrap gap-2 border rounded p-2">
            <span v-if="!floors.length" class="text-secondary small">暂无楼层</span>
            <div class="form-check mb-0" v-for="f in floors" :key="f.id">
              <input type="checkbox" :id="'lf'+f.id" :value="f.id" v-model="lModal.floor_ids" class="form-check-input" />
              <label :for="'lf'+f.id" class="form-check-label small">{{ f.name }} <code class="text-secondary" v-if="f.cidr">{{ f.cidr }}</code></label>
            </div>
          </div></div>
        <div class="col-md-4"><label class="form-label small mb-1">下行契约 Mbps</label>
          <input v-model.number="lModal.contract_down_mbps" type="number" class="form-control form-control-sm" placeholder="如 1000" /></div>
        <div class="col-md-4"><label class="form-label small mb-1">上行契约 Mbps</label>
          <input v-model.number="lModal.contract_up_mbps" type="number" class="form-control form-control-sm" placeholder="如 50" /></div>
        <div class="col-md-4"><label class="form-label small mb-1">ISP</label>
          <input v-model="lModal.isp" class="form-control form-control-sm" placeholder="留空=探针自动探测" /></div>
        <div class="col-12"><label class="form-label small mb-1">出口公网 IP</label>
          <input v-model="lModal.public_ip" class="form-control form-control-sm font-monospace" placeholder="留空=探针自动探测并自动跟随变更" />
          <div class="form-text small">公网 IP、ISP、归属地均由绑定探针自动探测回填，手工值仅作探测全挂时的兜底显示</div></div>
      </div>
      <div v-if="lModal.err" class="text-danger small mt-2">{{ lModal.err }}</div>
    </ModalBox>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api } from '../api'
import ModalBox from '../components/ModalBox.vue'

const floors = ref([]), lines = ref([])
const csv = ref(''), importMsg = ref(''), importOk = ref(false)
const speeding = ref(0), speedMsg = ref('')
const lModal = reactive({ show: false, id: 0, name: '', public_ip: '', contract_down_mbps: undefined,
  contract_up_mbps: undefined, floor_ids: [], isp: '', err: '' })

async function speedNow(l) {
  speedMsg.value = ''
  try {
    await api.post('/api/admin/speedtest/run', { line_id: l.id })
    speeding.value = l.id
    speedMsg.value = `已为「${l.name}」排程测速，探针 30 秒内开始执行`
    setTimeout(() => { speeding.value = 0 }, 15000)
  } catch (e) { speedMsg.value = e.message }
}

async function load() {
  floors.value = await api.get('/api/admin/floors')
  lines.value = await api.get('/api/admin/lines')
}

// ---- 线路：点击新增/编辑 → 弹窗 → 确定 ----
function openLine(l) {
  Object.assign(lModal, {
    show: true, id: l ? l.id : 0,
    name: l ? l.name : '', public_ip: l ? l.public_ip : '',
    contract_down_mbps: l ? l.contract_down_mbps || undefined : undefined,
    contract_up_mbps: l ? l.contract_up_mbps || undefined : undefined,
    floor_ids: l ? (l.floors || []).map(f => f.id) : [],
    isp: l ? l.isp : '', err: '',
  })
}
async function saveLine() {
  lModal.err = ''
  if (!lModal.name.trim()) { lModal.err = '请填写线路名'; return }
  try {
    await api.post('/api/admin/lines', {
      id: lModal.id || undefined, name: lModal.name.trim(),
      public_ip: lModal.public_ip.trim(), isp: lModal.isp.trim(),
      contract_down_mbps: lModal.contract_down_mbps || 0, contract_up_mbps: lModal.contract_up_mbps || 0,
      floor_ids: lModal.floor_ids,
    })
    lModal.show = false
    await load()
  } catch (e) { lModal.err = e.message }
}

async function delLine(l) {
  if (!confirm(`删除线路「${l.name}」？绑定在该线路的探针将解绑。`)) return
  await api.del('/api/admin/lines/' + l.id)
  await load()
}

async function doImport() {
  importMsg.value = ''; importOk.value = false
  try {
    const r = await fetch('/api/admin/lines/import', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'text/csv' }, body: csv.value,
    })
    const d = await r.json()
    if (!r.ok) throw new Error(d.error || '导入失败')
    importMsg.value = `成功导入 ${d.imported} 条`; importOk.value = true
    await load()
  } catch (e) { importMsg.value = e.message }
}

onMounted(load)
</script>
