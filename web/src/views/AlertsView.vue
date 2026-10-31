<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <h4 class="mb-0">告警中心</h4>
      <select v-model="filter" class="form-select form-select-sm w-auto ms-3" @change="load">
        <option value="firing">进行中</option>
        <option value="resolved">已恢复</option>
        <option value="all">全部</option>
      </select>
      <button class="btn btn-sm btn-outline-secondary ms-auto" @click="load">刷新</button>
    </div>
    <div class="nw-card mb-3">
      <div class="d-flex align-items-center mb-2">
        <h6 class="mb-0">静默窗口（期间该线路不触发新告警）</h6>
        <button class="btn btn-sm btn-outline-primary ms-auto" @click="showSilence = !showSilence">{{ showSilence ? '收起' : '新增静默' }}</button>
      </div>
      <div v-if="showSilence" class="row g-2 align-items-end mb-2">
        <div class="col-md-3">
          <label class="form-label small mb-1">线路</label>
          <select v-model.number="sil.line_id" class="form-select form-select-sm">
            <option :value="0">全部线路</option>
            <option v-for="l in lines" :key="l.id" :value="l.id">{{ l.name }}</option>
          </select>
        </div>
        <div class="col-md-2"><label class="form-label small mb-1">时长（小时）</label><input v-model.number="sil.hours" type="number" min="0.5" step="0.5" class="form-control form-control-sm" /></div>
        <div class="col-md-4"><label class="form-label small mb-1">原因（如线路商割接）</label><input v-model="sil.reason" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><button class="btn btn-sm btn-primary w-100" @click="addSilence">创建</button></div>
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
    </div>

    <div class="nw-card">
      <h6>告警列表</h6>
      <table class="table nw-table table-sm align-middle">
        <thead><tr><th>级别</th><th>类型</th><th>标题</th><th>开始时间</th><th>恢复时间</th><th>状态</th></tr></thead>
        <tbody>
          <tr v-for="a in alerts" :key="a.id">
            <td><span class="badge" :class="sevCls(a.severity)">{{ a.severity }}</span></td>
            <td class="small">{{ kindText(a.kind) }}</td>
            <td>{{ a.title }}</td>
            <td class="small">{{ fmt(a.starts_at) }}</td>
            <td class="small">{{ a.ends_at ? fmt(a.ends_at) : '—' }}</td>
            <td>
              <span class="badge" :class="a.status === 'firing' ? 'bg-danger' : 'bg-success'">
                {{ a.status === 'firing' ? '进行中' : '已恢复' }}
              </span>
            </td>
          </tr>
          <tr v-if="!alerts.length"><td colspan="6" class="text-secondary text-center py-4">暂无告警</td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import dayjs from 'dayjs'
import { api } from '../api'

const alerts = ref([]), filter = ref('firing')
const silences = ref([]), lines = ref([])
const showSilence = ref(false)
const sil = ref({ line_id: 0, hours: 2, reason: '' })

const kinds = { line_down: '线路中断', floor_down: '楼层中断', blind: '监控盲区', degraded: '线路降质', speed_low: '带宽不达标', probe_conflict: '探针数据冲突', site_down: '网站外网不可达', site_conflict: '外网探针冲突', cert_expiring: '证书即将过期', monitor_down: '内网服务不可达' }
function kindText(k) { return kinds[k] || k }
function sevCls(s) { return { P0: 'bg-dark', P1: 'bg-danger', P2: 'bg-warning text-dark' }[s] || 'bg-secondary' }
function fmt(t) { return dayjs(t).format('YYYY-MM-DD HH:mm:ss') }

async function load() {
  ;[alerts.value, silences.value, lines.value] = await Promise.all([
    api.get(`/api/admin/alerts?status=${filter.value}&limit=300`),
    api.get('/api/admin/silences'),
    api.get('/api/admin/lines'),
  ])
}

async function addSilence() {
  try {
    await api.post('/api/admin/silences', {
      line_id: sil.value.line_id || null,
      hours: sil.value.hours,
      reason: sil.value.reason,
    })
    showSilence.value = false
    await load()
  } catch (e) { alert(e.message) }
}

async function delSilence(s) {
  await api.del('/api/admin/silences/' + s.id)
  await load()
}

onMounted(load)
</script>
