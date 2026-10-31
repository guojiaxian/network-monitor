<template>
  <div>
    <h4 class="mb-3">系统设置</h4>

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
      <h6>告警阈值</h6>
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
      <h6>报表推送</h6>
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
      <div v-if="rpMsg" class="small mt-2" :class="rpOk ? 'text-success' : 'text-danger'">{{ rpMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>用户管理</h6>
      <table class="table nw-table table-sm align-middle mb-2">
        <thead><tr><th>用户名</th><th>角色</th><th style="width:100px"></th></tr></thead>
        <tbody>
          <tr v-for="u in users" :key="u.id">
            <td>{{ u.username }}</td>
            <td><span class="badge" :class="u.role === 'admin' ? 'bg-dark' : 'bg-secondary'">{{ u.role === 'admin' ? '管理员' : '观察者' }}</span></td>
            <td class="text-end"><button class="btn btn-sm btn-outline-danger" @click="delUser(u)">删除</button></td>
          </tr>
        </tbody>
      </table>
      <div class="row g-2 align-items-end">
        <div class="col-md-3"><input v-model="nu.username" class="form-control form-control-sm" placeholder="用户名" /></div>
        <div class="col-md-3"><input v-model="nu.password" type="password" class="form-control form-control-sm" placeholder="密码（≥8位）" /></div>
        <div class="col-md-2">
          <select v-model="nu.role" class="form-select form-select-sm">
            <option value="viewer">观察者（只读）</option>
            <option value="admin">管理员</option>
          </select>
        </div>
        <div class="col-md-2"><button class="btn btn-sm btn-primary w-100" @click="addUser">新增用户</button></div>
      </div>
      <div v-if="userMsg" class="small mt-2" :class="userOk ? 'text-success' : 'text-danger'">{{ userMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>操作审计日志（最近 100 条）</h6>
      <table class="table nw-table table-sm">
        <thead><tr><th style="width:160px">时间</th><th style="width:120px">用户</th><th>操作</th><th>结果</th></tr></thead>
        <tbody>
          <tr v-for="a in audits" :key="a.id">
            <td class="small text-secondary">{{ fmt(a.created_at) }}</td>
            <td>{{ a.username }}</td>
            <td><code class="small">{{ a.action }}</code></td>
            <td class="small" :style="{ color: a.detail.includes('status=2') ? '#05c96b' : '#dc3545' }">{{ a.detail }}</td>
          </tr>
          <tr v-if="!audits.length"><td colspan="4" class="text-secondary small">暂无记录（变更操作会自动记录）</td></tr>
        </tbody>
      </table>
    </div>

    <div class="nw-card mb-3">
      <h6>测速计划</h6>
      <div class="row g-2">
        <div class="col-md-3 d-flex align-items-center">
          <div class="form-check">
            <input id="sp1" v-model="sp.enabled" type="checkbox" class="form-check-input" />
            <label for="sp1" class="form-check-label">启用定时测速</label>
          </div>
        </div>
        <div class="col-md-2">
          <label class="form-label small mb-1">引擎</label>
          <select v-model="sp.engine" class="form-select form-select-sm">
            <option value="ookla">Ookla 公共节点</option>
            <option value="http">HTTP 大文件下载</option>
          </select>
        </div>
        <div class="col-md-2"><label class="form-label small mb-1">每线间隔(分钟)</label><input v-model.number="sp.interval_min" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">单次超时(分钟)</label><input v-model.number="sp.timeout_min" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-3 text-end align-self-end"><button class="btn btn-sm btn-primary" @click="saveSP">保存</button></div>
        <div class="col-12">
          <label class="form-label small mb-1">HTTP 引擎下载源（每行一个 URL，按序尝试）</label>
          <textarea v-model="spUrlStr" class="form-control form-control-sm font-monospace" rows="3"></textarea>
        </div>
      </div>
      <p class="text-secondary small mb-0 mt-2">全局串行调度：同一时刻只有一条线路在测速；测速结果低于契约带宽 70%（连续 2 次）将触发 P2 告警。</p>
      <div v-if="spMsg" class="small mt-2" :class="spOk ? 'text-success' : 'text-danger'">{{ spMsg }}</div>
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
      <h6>SNMP 真实流量采集（可选，需防火墙开通只读 SNMP）</h6>
      <div class="row g-2">
        <div class="col-md-2 d-flex align-items-center">
          <div class="form-check"><input id="sn1" v-model="sn.enabled" type="checkbox" class="form-check-input" /><label for="sn1" class="form-check-label">启用</label></div>
        </div>
        <div class="col-md-3"><label class="form-label small mb-1">防火墙管理 IP</label><input v-model="sn.device" class="form-control form-control-sm" placeholder="如 10.8.0.1" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">只读团体字</label><input v-model="sn.community" class="form-control form-control-sm" placeholder="public" /></div>
        <div class="col-md-1"><label class="form-label small mb-1">端口</label><input v-model.number="sn.port" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">轮询间隔(秒)</label><input v-model.number="sn.interval_s" type="number" class="form-control form-control-sm" /></div>
        <div class="col-md-2 d-flex gap-2 align-items-end">
          <button class="btn btn-sm btn-outline-primary flex-fill" @click="listIfaces">获取接口列表</button>
          <button class="btn btn-sm btn-primary flex-fill" @click="saveSN">保存</button>
        </div>
        <div class="col-12" v-if="ifaces.length">
          <label class="form-label small mb-1">接口映射（每行：线路名 = 接口）</label>
          <div class="row g-1">
            <div class="col-md-4" v-for="ifc in ifaces" :key="ifc.index">
              <div class="input-group input-group-sm">
                <span class="input-group-text" style="min-width:110px" :title="'ifIndex ' + ifc.index">{{ ifc.descr || ('接口' + ifc.index) }}</span>
                <select class="form-select" :value="mappingOf(ifc.index)" @change="setMapping(ifc.index, ifc.descr, $event.target.value)">
                  <option value="">未映射</option>
                  <option v-for="l in allLines" :key="l.id" :value="l.id">{{ l.name }}</option>
                </select>
              </div>
            </div>
          </div>
        </div>
      </div>
      <p class="text-secondary small mb-0 mt-2">启用后每 30 秒轮询防火墙 WAN 口字节计数器，得到每线真实流量曲线（零测试流量成本，与测速互补）；映射改动保存后下轮生效。</p>
      <div v-if="snMsg" class="small mt-2" :class="snOk ? 'text-success' : 'text-danger'">{{ snMsg }}</div>
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
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import dayjs from 'dayjs'
import { api } from '../api'

const smtp = ref({}), smtpToStr = ref(''), smtpMsg = ref(''), smtpOk = ref(false)
const als = ref({}), alsToStr = ref(''), alsP0P1Str = ref(''), alsP2Str = ref(''), alsMsg = ref(''), alsOk = ref(false)
const rp = ref({}), rpMsg = ref(''), rpOk = ref(false)
const nc = ref({}), ncMsg = ref(''), ncOk = ref(false)
const sn = ref({}), ifaces = ref([]), allLines = ref([]), snMsg = ref(''), snOk = ref(false)
const ext = ref({}), extHttpStr = ref(''), extIcmpStr = ref(''), extMsg = ref(''), extOk = ref(false)
const im = ref({}), imStr = ref(''), imMsg = ref(''), imOk = ref(false)
const users = ref([]), nu = ref({ username: '', password: '', role: 'viewer' }), userMsg = ref(''), userOk = ref(false)
const audits = ref([])
const sp = ref({}), spUrlStr = ref(''), spMsg = ref(''), spOk = ref(false)
const ac = ref({}), icmpStr = ref(''), tcpStr = ref(''), dnsStr = ref(''), httpStr = ref('')
const enrollToken = ref(''), acMsg = ref(''), acOk = ref(false)

const lines = (s) => (s || []).join('\n')
const parseLines = (s) => s.split('\n').map(x => x.trim()).filter(Boolean)
const toCSV = (a) => (a || []).join(', ')

async function load() {
  const s = await api.get('/api/admin/settings')
  smtp.value = s.smtp || {}
  smtpToStr.value = toCSV(smtp.value.to)
  als.value = s.alert_settings || {}
  alsToStr.value = toCSV(als.value.notify_to)
  alsP0P1Str.value = toCSV(als.value.notify_to_p0p1)
  alsP2Str.value = toCSV(als.value.notify_to_p2)
  rp.value = s.report_settings || {}
  nc.value = s.notify_channels || {}
  sn.value = s.snmp_settings || {}
  ext.value = s.agent_config_external || {}
  extHttpStr.value = lines(ext.value.http_targets)
  extIcmpStr.value = lines(ext.value.icmp_targets)
  im.value = s.internal_monitors || {}
  imStr.value = (im.value.items || []).map(x => [x.name, x.type, x.target, x.interval_s].join(',')).join('\n')
  allLines.value = await api.get('/api/admin/lines')
  sp.value = s.speed_settings || {}
  spUrlStr.value = lines(sp.value.http_urls)
  ac.value = s.agent_config || {}
  icmpStr.value = lines(ac.value.icmp_targets)
  tcpStr.value = lines(ac.value.tcp_targets)
  dnsStr.value = lines(ac.value.dns_servers)
  httpStr.value = lines(ac.value.http_targets)
  enrollToken.value = s['agent.enroll_token'] ? JSON.parse(JSON.stringify(s['agent.enroll_token'])) : ''
  users.value = await api.get('/api/admin/users')
  audits.value = await api.get('/api/admin/audit?limit=100')
}

function bump(v) { v.version = (v.version || 0) + 1; return v }

async function saveSMTP() {
  smtpMsg.value = ''; smtpOk.value = false
  try {
    const s = { ...smtp.value, to: smtpToStr.value.split(/[,，]/).map(x => x.trim()).filter(Boolean) }
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

async function saveALS() {
  alsMsg.value = ''; alsOk.value = false
  try {
    const a = { ...als.value,
      notify_to: alsToStr.value.split(/[,，]/).map(x => x.trim()).filter(Boolean),
      notify_to_p0p1: alsP0P1Str.value.split(/[,，]/).map(x => x.trim()).filter(Boolean),
      notify_to_p2: alsP2Str.value.split(/[,，]/).map(x => x.trim()).filter(Boolean),
    }
    await api.put('/api/admin/settings', { alert_settings: a })
    alsMsg.value = '已保存'; alsOk.value = true
  } catch (e) { alsMsg.value = e.message }
}

async function saveRP() {
  rpMsg.value = ''; rpOk.value = false
  try {
    await api.put('/api/admin/settings', { report_settings: { ...rp.value } })
    rpMsg.value = '已保存'; rpOk.value = true
  } catch (e) { rpMsg.value = e.message }
}

const fmt = (t) => dayjs(t).format('MM-DD HH:mm:ss')

async function addUser() {
  userMsg.value = ''; userOk.value = false
  try {
    await api.post('/api/admin/users', { ...nu.value })
    userMsg.value = '已创建'; userOk.value = true
    nu.value = { username: '', password: '', role: 'viewer' }
    users.value = await api.get('/api/admin/users')
  } catch (e) { userMsg.value = e.message }
}

async function delUser(u) {
  if (!confirm(`删除用户 ${u.username}？`)) return
  try {
    await api.del('/api/admin/users/' + u.id)
    users.value = await api.get('/api/admin/users')
  } catch (e) { alert(e.message) }
}

async function saveSP() {
  spMsg.value = ''; spOk.value = false
  try {
    const s = { ...sp.value, http_urls: parseLines(spUrlStr.value) }
    await api.put('/api/admin/settings', { speed_settings: s })
    spMsg.value = '已保存'; spOk.value = true
  } catch (e) { spMsg.value = e.message }
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

function mappingOf(idx) {
  const m = (sn.value.mappings || []).find(x => x.ifindex === idx)
  return m ? m.line_id : ''
}
function setMapping(idx, descr, lineId) {
  const ms = (sn.value.mappings || []).filter(x => x.ifindex !== idx)
  if (lineId) {
    const l = allLines.value.find(x => x.id === Number(lineId))
    if (l) ms.push({ line_id: l.id, line_name: l.name, ifindex: idx, ifdescr: descr || '' })
  }
  sn.value = { ...sn.value, mappings: ms }
}

async function listIfaces() {
  snMsg.value = ''; snOk.value = false
  try {
    await api.put('/api/admin/settings', { snmp_settings: { ...sn.value } })
    ifaces.value = await api.get('/api/admin/snmp/ifaces')
    snMsg.value = `获取到 ${ifaces.value.length} 个接口，请完成映射后保存`; snOk.value = true
  } catch (e) { snMsg.value = e.message }
}

async function saveSN() {
  snMsg.value = ''; snOk.value = false
  try {
    await api.put('/api/admin/settings', { snmp_settings: { ...sn.value } })
    snMsg.value = '已保存'; snOk.value = true
  } catch (e) { snMsg.value = e.message }
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

onMounted(load)
</script>
