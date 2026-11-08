<template>
  <div>
    <div class="nw-card mb-3">
      <h6>邮件服务器（SMTP）</h6>
      <div class="row g-2">
        <div class="col-md-4"><label class="form-label small mb-1">服务器</label><input v-model="smtp.host" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">端口</label><input v-model.number="smtp.port" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-3"><label class="form-label small mb-1">账号</label><input v-model="smtp.username" class="form-control form-control-sm" /></div>
        <div class="col-md-3"><label class="form-label small mb-1">密码</label><input v-model="smtp.password" type="password" class="form-control form-control-sm" /></div>
        <div class="col-md-4"><label class="form-label small mb-1">发件人</label><input v-model="smtp.from" class="form-control form-control-sm" /></div>
        <div class="col-md-5"><label class="form-label small mb-1">默认收件人（逗号分隔）</label><input v-model="smtpToStr" class="form-control form-control-sm" /></div>
        <div class="col-md-3 d-flex align-items-end gap-3">
          <div class="form-check">
            <input id="s2" v-model="smtp.use_ssl" type="checkbox" class="form-check-input" /><label for="s2" class="form-check-label">SSL</label>
          </div>
          <button class="btn btn-sm btn-primary ms-auto" @click="saveSMTP">保存</button>
          <button class="btn btn-sm btn-outline-primary" @click="testSMTP">测试邮件</button>
        </div>
      </div>
      <div v-if="smtpMsg" class="small mt-2" :class="smtpOk ? 'text-success' : 'text-danger'">{{ smtpMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>IM 告警通道（钉钉 / 企业微信群机器人）</h6>
      <div class="row g-2">
        <div class="col-md-2 d-flex align-items-center">
          <div class="form-check"><input id="nc1" v-model="nc.enabled" type="checkbox" class="form-check-input" /><label for="nc1" class="form-check-label">启用推送</label></div>
        </div>
        <div class="col-md-5"><label class="form-label small mb-1">钉钉机器人 Webhook</label><input v-model="nc.dingtalk_webhook" class="form-control form-control-sm" placeholder="https://oapi.dingtalk.com/robot/send?access_token=..." /></div>
        <div class="col-md-3"><label class="form-label small mb-1">钉钉加签密钥（可空）</label><input v-model="nc.dingtalk_secret" class="form-control form-control-sm" placeholder="SEC..." /></div>
        <div class="col-md-2 text-end"><button class="btn btn-sm btn-outline-primary" @click="testNC('dingtalk')">测试钉钉</button></div>
        <div class="col-md-5"><label class="form-label small mb-1">企业微信机器人 Webhook</label><input v-model="nc.wecom_webhook" class="form-control form-control-sm" placeholder="https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=..." /></div>
        <div class="col-md-3 d-flex align-items-center">
          <div class="form-check"><input id="nc2" v-model="nc.send_p2" type="checkbox" class="form-check-input" /><label for="nc2" class="form-check-label">P2 告警也推送</label></div>
        </div>
        <div class="col-md-2 text-end align-self-end"><button class="btn btn-sm btn-primary" @click="saveNC">保存</button></div>
        <div class="col-md-2 text-end align-self-end"><button class="btn btn-sm btn-outline-primary" @click="testNC('wecom')">测试企微</button></div>
      </div>
      <p class="text-secondary small mb-0 mt-2">P0/P1 告警与恢复通知恒推送；P2（降质/带宽不达标）按上方开关。与邮件告警相互独立，可同时启用。</p>
      <div v-if="ncMsg" class="small mt-2" :class="ncOk ? 'text-success' : 'text-danger'">{{ ncMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>日志外发（Syslog → 日志审计服务器）</h6>
      <div class="row g-2">
        <div class="col-md-2 d-flex align-items-center">
          <div class="form-check"><input id="sl1" v-model="sl.enabled" type="checkbox" class="form-check-input" /><label for="sl1" class="form-check-label">启用外发</label></div>
        </div>
        <div class="col-md-2"><label class="form-label small mb-1">审计服务器地址</label><input v-model="sl.host" class="form-control form-control-sm" placeholder="如 10.0.0.9" /></div>
        <div class="col-md-1"><label class="form-label small mb-1">端口</label><input v-model.number="sl.port" type="number" class="form-control form-control-sm" placeholder="514" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">协议</label>
          <select v-model="sl.proto" class="form-select form-select-sm"><option value="udp">UDP</option><option value="tcp">TCP</option></select>
        </div>
        <div class="col-md-2"><label class="form-label small mb-1">格式</label>
          <select v-model="sl.format" class="form-select form-select-sm"><option value="cef">CEF</option><option value="kv">KV 键值对</option></select>
        </div>
        <div class="col-md-1"><label class="form-label small mb-1">超时(秒)</label><input v-model.number="sl.timeout_s" type="number" class="form-control form-control-sm" placeholder="5" /></div>
        <div class="col-md-2 d-flex align-items-end gap-2">
          <button class="btn btn-sm btn-outline-primary flex-fill" @click="testSL">测试发送</button>
          <button class="btn btn-sm btn-primary flex-fill" @click="saveSL">保存</button>
        </div>
        <div class="col-12 d-flex flex-wrap gap-3 small mt-1">
          <div class="form-check"><input id="sl2" v-model="sl.send_alerts" type="checkbox" class="form-check-input" /><label for="sl2" class="form-check-label">告警触发/恢复</label></div>
          <div class="form-check"><input id="sl3" v-model="sl.send_speed" type="checkbox" class="form-check-input" /><label for="sl3" class="form-check-label">测速结果</label></div>
          <div class="form-check"><input id="sl4" v-model="sl.send_scan" type="checkbox" class="form-check-input" /><label for="sl4" class="form-check-label">IP 扫描汇总</label></div>
          <div class="form-check"><input id="sl5" v-model="sl.send_egress" type="checkbox" class="form-check-input" /><label for="sl5" class="form-check-label">出口 IP 变化</label></div>
        </div>
      </div>
      <p class="text-secondary small mb-0 mt-2">将监测事件以标准 syslog 转发至日志审计服务器（奇安信等，默认 UDP 514）。CEF=RFC5424 头 + CEF 扩展（SIEM 标准格式），KV=key=value 格式，可按审计平台要求切换；对接时用「测试发送」与平台侧核对解析。被静默窗口抑制的告警不外发（与邮件/IM 一致）。</p>
      <div v-if="slMsg" class="small mt-2" :class="slOk ? 'text-success' : 'text-danger'">{{ slMsg }}</div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../../api'
import { toCSV, parseCSV } from '../../utils'

const smtp = ref({}), smtpToStr = ref(''), smtpMsg = ref(''), smtpOk = ref(false)
const nc = ref({}), ncMsg = ref(''), ncOk = ref(false)
const sl = ref({}), slMsg = ref(''), slOk = ref(false)

async function load() {
  const s = await api.get('/api/admin/settings')
  smtp.value = s.smtp || {}
  smtpToStr.value = toCSV(smtp.value.to)
  nc.value = s.notify_channels || {}
  sl.value = s.syslog_settings || {}
}

async function saveSMTP() {
  smtpMsg.value = ''; smtpOk.value = false
  try {
    const s = { ...smtp.value, to: parseCSV(smtpToStr.value) }
    await api.put('/api/admin/settings', { smtp: s })
    smtpMsg.value = '已保存'; smtpOk.value = true
  } catch (e) { smtpMsg.value = e.message }
}

async function testSMTP() {
  smtpMsg.value = ''; smtpOk.value = false
  try {
    await api.post('/api/admin/settings/smtp/test', {})
    smtpMsg.value = '测试邮件已发送，请查收'; smtpOk.value = true
  } catch (e) { smtpMsg.value = e.message }
}

async function saveNC() {
  ncMsg.value = ''; ncOk.value = false
  try {
    await api.put('/api/admin/settings', { notify_channels: { ...nc.value } })
    ncMsg.value = '已保存'; ncOk.value = true
  } catch (e) { ncMsg.value = e.message }
}

async function testNC(channel) {
  ncMsg.value = ''; ncOk.value = false
  try {
    await api.put('/api/admin/settings', { notify_channels: { ...nc.value } })
    await api.post('/api/admin/settings/notify/test', { channel })
    ncMsg.value = '测试消息已发送，请到群里查收'; ncOk.value = true
  } catch (e) { ncMsg.value = e.message }
}

async function saveSL() {
  slMsg.value = ''; slOk.value = false
  try {
    await api.put('/api/admin/settings', { syslog_settings: { ...sl.value } })
    slMsg.value = '已保存'; slOk.value = true
  } catch (e) { slMsg.value = e.message }
}

async function testSL() {
  slMsg.value = ''; slOk.value = false
  try {
    await api.put('/api/admin/settings', { syslog_settings: { ...sl.value } })
    await api.post('/api/admin/settings/syslog/test', {})
    slMsg.value = '测试日志已发送，请到审计平台查收'; slOk.value = true
  } catch (e) { slMsg.value = e.message }
}

onMounted(load)
</script>
