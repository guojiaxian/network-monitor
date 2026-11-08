<template>
  <div>
    <div class="nw-card mb-3">
      <h6>SNMP 真实流量采集（可选，需防火墙开通只读 SNMP）</h6>
      <div class="row g-2">
        <div class="col-md-2 d-flex align-items-center">
          <div class="form-check"><input id="sn1" v-model="sn.enabled" type="checkbox" class="form-check-input" /><label for="sn1" class="form-check-label">启用</label></div>
        </div>
        <div class="col-md-3"><label class="form-label small mb-1">防火墙管理 IP</label><input v-model="sn.device" class="form-control form-control-sm" placeholder="如 10.8.0.1" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">只读团体字</label><input v-model="sn.community" class="form-control form-control-sm" placeholder="public" /></div>
        <div class="col-md-1"><label class="form-label small mb-1">端口</label><input v-model.number="sn.port" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">轮询间隔(秒)</label><input v-model.number="sn.interval_s" type="number" class="form-control form-control-sm" /><div class="text-secondary" style="font-size:.75rem">≥10，默认 30</div></div>
        <div class="col-md-2"><label class="form-label small mb-1">请求超时(秒)</label><input v-model.number="sn.timeout_s" type="number" class="form-control form-control-sm" /><div class="text-secondary" style="font-size:.75rem">1 ~ 30，默认 5</div></div>
        <div class="col-md-2 d-flex gap-2 align-items-end">
          <button class="btn btn-sm btn-outline-primary flex-fill" @click="listIfaces">获取接口列表</button>
          <button class="btn btn-sm btn-primary flex-fill" @click="saveSN">保存</button>
        </div>
        <div class="col-12" v-if="ifaces.length">
          <label class="form-label small mb-1">接口映射（每行：线路名 = 接口）</label>
          <div class="row g-1">
            <div class="col-md-4" v-for="ifc in ifaces" :key="ifc.index">
              <div class="input-group input-group-sm">
                <span class="input-group-text" style="min-width:110px" :title="'ifIndex ' + ifc.index">{{ ifc.descr || ('接口' + ifc.index) }}</span>
                <select class="form-select" :value="mappingOf(ifc.index)" @change="setMapping(ifc.index, ifc.descr, $event.target.value)">
                  <option value="">未映射</option>
                  <option v-for="l in allLines" :key="l.id" :value="l.id">{{ l.name }}</option>
                </select>
              </div>
            </div>
          </div>
        </div>
      </div>
      <p class="text-secondary small mb-0 mt-2">启用后按「轮询间隔」轮询防火墙 WAN 口字节计数器，得到每线真实流量曲线（零测试流量成本，与测速互补）；映射改动保存后下轮生效。</p>
      <div v-if="snMsg" class="small mt-2" :class="snOk ? 'text-success' : 'text-danger'">{{ snMsg }}</div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../../api'

// 测速计划卡已迁至「探测配置」页（/probing），此处仅保留 SNMP 流量采集
const sn = ref({}), ifaces = ref([]), allLines = ref([]), snMsg = ref(''), snOk = ref(false)

async function load() {
  const s = await api.get('/api/admin/settings')
  sn.value = s.snmp_settings || {}
  allLines.value = await api.get('/api/admin/lines')
}

function mappingOf(idx) {
  const m = (sn.value.mappings || []).find(x => x.ifindex === idx)
  return m ? m.line_id : ''
}
function setMapping(idx, descr, lineId) {
  const ms = (sn.value.mappings || []).filter(x => x.ifindex !== idx)
  if (lineId) {
    const l = allLines.value.find(x => x.id === Number(lineId))
    if (l) ms.push({ line_id: l.id, line_name: l.name, ifindex: idx, ifdescr: descr || '' })
  }
  sn.value = { ...sn.value, mappings: ms }
}

async function listIfaces() {
  snMsg.value = ''; snOk.value = false
  try {
    await api.put('/api/admin/settings', { snmp_settings: { ...sn.value } })
    ifaces.value = await api.get('/api/admin/snmp/ifaces')
    snMsg.value = `获取到 ${ifaces.value.length} 个接口，请完成映射后保存`; snOk.value = true
  } catch (e) { snMsg.value = e.message }
}

async function saveSN() {
  snMsg.value = ''; snOk.value = false
  try {
    await api.put('/api/admin/settings', { snmp_settings: { ...sn.value } })
    snMsg.value = '已保存'; snOk.value = true
  } catch (e) { snMsg.value = e.message }
}

onMounted(load)
</script>
