<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <h4 class="mb-0">楼层配置</h4>
      <span class="text-secondary small ms-3">楼层（子网）是线路归属与 IP 扫描的锚点；楼层顺序即总览/大屏展示顺序</span>
    </div>

    <div class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">楼层列表</h6>
        <button class="btn btn-sm btn-primary ms-auto" @click="openFloor()">＋ 新增楼层</button>
      </div>
      <table class="table nw-table table-sm align-middle mb-2 table-fixed">
        <thead><tr><th style="width:22%">楼层名</th><th style="width:24%">子网 CIDR</th><th style="width:17%">线路数</th><th style="width:25%">关联线路（出口公网 IP）</th><th style="width:12%" class="text-end"></th></tr></thead>
        <tbody>
          <tr v-for="f in floors" :key="f.id">
            <td>{{ f.name }}</td><td><code>{{ f.cidr || '—' }}</code></td><td>{{ f.line_count }}</td>
            <td class="small">
              <template v-if="f.lines && f.lines.length">
                <div v-for="l in f.lines" :key="l.id">
                  <router-link :to="'/lines/' + l.id">{{ l.name }}</router-link>
                  <code v-if="l.public_ip" class="ms-1">{{ l.public_ip }}</code>
                  <span v-else class="text-secondary ms-1">出口待探测</span>
                </div>
              </template>
              <span v-else class="text-secondary">—</span>
            </td>
            <td class="text-end">
              <button class="btn btn-sm btn-outline-primary me-1" @click="openFloor(f)">编辑</button>
              <button class="btn btn-sm btn-outline-danger" @click="delFloor(f)">删除</button>
            </td>
          </tr>
          <tr v-if="!floors.length"><td colspan="5" class="text-secondary">暂无楼层，点击右上角「＋ 新增楼层」</td></tr>
        </tbody>
      </table>
    </div>

    <!-- 新增/编辑楼层 -->
    <ModalBox :show="fModal.show" :title="fModal.id ? '编辑楼层' : '新增楼层'" @close="fModal.show = false" @ok="saveFloor">
      <div class="mb-2"><label class="form-label small mb-1">楼层名 <span class="text-danger">*</span></label>
        <input v-model="fModal.name" class="form-control form-control-sm" placeholder="如 3楼" /></div>
      <div><label class="form-label small mb-1">子网 CIDR</label>
        <input v-model="fModal.cidr" class="form-control form-control-sm font-monospace" placeholder="10.8.27.0/24" />
        <div class="form-text small">用于楼层 IP 扫描与展示，可留空</div></div>
      <div v-if="fModal.err" class="text-danger small mt-2">{{ fModal.err }}</div>
    </ModalBox>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api } from '../api'
import ModalBox from '../components/ModalBox.vue'

const floors = ref([])
const fModal = reactive({ show: false, id: 0, name: '', cidr: '', err: '' })

async function load() {
  floors.value = await api.get('/api/admin/floors')
}

function openFloor(f) {
  Object.assign(fModal, { show: true, id: f ? f.id : 0, name: f ? f.name : '', cidr: f ? f.cidr : '', err: '' })
}
async function saveFloor() {
  fModal.err = ''
  if (!fModal.name.trim()) { fModal.err = '请填写楼层名'; return }
  try {
    await api.post('/api/admin/floors', { id: fModal.id || undefined, name: fModal.name.trim(), cidr: fModal.cidr.trim() })
    fModal.show = false
    await load()
  } catch (e) { fModal.err = e.message }
}

async function delFloor(f) {
  if (!confirm(`删除楼层「${f.name}」？其下线路将变为未关联。`)) return
  await api.del('/api/admin/floors/' + f.id)
  await load()
}

onMounted(load)
</script>
