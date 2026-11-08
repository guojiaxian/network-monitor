<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <div>
        <h4 class="mb-0">探针状态</h4>
        <div class="text-secondary small mt-1">每 15 秒自动刷新<span v-if="refreshMsg" class="text-success ms-2">{{ refreshMsg }}</span><span v-if="err" class="text-danger ms-2">{{ err }}</span></div>
      </div>
      <button class="nw-btn nw-btn-accent nw-btn-spin ms-auto" :disabled="refreshing" @click="refreshNow"
        title="命令全部探针立即重探出口 IP 并排程一轮测速（下一心跳 ≤15s 回报，约 1 分钟内开测）">
        <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M11.534 7h3.932a.25.25 0 0 1 .192.41l-1.966 2.36a.25.25 0 0 1-.384 0l-1.966-2.36a.25.25 0 0 1 .192-.41zm-11 2h3.932a.25.25 0 0 0 .192-.41L2.692 6.23a.25.25 0 0 0-.384 0L.342 8.59A.25.25 0 0 0 .534 9z"/><path fill-rule="evenodd" d="M8 3c-1.552 0-2.94.707-3.857 1.818a.5.5 0 1 1-.771-.636A6.002 6.002 0 0 1 13.917 7H12.9A5.002 5.002 0 0 0 8 3zM3.1 9a5.002 5.002 0 0 0 8.757 2.182.5.5 0 1 1 .771.636A6.002 6.002 0 0 1 2.083 9H3.1z"/></svg>{{ refreshing ? '下发中…' : '立即探测' }}
      </button>
      <router-link to="/probes/manage" class="nw-btn nw-btn-neutral ms-2">
        <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M12.146.146a.5.5 0 0 1 .708 0l3 3a.5.5 0 0 1 0 .708l-10 10a.5.5 0 0 1-.168.11l-5 2a.5.5 0 0 1-.65-.65l2-5a.5.5 0 0 1 .11-.168l10-10zM11.207 2.5 13.5 4.793 14.793 3.5 12.5 1.207 11.207 2.5zm1.586 3L10.5 3.207 4 9.707V10h.5a.5.5 0 0 1 .5.5v.5h.5a.5.5 0 0 1 .5.5v.5h.293l6.5-6.5zm-9.761 5.175-.106.106-1.528 3.821 3.821-1.528.106-.106A.5.5 0 0 1 5 12.5V12h-.5a.5.5 0 0 1-.5-.5V11h-.5a.5.5 0 0 1-.468-.325z"/></svg>绑定 / 运维操作 → 探针配置
      </router-link>
    </div>

    <div class="nw-card mb-3">
      <!-- table-layout:fixed 固定列宽：展开行出现/收起时列表不再左右跳动 -->
      <table class="table nw-table table-sm align-middle table-fixed">
        <thead>
          <tr>
            <th style="width:3.5%"></th><th style="width:17%">名称</th>
            <th style="width:13%">绑定线路</th><th style="width:9%">状态</th><th style="width:13.5%">自健康 (CPU/内存/磁盘)</th>
            <th style="width:15%">出口公网 IP</th><th style="width:10%">出口源 IP</th><th style="width:10%">默认网关</th><th style="width:9%">最后心跳</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="p in probes" :key="p.probe.id">
            <tr>
              <td><a href="javascript:void(0)" class="text-decoration-none" @click="toggle(p.probe.id)">{{ expanded[p.probe.id] ? '▾' : '▸' }}</a></td>
              <td><b>{{ p.probe.name || p.probe.hostname }}</b> <code class="small text-secondary" :title="p.probe.id">{{ p.probe.id.slice(0, 8) }}</code></td>
              <td>{{ lineName(p) }}</td>
              <td><StatusBadge :status="p.status && p.status.status || 'unknown'" /></td>
              <td>
                <span v-if="health(p)" class="small">
                  <span :class="p.status.cpu_pct > 90 ? 'text-danger' : ''">{{ Math.round(p.status.cpu_pct) }}%</span> /
                  <span :class="p.status.mem_pct > 90 ? 'text-danger' : ''">{{ Math.round(p.status.mem_pct) }}%</span> /
                  <span :class="p.status.disk_pct > 90 ? 'text-danger' : ''">{{ Math.round(p.status.disk_pct) }}%</span>
                  <span v-if="clockOff(p)" class="text-warning ms-1" :title="'探针时钟偏移 ' + Math.round(p.status.clock_offset_ms / 1000) + ' 秒，请为其配置 NTP'">⏰偏移</span>
                </span>
                <span v-else class="text-secondary small">—</span>
              </td>
              <td>
                <code>{{ p.status && p.status.egress_ip || '—' }}</code>
                <span v-if="p.status && p.status.egress_isp" class="text-secondary small ms-1" :title="p.status.egress_geo">{{ p.status.egress_isp }}</span>
                <span v-if="p.status && p.status.egress_ipv6" class="text-secondary small" title="出口 IPv6">+v6</span>
              </td>
              <td><code>{{ p.probe.source_ip || '—' }}</code></td>
              <td><code>{{ p.probe.gateway || '—' }}</code></td>
              <td class="small text-secondary">{{ fmtTime(p.probe.last_seen) }}</td>
            </tr>
            <tr v-if="expanded[p.probe.id]">
              <td colspan="9" class="bg-light">
                <div class="row g-2 small p-1">
                  <div class="col-md-6">
                    <b class="d-block mb-1">探针资产（注册时自动采集）</b>
                    <span v-if="assetItems(p).length">
                      <span v-for="a in assetItems(p)" :key="a[0]" class="me-3 d-inline-block"><span class="text-secondary">{{ a[1] }}：</span><code>{{ a[0] }}</code></span>
                    </span>
                    <span v-else class="text-secondary">暂无（该探针注册于旧版本，重启探针后自动补采）</span>
                  </div>
                  <div class="col-md-6">
                    <b class="d-block mb-1">出口信息（每 5 分钟自动探测）</b>
                    <span class="me-3 d-inline-block"><span class="text-secondary">公网 IPv4：</span><code>{{ p.status && p.status.egress_ip || '未探得' }}</code></span>
                    <span class="me-3 d-inline-block"><span class="text-secondary">运营商：</span><code>{{ p.status && p.status.egress_isp || '未探得' }}</code></span>
                    <span class="me-3 d-inline-block"><span class="text-secondary">归属地：</span><code>{{ (p.status && p.status.egress_geo) || '未探得' }}</code></span>
                    <span class="me-3 d-inline-block"><span class="text-secondary">IPv6：</span><code>{{ (p.status && p.status.egress_ipv6) || '不支持/未探得' }}</code></span>
                    <span v-if="p.status && p.status.clock_offset_ms" class="d-inline-block"><span class="text-secondary">时钟偏移：</span><code>{{ (p.status.clock_offset_ms / 1000).toFixed(1) }}s</code></span>
                  </div>
                </div>
              </td>
            </tr>
          </template>
          <tr v-if="!probes.length">
            <td colspan="9" class="text-secondary py-4 text-center">
              暂无探针注册。请在楼层 VM 上执行：<code>deploy/install-agent.sh http://&lt;中心IP&gt; &lt;注册令牌&gt;</code>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="text-secondary small">本页为只读总览；探针的绑定线路、SSH 运维与版本升级请前往 <router-link to="/probes/manage">探针配置</router-link>。探针自身内存/磁盘连续超 90% 会触发 P2"探针资源紧张"告警。</p>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, onUnmounted } from 'vue'
import { api } from '../api'
import StatusBadge from '../components/StatusBadge.vue'
import { fmtTime } from '../utils'

const probes = ref([]), lines = ref([]), err = ref('')
const expanded = reactive({})
const refreshing = ref(false), refreshMsg = ref('')
let timer = null

function lineName(p) {
  const l = lines.value.find(x => x.id === p.probe.line_id)
  return l ? l.name : '未绑定'
}
const health = (p) => p.status && (p.status.cpu_pct !== undefined || p.status.mem_pct !== undefined)
const clockOff = (p) => p.status && Math.abs(p.status.clock_offset_ms || 0) > 30000
function toggle(id) { expanded[id] = !expanded[id] }

// 资产明细（key → 展示名）
const assetLabels = {
  cpu_model: 'CPU', cpu_cores: '核数', mem_total_mb: '内存MB', disk_total_gb: '磁盘GB', disk_used_gb: '已用GB',
  kernel: '内核', distro: '系统', virt: '虚拟化', iface: '网卡', mac: 'MAC', mtu: 'MTU',
}
function assetItems(p) {
  const si = p.probe.sysinfo || {}
  return Object.entries(si).map(([k, v]) => [v, assetLabels[k] || k]).filter(x => x[0] !== '' && x[0] !== undefined)
}

async function load() {
  try {
    ;[probes.value, lines.value] = await Promise.all([
      api.get('/api/admin/probes'), api.get('/api/admin/lines'),
    ])
    err.value = ''
  } catch (e) { err.value = e.message }
}

async function refreshNow() {
  refreshing.value = true; refreshMsg.value = ''
  try {
    await api.post('/api/admin/probes/refresh', {})
    refreshMsg.value = '已下发：探针下一心跳（≤15s）重探出口并回报，同时已排程一轮测速（约 1 分钟内开始），页面加速刷新中'
    // 30 秒内 3s 快速轮询，之后恢复 15s
    clearInterval(timer)
    let n = 10
    timer = setInterval(() => { load(); if (--n <= 0) { clearInterval(timer); timer = setInterval(load, 15000) } }, 3000)
    setTimeout(() => { refreshing.value = false }, 8000)
  } catch (e) { err.value = e.message; refreshing.value = false }
}

onMounted(() => { load(); timer = setInterval(load, 15000) })
onUnmounted(() => clearInterval(timer))
</script>
