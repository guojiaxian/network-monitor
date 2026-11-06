<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <h4 class="mb-0">🔎 IP 扫描</h4>
      <span class="text-secondary small ms-3">中心服务器对楼层子网逐 IP ping（单包）；禁 ping 的主机（如 Windows 默认防火墙）会显示离线</span>
    </div>

    <div class="nw-card mb-3">
      <div class="row g-2 align-items-end">
        <div class="col-md-4">
          <label class="form-label small mb-1">楼层</label>
          <select v-model="scanFloorId" class="form-select form-select-sm">
            <option v-for="f in floors" :key="f.id" :value="f.id">{{ f.name }}（{{ f.cidr || '未配 CIDR' }}）</option>
          </select>
        </div>
        <div class="col-md-3">
          <button class="btn btn-sm btn-primary w-100" :disabled="!scanFloorId || scanning" @click="doScan">{{ scanning ? '扫描中…' : '开始扫描' }}</button>
        </div>
        <div class="col-md-5 text-end small text-secondary" v-if="scanRes">
          在线 <b style="color:#0a8f4d">{{ scanRes.up }}</b> · 离线 <b>{{ scanRes.down }}</b> · 共 {{ scanRes.total }} · 耗时 {{ (scanRes.duration_ms/1000).toFixed(1) }}s
        </div>
      </div>
    </div>

    <div class="nw-card" v-if="scanRes">
      <div class="ip-grid">
        <div v-for="r in scanRes.results" :key="r.ip" class="ip-cell" :class="r.online ? 'on' : 'off'"
             :title="r.ip + (r.online ? ' · 在线 · ' + r.rtt_ms.toFixed(1) + 'ms' : ' · 离线')">
          {{ r.ip.split('.')[3] }}<span v-if="r.online" class="ip-rtt">{{ r.rtt_ms.toFixed(1) }}</span>
        </div>
      </div>
    </div>
    <div class="nw-card text-secondary small py-4 text-center" v-else>选择楼层并点击"开始扫描"，在线主机将以绿色格子展示。</div>
    <div v-if="scanErr" class="text-danger small mt-2">{{ scanErr }}</div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../api'

const floors = ref([])
const scanFloorId = ref(null), scanning = ref(false), scanRes = ref(null), scanErr = ref('')

async function doScan() {
  scanning.value = true; scanErr.value = ''
  try {
    scanRes.value = await api.post(`/api/admin/floors/${scanFloorId.value}/scan`)
  } catch (e) { scanErr.value = e.message } finally { scanning.value = false }
}

onMounted(async () => {
  floors.value = await api.get('/api/admin/floors')
  if (floors.value.length) scanFloorId.value = floors.value[0].id
})
</script>
