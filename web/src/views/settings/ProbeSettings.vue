<template>
  <div>
    <div class="nw-card mb-3">
      <h6>探测配置（内网组·下发到全部楼层探针）</h6>
      <div class="row g-2">
        <div class="col-md-2"><label class="form-label small mb-1">Ping 间隔(秒)</label><input v-model.number="ac.interval_ping_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">TCP 间隔(秒)</label><input v-model.number="ac.interval_tcp_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">DNS 间隔(秒)</label><input v-model.number="ac.interval_dns_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">HTTP 间隔(秒)</label><input v-model.number="ac.interval_http_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">超时(秒)</label><input v-model.number="ac.timeout_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-6"><label class="form-label small mb-1">ICMP 目标（每行一个 IP）</label><textarea v-model="icmpStr" class="form-control form-control-sm font-monospace" rows="3"></textarea></div>
        <div class="col-md-6"><label class="form-label small mb-1">TCP 目标（每行一个 host:port）</label><textarea v-model="tcpStr" class="form-control form-control-sm font-monospace" rows="3"></textarea></div>
        <div class="col-md-4"><label class="form-label small mb-1">DNS 服务器（每行一个）</label><textarea v-model="dnsStr" class="form-control form-control-sm font-monospace" rows="2"></textarea></div>
        <div class="col-md-4"><label class="form-label small mb-1">解析测试域名</label><input v-model="ac.dns_domain" class="form-control form-control-sm" /></div>
        <div class="col-md-4"><label class="form-label small mb-1">HTTP 目标（每行一个 URL）</label><textarea v-model="httpStr" class="form-control form-control-sm font-monospace" rows="2"></textarea></div>
        <div class="col-md-4"><label class="form-label small mb-1">注册令牌（探针接入用）</label><input v-model="enrollToken" class="form-control form-control-sm font-monospace" /></div>
        <div class="col-md-8 text-end align-self-end">
          <span class="text-secondary small me-2">修改后约 60 秒内下发到全部探针</span>
          <button class="btn btn-sm btn-primary" @click="saveAC">保存并下发</button>
        </div>
      </div>
      <div v-if="acMsg" class="small mt-2" :class="acOk ? 'text-success' : 'text-danger'">{{ acMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>外网探测组（阿里云等外网服务器 · 探测单位官网）</h6>
      <p class="text-secondary small mb-2">在外网服务器上安装同一探针程序并切到"外网组"后，按此配置探测：<b>ping 官网域名 + HTTPS 访问官网</b>。全部外网探针访问失败 → P1"网站从外网不可达"；HTTPS 证书剩余 &lt;15 天 → P2 提醒。</p>
      <div class="row g-2">
        <div class="col-md-4"><label class="form-label small mb-1">HTTPS 目标（每行一个 URL，即官网地址）</label><textarea v-model="extHttpStr" class="form-control form-control-sm font-monospace" rows="3" placeholder="https://www.单位域名.cn"></textarea></div>
        <div class="col-md-4"><label class="form-label small mb-1">ICMP 目标（每行一个域名或 IP）</label><textarea v-model="extIcmpStr" class="form-control form-control-sm font-monospace" rows="3" placeholder="www.单位域名.cn"></textarea></div>
        <div class="col-md-2"><label class="form-label small mb-1">Ping 间隔(秒)</label><input v-model.number="ext.interval_ping_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">HTTP 间隔(秒)</label><input v-model.number="ext.interval_http_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">超时(秒)</label><input v-model.number="ext.timeout_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-10 text-end align-self-end">
          <span class="text-secondary small me-2">保存后 30 秒内下发给全部外网组探针</span>
          <button class="btn btn-sm btn-primary" @click="saveExt">保存外网组配置</button>
        </div>
      </div>
      <div v-if="extMsg" class="small mt-2" :class="extOk ? 'text-success' : 'text-danger'">{{ extMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>内网服务监控（内→内 · 中心服务器内置探测）</h6>
      <p class="text-secondary small mb-2">类似 Uptime Kuma：中心服务器自己作为探针，监控内网服务（OA、业务系统、服务器、交换机）。每行一个目标：<code>名称,类型,目标,间隔秒</code>，类型为 tcp / http / icmp。连续 3 次失败触发 P2 告警。</p>
      <div class="d-flex align-items-center mb-2">
        <div class="form-check">
          <input id="im1" v-model="im.enabled" type="checkbox" class="form-check-input" />
          <label for="im1" class="form-check-label">启用</label>
        </div>
        <button class="btn btn-sm btn-primary ms-auto" @click="saveIM">保存</button>
      </div>
      <textarea v-model="imStr" class="form-control font-monospace" rows="5"
        placeholder="OA系统,http,http://10.8.0.20:8080/login,30&#10;核心交换机,icmp,10.8.0.1,30&#10;文件服务器,tcp,10.8.0.30:445,30"></textarea>
      <div v-if="imMsg" class="small mt-2" :class="imOk ? 'text-success' : 'text-danger'">{{ imMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>行业网监测（中心 → 行业网 · 仅 ICMP ping）</h6>
      <p class="text-secondary small mb-2">中心服务器对行业网指定 IP <b>仅做 ICMP ping</b>，不做 TCP/HTTP 等任何其他探测（以免被行业网封禁）。每行一个目标：<code>名称,IP,间隔秒</code>，IP 须为合法地址（如 10.5.x.x）。请确保中心服务器可路由到行业网网段。连续 3 次失败触发 P2"行业网不可达"，恢复自动解除。</p>
      <div class="d-flex align-items-center mb-2">
        <div class="form-check">
          <input id="ind1" v-model="ind.enabled" type="checkbox" class="form-check-input" />
          <label for="ind1" class="form-check-label">启用</label>
        </div>
        <button class="btn btn-sm btn-primary ms-auto" @click="saveInd">保存</button>
      </div>
      <textarea v-model="indStr" class="form-control font-monospace" rows="4"
        placeholder="行业网核心交换,10.5.0.1,30&#10;行业网业务主机,10.5.0.20,30"></textarea>
      <div v-if="indMsg" class="small mt-2" :class="indOk ? 'text-success' : 'text-danger'">{{ indMsg }}</div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { api } from '../../api'
import { lines, parseLines } from '../../utils'

const ac = ref({}), icmpStr = ref(''), tcpStr = ref(''), dnsStr = ref(''), httpStr = ref('')
const enrollToken = ref(''), acMsg = ref(''), acOk = ref(false)
const ext = ref({}), extHttpStr = ref(''), extIcmpStr = ref(''), extMsg = ref(''), extOk = ref(false)
const im = ref({}), imStr = ref(''), imMsg = ref(''), imOk = ref(false)
const ind = ref({}), indStr = ref(''), indMsg = ref(''), indOk = ref(false)

async function load() {
  const s = await api.get('/api/admin/settings')
  ac.value = s.agent_config || {}
  icmpStr.value = lines(ac.value.icmp_targets)
  tcpStr.value = lines(ac.value.tcp_targets)
  dnsStr.value = lines(ac.value.dns_servers)
  httpStr.value = lines(ac.value.http_targets)
  enrollToken.value = s['agent.enroll_token'] ? JSON.parse(JSON.stringify(s['agent.enroll_token'])) : ''
  ext.value = s.agent_config_external || {}
  extHttpStr.value = lines(ext.value.http_targets)
  extIcmpStr.value = lines(ext.value.icmp_targets)
  im.value = s.internal_monitors || {}
  imStr.value = (im.value.items || []).map(x => [x.name, x.type, x.target, x.interval_s].join(',')).join('\n')
  ind.value = s.industry_monitors || {}
  indStr.value = (ind.value.items || []).map(x => [x.name, x.target, x.interval_s].join(',')).join('\n')
}

function bump(v) { v.version = (v.version || 0) + 1; return v }

async function saveAC() {
  acMsg.value = ''; acOk.value = false
  try {
    const c = bump({ ...ac.value,
      icmp_targets: parseLines(icmpStr.value), tcp_targets: parseLines(tcpStr.value),
      dns_servers: parseLines(dnsStr.value), http_targets: parseLines(httpStr.value),
    })
    const body = { agent_config: c }
    if (enrollToken.value) body['agent.enroll_token'] = enrollToken.value
    await api.put('/api/admin/settings', body)
    acMsg.value = '已保存，探针将在 60 秒内应用新配置'; acOk.value = true
  } catch (e) { acMsg.value = e.message }
}

async function saveExt() {
  extMsg.value = ''; extOk.value = false
  try {
    const c = { ...ext.value,
      http_targets: parseLines(extHttpStr.value), icmp_targets: parseLines(extIcmpStr.value),
      skip_gateway: true, version: (ext.value.version || 0) + 1 }
    await api.put('/api/admin/settings', { agent_config_external: c })
    ext.value = c
    extMsg.value = '已保存'; extOk.value = true
  } catch (e) { extMsg.value = e.message }
}

async function saveIM() {
  imMsg.value = ''; imOk.value = false
  try {
    const items = imStr.value.split('\n').map(l => l.trim()).filter(l => l && !l.startsWith('#')).map(l => {
      const [name, type, target, interval] = l.split(',').map(x => (x || '').trim())
      return { name, type: type || 'tcp', target, interval_s: Number(interval) || 30 }
    }).filter(x => x.name && x.target)
    await api.put('/api/admin/settings', { internal_monitors: { enabled: im.value.enabled, items } })
    imMsg.value = `已保存 ${items.length} 个目标，30 秒内生效`; imOk.value = true
  } catch (e) { imMsg.value = e.message }
}

async function saveInd() {
  indMsg.value = ''; indOk.value = false
  try {
    const items = indStr.value.split('\n').map(l => l.trim()).filter(l => l && !l.startsWith('#')).map(l => {
      const [name, target, interval] = l.split(',').map(x => (x || '').trim())
      return { name, target, interval_s: Number(interval) || 30 }
    }).filter(x => x.name && x.target)
    await api.put('/api/admin/settings', { industry_monitors: { enabled: ind.value.enabled, items } })
    indMsg.value = `已保存 ${items.length} 个目标，30 秒内生效（仅 ICMP ping）`; indOk.value = true
  } catch (e) { indMsg.value = e.message }
}

onMounted(load)
</script>
