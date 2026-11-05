<template>
  <div>
    <h4 class="mb-3">线路与楼层配置</h4>

    <div class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">楼层（子网）</h6>
      </div>
      <table class="table nw-table table-sm align-middle mb-2">
        <thead><tr><th style="width:180px">楼层名</th><th>子网 CIDR</th><th style="width:80px">线路数</th><th style="width:150px"></th></tr></thead>
        <tbody>
          <tr v-for="f in floors" :key="f.id">
            <td>{{ f.name }}</td><td><code>{{ f.cidr || '—' }}</code></td><td>{{ f.line_count }}</td>
            <td class="text-end"><button class="btn btn-sm btn-outline-danger" @click="delFloor(f)">删除</button></td>
          </tr>
          <tr>
            <td><input v-model="nf.name" class="form-control form-control-sm" placeholder="如 3楼" /></td>
            <td><input v-model="nf.cidr" class="form-control form-control-sm" placeholder="10.8.27.0/24" /></td>
            <td></td>
            <td class="text-end"><button class="btn btn-sm btn-primary" @click="addFloor">新增楼层</button></td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="nw-card mb-3">
      <h6>宽带线路</h6>
      <table class="table nw-table table-sm align-middle mb-2">
        <thead><tr><th>线路名</th><th>出口公网 IP</th><th>契约带宽(下/上 Mbps)</th><th>楼层</th><th style="width:150px"></th></tr></thead>
        <tbody>
          <tr v-for="l in lines" :key="l.id">
            <td><router-link :to="'/lines/' + l.id">{{ l.name }}</router-link></td>
            <td><code>{{ l.public_ip || '—' }}</code></td>
            <td>{{ l.contract_down_mbps || '—' }} / {{ l.contract_up_mbps || '—' }}</td>
            <td>{{ l.floor_name || '—' }}</td>
            <td class="text-end">
              <button class="btn btn-sm btn-outline-success me-1" :disabled="speeding === l.id" @click="speedNow(l)">
                {{ speeding === l.id ? '已排程…' : '⚡ 立即测速' }}
              </button>
              <button class="btn btn-sm btn-outline-primary me-1" @click="edit(l)">编辑</button>
              <button class="btn btn-sm btn-outline-danger" @click="delLine(l)">删除</button>
            </td>
          </tr>
          <tr v-if="!lines.length"><td colspan="5" class="text-secondary">暂无线路，可在下方添加或用 CSV 批量导入</td></tr>
        </tbody>
      </table>

      <div class="row g-2 align-items-end">
        <div class="col-md-2"><label class="form-label small mb-1">线路名</label><input v-model="nl.name" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">出口公网 IP</label><input v-model="nl.public_ip" class="form-control form-control-sm" placeholder="留空=探针自动探测" /></div>
        <div class="col-md-1"><label class="form-label small mb-1">下行M</label><input v-model.number="nl.contract_down_mbps" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-1"><label class="form-label small mb-1">上行M</label><input v-model.number="nl.contract_up_mbps" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2">
          <label class="form-label small mb-1">楼层</label>
          <select v-model.number="nl.floor_id" class="form-select form-select-sm">
            <option :value="undefined">未关联</option>
            <option v-for="f in floors" :key="f.id" :value="f.id">{{ f.name }}</option>
          </select>
        </div>
        <div class="col-md-2"><label class="form-label small mb-1">ISP</label><input v-model="nl.isp" class="form-control form-control-sm" placeholder="联通" /></div>
        <div class="col-md-2 text-end">
          <button class="btn btn-sm btn-primary" @click="saveLine">{{ nl.id ? '保存修改' : '新增线路' }}</button>
          <button v-if="nl.id" class="btn btn-sm btn-link" @click="resetLine">取消</button>
        </div>
      </div>
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
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../api'

const floors = ref([]), lines = ref([])
const nf = ref({ name: '', cidr: '' })
const nl = ref({})
const csv = ref(''), importMsg = ref(''), importOk = ref(false)
const speeding = ref(0), speedMsg = ref('')

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

function resetLine() { nl.value = {} }
function edit(l) { nl.value = { ...l, floor_id: l.floor_id || undefined } }

async function addFloor() {
  if (!nf.value.name) return
  await api.post('/api/admin/floors', { name: nf.value.name, cidr: nf.value.cidr })
  nf.value = { name: '', cidr: '' }
  await load()
}

async function delFloor(f) {
  if (!confirm(`删除楼层「${f.name}」？其下线路将变为未关联。`)) return
  await api.del('/api/admin/floors/' + f.id)
  await load()
}

async function saveLine() {
  if (!nl.value.name) return
  await api.post('/api/admin/lines', nl.value)
  resetLine()
  await load()
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
