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
      <div class="row g-2 align-items-end mt-1">
        <div class="col-md-3"><div class="form-check"><input id="rp3" v-model="rp.monthly_enabled" type="checkbox" class="form-check-input" /><label for="rp3" class="form-check-label">每月月报（出上一自然月）</label></div></div>
        <div class="col-md-2"><label class="form-label small mb-1">月报日期（几号）</label><input v-model.number="rp.monthly_day" type="number" min="1" max="28" class="form-control form-control-sm" placeholder="1" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">月报时间</label><input v-model="rp.monthly_at" class="form-control form-control-sm" placeholder="08:00" /></div>
        <div class="col-md-4"><label class="form-label small mb-1">单位名称（报告封面与正文用）</label><input v-model="rp.org_name" class="form-control form-control-sm" placeholder="如：××单位信息中心" /></div>
      </div>
      <p class="text-secondary small mb-0 mt-2">日报/周报/月报按计划生成（统计口径为自然日/自然周/自然月）并推送到通知渠道收件组；历史报表可在 <router-link to="/reports">报表</router-link> 页查看与导出。</p>
      <div v-if="rpMsg" class="small mt-2" :class="rpOk ? 'text-success' : 'text-danger'">{{ rpMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>报告模板（导出 Word 用）</h6>
      <p class="text-secondary small mb-2">报表页「导出 Word」按此模板生成严谨版式的运维报告（标题层级/表格样式/页码由系统内置）。语法：<code># 封面标题</code>、<code>## 一级标题</code>、<code>### 二级标题</code>、<code v-pre>{{表格:名称}}</code> 独占一行插入数据表、<code v-pre>{{分页}}</code> 分页、其余行为正文段落；正文内 <code v-pre>{{变量}}</code> 会被替换。</p>
      <div class="d-flex align-items-center mb-2 gap-2">
        <select v-model="tkind" class="form-select form-select-sm w-auto">
          <option value="day">日报模板</option>
          <option value="week">周报模板</option>
          <option value="month">月报模板</option>
        </select>
        <button class="btn btn-sm btn-outline-secondary" @click="restoreDefault">恢复默认</button>
        <button class="btn btn-sm btn-primary ms-auto" @click="saveTmpl">保存模板</button>
      </div>
      <textarea v-model="curTmpl" rows="22" class="form-control form-control-sm font-monospace small" spellcheck="false"></textarea>

      <details class="mt-2">
        <summary class="small text-secondary" style="cursor:pointer">可用变量与表格速查（点击展开）</summary>
        <div class="row g-3 mt-1">
          <div class="col-md-6">
            <div class="small fw-bold mb-1">行内变量</div>
            <table class="table table-sm small mb-0"><tbody>
              <tr v-for="d in docs.vars" :key="d.name"><td class="text-nowrap"><code v-pre>{{</code><code>{{ d.name }}</code><code v-pre>}}</code></td><td class="text-secondary">{{ d.desc }}</td></tr>
            </tbody></table>
          </div>
          <div class="col-md-6">
            <div class="small fw-bold mb-1">数据表格（整行独占）</div>
            <table class="table table-sm small mb-0"><tbody>
              <tr v-for="d in docs.tables" :key="d.name"><td class="text-nowrap"><code v-pre>{{</code><code>表格:{{ d.name }}</code><code v-pre>}}</code></td><td class="text-secondary">{{ d.desc }}</td></tr>
            </tbody></table>
          </div>
        </div>
      </details>
      <div v-if="tMsg" class="small mt-2" :class="tOk ? 'text-success' : 'text-danger'">{{ tMsg }}</div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../../api'

const rp = ref({}), rpMsg = ref(''), rpOk = ref(false)
const tmpls = ref({}), tkind = ref('day'), tMsg = ref(''), tOk = ref(false)
const docs = ref({ vars: [], tables: [] })

// 当前编辑的模板文本（三份模板分开保存）
const curTmpl = computed({
  get: () => tmpls.value[tkind.value] || '',
  set: (v) => { tmpls.value[tkind.value] = v },
})

async function load() {
  const s = await api.get('/api/admin/settings')
  rp.value = s.report_settings || {}
  tmpls.value = s.report_templates || {}
  // 速查数据来自后端（默认模板接口同时返回变量/表格字典，保持单一来源）
  api.get('/api/admin/reports/template-default?kind=day').then((d) => {
    docs.value = { vars: d.vars || [], tables: d.tables || [] }
    // 后端未配置模板时 GET settings 已回填默认；此处兜底再补一次
    for (const k of ['day', 'week', 'month']) {
      if (!tmpls.value[k]) tmpls.value[k] = d.text
    }
  })
}

async function saveRP() {
  rpMsg.value = ''; rpOk.value = false
  try {
    await api.put('/api/admin/settings', { report_settings: { ...rp.value } })
    rpMsg.value = '已保存'; rpOk.value = true
  } catch (e) { rpMsg.value = e.message }
}

async function saveTmpl() {
  tMsg.value = ''; tOk.value = false
  try {
    await api.put('/api/admin/settings', { report_templates: { ...tmpls.value } })
    tMsg.value = '模板已保存，可在报表页生成验证'; tOk.value = true
  } catch (e) { tMsg.value = e.message }
}

async function restoreDefault() {
  tMsg.value = ''; tOk.value = false
  try {
    const d = await api.get(`/api/admin/reports/template-default?kind=${tkind.value}`)
    curTmpl.value = d.text
    tMsg.value = '已回填内置默认模板，点击「保存模板」后生效'; tOk.value = true
  } catch (e) { tMsg.value = e.message }
}

onMounted(load)
</script>
