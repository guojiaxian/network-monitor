<template>
  <div class="container" style="max-width: 720px; padding-top: 6vh">
    <h3 class="mb-1">🛰️ NetWatch 初始化</h3>
    <p class="text-secondary">首次部署向导：创建管理员 → 邮件服务器 → 录入线路。后两步可跳过稍后在“系统设置”中完成。</p>

    <div class="nw-card mb-3">
      <h6>① 创建管理员账号</h6>
      <div class="row g-2">
        <div class="col-md-5"><input v-model="username" class="form-control" placeholder="用户名" /></div>
        <div class="col-md-5"><input v-model="password" type="password" class="form-control" placeholder="密码（至少 8 位）" /></div>
        <div class="col-md-2"><button class="btn btn-primary w-100" @click="doSetup">创建</button></div>
      </div>
      <div v-if="msg" class="text-danger small mt-2">{{ msg }}</div>
    </div>

    <div class="nw-card mb-3" :class="{ 'opacity-50': !done1 }">
      <h6>② SMTP 邮件服务器</h6>
      <div class="row g-2">
        <div class="col-md-4"><input v-model="smtp.host" class="form-control" placeholder="SMTP 服务器" :disabled="!done1" /></div>
        <div class="col-md-2"><input v-model.number="smtp.port" class="form-control" placeholder="端口" :disabled="!done1" /></div>
        <div class="col-md-3"><input v-model="smtp.username" class="form-control" placeholder="账号（可空）" :disabled="!done1" /></div>
        <div class="col-md-3"><input v-model="smtp.password" type="password" class="form-control" placeholder="密码" :disabled="!done1" /></div>
        <div class="col-md-4"><input v-model="smtp.from" class="form-control" placeholder="发件人，如 netwatch@corp.cn" :disabled="!done1" /></div>
        <div class="col-md-5"><input v-model="toStr" class="form-control" placeholder="收件人（多个用逗号分隔）" :disabled="!done1" /></div>
        <div class="col-md-3 d-flex gap-2">
          <div class="form-check align-self-center">
            <input id="ssl" v-model="smtp.use_ssl" type="checkbox" class="form-check-input" :disabled="!done1" />
            <label for="ssl" class="form-check-label">SSL(465)</label>
          </div>
          <button class="btn btn-outline-primary ms-auto" :disabled="!done1" @click="saveSMTP">保存</button>
        </div>
      </div>
      <div v-if="msg2" :class="ok2 ? 'text-success' : 'text-danger'" class="small mt-2">{{ msg2 }}</div>
    </div>

    <div class="nw-card mb-3" :class="{ 'opacity-50': !done1 }">
      <h6>③ 线路清单（CSV，可跳过）</h6>
      <p class="text-secondary small mb-1">每行：楼层名,子网CIDR,线路名,公网IP,下行Mbps,上行Mbps（后三列可省略）</p>
      <textarea v-model="csv" class="form-control font-monospace" rows="5" :disabled="!done1"
        placeholder="1楼,10.8.25.0/24,联通1号线,,1000,50&#10;2楼,10.8.26.0/24,联通2号线,,1000,50"></textarea>
      <div class="d-flex gap-2 mt-2">
        <button class="btn btn-outline-primary" :disabled="!done1 || !csv" @click="doImport">导入</button>
        <router-link to="/" class="btn btn-success ms-auto" :class="{ disabled: !done1 }">完成，进入总览 →</router-link>
      </div>
      <div v-if="msg3" class="small mt-2" :class="ok3 ? 'text-success' : 'text-danger'">{{ msg3 }}</div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { api } from '../api'

const username = ref(''), password = ref(''), msg = ref('')
const done1 = ref(false)
const smtp = ref({ host: '', port: 465, username: '', password: '', from: '', use_ssl: true, to: [] })
const toStr = ref(''), msg2 = ref(''), ok2 = ref(false)
const csv = ref(''), msg3 = ref(''), ok3 = ref(false)

async function doSetup() {
  msg.value = ''
  try {
    await api.post('/api/admin/setup', { username: username.value, password: password.value })
    done1.value = true
  } catch (e) { msg.value = e.message }
}

async function saveSMTP() {
  msg2.value = ''; ok2.value = false
  try {
    const s = { ...smtp.value, to: toStr.value.split(/[,，]/).map(x => x.trim()).filter(Boolean) }
    await api.put('/api/admin/settings', { smtp: s })
    msg2.value = '已保存，可到“系统设置”发送测试邮件验证'; ok2.value = true
  } catch (e) { msg2.value = e.message }
}

async function doImport() {
  msg3.value = ''; ok3.value = false
  try {
    const r = await fetch('/api/admin/lines/import', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'text/csv' }, body: csv.value,
    })
    const d = await r.json()
    if (!r.ok) throw new Error(d.error || '导入失败')
    msg3.value = `成功导入 ${d.imported} 条线路`; ok3.value = true
  } catch (e) { msg3.value = e.message }
}
</script>
