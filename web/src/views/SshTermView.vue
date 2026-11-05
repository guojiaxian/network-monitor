<template>
  <div>
    <div class="d-flex align-items-center mb-2">
      <router-link to="/probes/manage" class="btn btn-sm btn-link px-0 me-2">← 返回</router-link>
      <h5 class="mb-0">网页终端 · {{ probeName }}</h5>
      <span v-if="closed" class="badge bg-secondary ms-3">已断开</span>
      <span v-else class="badge bg-success ms-3">已连接</span>
      <button class="btn btn-sm btn-outline-secondary ms-auto" @click="reconnect">重新连接</button>
    </div>
    <div class="nw-card p-2" style="background:#141d2e">
      <div ref="termEl" class="term-box"></div>
    </div>
    <p class="text-secondary small mt-2 mb-0">
      基于 xterm.js（MIT 开源，VS Code 同款终端）· 连接目标为该探针配置的 SSH（{{ sshTarget }}）· 操作记录进入审计日志
    </p>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { api } from '../api'

const route = useRoute()
const termEl = ref(null)
const probeName = ref('')
const sshTarget = ref('')
const closed = ref(true)
let term = null, fit = null, ws = null

function connect() {
  closed.value = false
  term = new Terminal({
    cursorBlink: true, fontSize: 14, fontFamily: 'Consolas, Menlo, monospace',
    theme: { background: '#141d2e' }, scrollback: 5000,
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(termEl.value)
  fit.fit()
  term.focus()

  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${proto}://${location.host}/api/admin/probes/${route.params.id}/ssh/terminal`)
  ws.binaryType = 'arraybuffer'

  ws.onopen = () => {
    sendResize()
    term.write('\r\n\x1b[32m—— 已连接到探针，输入 exit 或关闭页面断开 ——\x1b[0m\r\n\r\n')
  }
  ws.onmessage = (ev) => {
    if (ev.data instanceof ArrayBuffer) {
      term.write(new Uint8Array(ev.data))
    }
  }
  ws.onclose = () => {
    closed.value = true
    term.write('\r\n\x1b[31m—— 连接已断开 ——\x1b[0m\r\n')
  }
  term.onData((data) => {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(new TextEncoder().encode(data))
  })
  window.addEventListener('resize', sendResize)
}

function sendResize() {
  if (ws && ws.readyState === WebSocket.OPEN && fit) {
    ws.send(JSON.stringify({ cols: term.cols, rows: term.rows }))
  }
}

function reconnect() {
  cleanup()
  connect()
  fitFit()
}

function fitFit() {
  try { fit && fit.fit() } catch { /* ignore */ }
}

function cleanup() {
  window.removeEventListener('resize', sendResize)
  if (ws) { ws.onclose = null; ws.close(); ws = null }
  if (term) { term.dispose(); term = null }
}

onMounted(async () => {
  try {
    const probes = await api.get('/api/admin/probes')
    const p = probes.find(x => x.probe.id === route.params.id)
    if (p) {
      probeName.value = p.probe.name || p.probe.hostname || p.probe.id.slice(0, 8)
      sshTarget.value = `${p.probe.ssh_host}:${p.probe.ssh_port}`
    }
  } catch { /* 名称非关键 */ }
  connect()
})
onUnmounted(cleanup)
</script>

<style scoped>
.term-box { height: calc(100vh - 210px); min-height: 400px; }
</style>
