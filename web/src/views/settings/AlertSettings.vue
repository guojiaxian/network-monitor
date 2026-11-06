<template>
  <div>
    <div class="nw-card mb-3">
      <h6>告警阈值与收件组</h6>
      <div class="row g-2">
        <div class="col-md-3"><label class="form-label small mb-1">盲区判定（秒）</label><input v-model.number="als.blind_secs" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-3"><label class="form-label small mb-1">中断判定窗口（秒）</label><input v-model.number="als.line_down_secs" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-3"><label class="form-label small mb-1">降质丢包阈值（%）</label><input v-model.number="als.loss_pct_thresh" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-3"><label class="form-label small mb-1">降质统计窗口（分钟）</label><input v-model.number="als.loss_window_min" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-8"><label class="form-label small mb-1">P2 收件组（逗号分隔，留空用默认收件人）</label><input v-model="alsP2Str" class="form-control form-control-sm" /></div>
        <div class="col-md-8"><label class="form-label small mb-1">P0/P1 收件组（逗号分隔，留空用默认收件人）</label><input v-model="alsP0P1Str" class="form-control form-control-sm" /></div>
        <div class="col-md-4 text-end align-self-end"><button class="btn btn-sm btn-primary" @click="saveALS">保存</button></div>
      </div>
      <div v-if="alsMsg" class="small mt-2" :class="alsOk ? 'text-success' : 'text-danger'">{{ alsMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">静默窗口（期间该线路不触发新告警）</h6>
        <button class="btn btn-sm btn-outline-primary ms-auto" @click="openSilence()">＋ 新增静默</button>
      </div>
      <table class="table nw-table table-sm align-middle mb-0">
        <thead><tr><th>范围</th><th>开始</th><th>结束</th><th>原因</th><th>创建人</th><th style="width:80px"></th></tr></thead>
        <tbody>
          <tr v-for="s in silences" :key="s.id">
            <td>{{ s.line_name || '全部线路' }}</td>
            <td class="small">{{ fmt(s.starts_at) }}</td>
            <td class="small">{{ fmt(s.ends_at) }}</td>
            <td class="small">{{ s.reason || '—' }}</td>
            <td class="small">{{ s.created_by }}</td>
            <td class="text-end"><button class="btn btn-sm btn-outline-danger" @click="delSilence(s)">取消</button></td>
          </tr>
          <tr v-if="!silences.length"><td colspan="6" class="text-secondary small">暂无静默窗口</td></tr>
        </tbody>
      </table>
      <p class="text-secondary small mb-0 mt-2">维护割接前创建静默窗口，期间该线路不触发新告警；告警中心页会正常展示历史记录。</p>
    </div>

    <!-- 新增静默弹窗 -->
    <ModalBox :show="silModal.show" title="新增静默窗口" @close="silModal.show = false" @ok="addSilence">
      <div class="row g-2">
        <div class="col-md-6">
          <label class="form-label small mb-1">范围</label>
          <select v-model.number="silModal.line_id" class="form-select form-select-sm">
            <option :value="0">全部线路</option>
            <option v-for="l in lines" :key="l.id" :value="l.id">{{ l.name }}</option>
          </select>
        </div>
        <div class="col-md-6"><label class="form-label small mb-1">时长（小时）</label><input v-model.number="silModal.hours" type="number" min="0.5" step="0.5" class="form-control form-control-sm" /></div>
        <div class="col-12"><label class="form-label small mb-1">原因（如线路商割接）</label><input v-model="silModal.reason" class="form-control form-control-sm" /></div>
      </div>
      <div v-if="silModal.err" class="text-danger small mt-2">{{ silModal.err }}</div>
    </ModalBox>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api } from '../../api'
import ModalBox from '../../components/ModalBox.vue'
import { toCSV, parseCSV, fmtTime } from '../../utils'

const als = ref({}), alsP0P1Str = ref(''), alsP2Str = ref(''), alsMsg = ref(''), alsOk = ref(false)
const silences = ref([]), lines = ref([])
const silModal = reactive({ show: false, line_id: 0, hours: 2, reason: '', err: '' })

function openSilence() { Object.assign(silModal, { show: true, line_id: 0, hours: 2, reason: '', err: '' }) }

const fmt = fmtTime

async function load() {
  const s = await api.get('/api/admin/settings')
  als.value = s.alert_settings || {}
  alsP0P1Str.value = toCSV(als.value.notify_to_p0p1)
  alsP2Str.value = toCSV(als.value.notify_to_p2)
  ;[silences.value, lines.value] = await Promise.all([
    api.get('/api/admin/silences'), api.get('/api/admin/lines'),
  ])
}

async function saveALS() {
  alsMsg.value = ''; alsOk.value = false
  try {
    const a = { ...als.value,
      notify_to_p0p1: parseCSV(alsP0P1Str.value),
      notify_to_p2: parseCSV(alsP2Str.value),
    }
    await api.put('/api/admin/settings', { alert_settings: a })
    alsMsg.value = '已保存'; alsOk.value = true
  } catch (e) { alsMsg.value = e.message }
}

async function addSilence() {
  silModal.err = ''
  if (!silModal.hours || silModal.hours <= 0) { silModal.err = '时长须大于 0'; return }
  try {
    await api.post('/api/admin/silences', {
      line_id: silModal.line_id || null,
      hours: silModal.hours,
      reason: silModal.reason,
    })
    silModal.show = false
    await load()
  } catch (e) { silModal.err = e.message }
}

async function delSilence(s) {
  await api.del('/api/admin/silences/' + s.id)
  await load()
}

onMounted(load)
</script>
