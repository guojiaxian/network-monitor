<template>
  <div>
    <div class="nw-card mb-3">
      <h6>报表推送计划</h6>
      <div class="row g-2 align-items-end">
        <div class="col-md-3 d-flex gap-3">
          <div class="form-check"><input id="rp1" v-model="rp.daily_enabled" type="checkbox" class="form-check-input" /><label for="rp1" class="form-check-label">每日日报</label></div>
          <div class="form-check"><input id="rp2" v-model="rp.weekly_enabled" type="checkbox" class="form-check-input" /><label for="rp2" class="form-check-label">每周周报</label></div>
        </div>
        <div class="col-md-2"><label class="form-label small mb-1">日报时间</label><input v-model="rp.daily_at" class="form-control form-control-sm" placeholder="08:00" /></div>
        <div class="col-md-2">
          <label class="form-label small mb-1">周报星期</label>
          <select v-model.number="rp.weekly_day" class="form-select form-select-sm">
            <option :value="1">周一</option><option :value="2">周二</option><option :value="3">周三</option>
            <option :value="4">周四</option><option :value="5">周五</option><option :value="6">周六</option><option :value="0">周日</option>
          </select>
        </div>
        <div class="col-md-2"><label class="form-label small mb-1">周报时间</label><input v-model="rp.weekly_at" class="form-control form-control-sm" placeholder="08:30" /></div>
        <div class="col-md-3 text-end"><button class="btn btn-sm btn-primary" @click="saveRP">保存</button></div>
      </div>
      <p class="text-secondary small mb-0 mt-2">日报/周报按计划生成并推送到通知渠道收件组；历史报表可在 <router-link to="/reports">报表</router-link> 页查看与导出。</p>
      <div v-if="rpMsg" class="small mt-2" :class="rpOk ? 'text-success' : 'text-danger'">{{ rpMsg }}</div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../../api'

const rp = ref({}), rpMsg = ref(''), rpOk = ref(false)

async function load() {
  const s = await api.get('/api/admin/settings')
  rp.value = s.report_settings || {}
}

async function saveRP() {
  rpMsg.value = ''; rpOk.value = false
  try {
    await api.put('/api/admin/settings', { report_settings: { ...rp.value } })
    rpMsg.value = '已保存'; rpOk.value = true
  } catch (e) { rpMsg.value = e.message }
}

onMounted(load)
</script>
