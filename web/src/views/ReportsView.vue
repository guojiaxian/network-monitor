<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <h4 class="mb-0">报表</h4>
      <select v-model="kind" class="form-select form-select-sm w-auto ms-3" @change="load">
        <option value="all">全部</option>
        <option value="day">日报</option>
        <option value="week">周报</option>
      </select>
      <button class="btn btn-sm btn-outline-primary ms-auto" @click="gen('day')">生成日报（近24h）</button>
      <button class="btn btn-sm btn-outline-primary ms-2" @click="gen('week')">生成周报（近7天）</button>
    </div>

    <div class="nw-card mb-3">
      <table class="table nw-table table-sm align-middle">
        <thead><tr><th>类型</th><th>统计周期</th><th>生成时间</th><th style="width:220px"></th></tr></thead>
        <tbody>
          <tr v-for="r in reports" :key="r.id">
            <td><span class="badge" :class="r.kind === 'week' ? 'bg-primary' : 'bg-secondary'">{{ r.kind === 'week' ? '周报' : '日报' }}</span></td>
            <td>{{ fmt(r.period_start) }} ~ {{ fmt(r.period_end) }}</td>
            <td class="small text-secondary">{{ fmt(r.created_at) }}</td>
            <td class="text-end">
              <button class="btn btn-sm btn-outline-primary me-1" @click="view(r)">查看</button>
              <a class="btn btn-sm btn-outline-success" :href="'#/reports'" @click.prevent="exportCSV(r)">导出 CSV</a>
            </td>
          </tr>
          <tr v-if="!reports.length"><td colspan="4" class="text-secondary text-center py-4">暂无报表，可手动生成；每日/每周也会按设置自动生成并邮件推送</td></tr>
        </tbody>
      </table>
    </div>

    <div v-if="detail" class="nw-card">
      <div class="d-flex mb-2">
        <h6 class="mb-0">{{ detail.kind === 'week' ? '周报' : '日报' }}：{{ detail.start }} ~ {{ detail.end }}</h6>
        <button class="btn-close ms-auto" @click="detail = null"></button>
      </div>
      <table class="table nw-table table-sm">
        <thead><tr><th>线路</th><th>楼层</th><th>可用率</th><th>平均时延</th><th>平均丢包</th><th>测速峰值</th><th>测速均值</th><th>告警次数</th></tr></thead>
        <tbody>
          <tr v-for="l in detail.lines" :key="l.name">
            <td>{{ l.name }}</td><td>{{ l.floor || '—' }}</td>
            <td :style="{ color: color(l.avail_pct), fontWeight: 'bold' }">{{ pct(l.avail_pct) }}</td>
            <td>{{ v(l.avg_rtt_ms, 1) }} ms</td>
            <td>{{ v(l.avg_loss_pct, 2) }} %</td>
            <td>{{ v(l.speed_max_mbps, 0) }} Mbps</td>
            <td>{{ v(l.speed_avg_mbps, 0) }} Mbps</td>
            <td>{{ l.alert_count }}</td>
          </tr>
        </tbody>
      </table>
      <p class="text-secondary small mb-0">生成时间：{{ detail.generated }}</p>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import dayjs from 'dayjs'
import { api } from '../api'

const reports = ref([]), detail = ref(null), kind = ref('all'), msg = ref('')

const fmt = (t) => dayjs(t).format('YYYY-MM-DD HH:mm')
const pct = (v) => v == null ? '无数据' : Number(v).toFixed(2) + '%'
const v = (val, d) => val == null ? '—' : Number(val).toFixed(d)
const color = (p) => (p == null ? '#999' : p < 99 ? '#dc3545' : '#05c96b')

async function load() {
  reports.value = await api.get(`/api/admin/reports?kind=${kind.value}`)
}

async function view(r) {
  detail.value = await api.get('/api/admin/reports/' + r.id)
}

function exportCSV(r) {
  const w = window.open(`/api/admin/reports/${r.id}/export.csv`, '_blank')
  if (w) w.opener = null
}

async function gen(k) {
  msg.value = ''
  try {
    await api.post('/api/admin/reports/generate', { kind: k })
    await load()
  } catch (e) { msg.value = e.message }
}

onMounted(load)
</script>
