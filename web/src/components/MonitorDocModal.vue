<template>
  <ModalBox :show="show" title="监测说明（自动探测与监测机制一览）" wide @close="$emit('close')">
    <div class="monitor-doc">
      <div class="d-flex align-items-center mb-2">
        <p class="text-secondary small mb-0">
          数据<b>实时取自系统配置</b>——打开即刷新，任何周期/超时/开关的改动即刻反映在此；行尾标注各数据的配置入口。
        </p>
        <button class="nw-btn nw-btn-neutral nw-btn-spin ms-auto flex-shrink-0" :disabled="loading" @click="load" title="仅重新拉取说明数据显示，不触发探测">
          <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path fill-rule="evenodd" d="M8 3a5 5 0 1 0 4.546 2.914.5.5 0 0 1 .908-.417A6 6 0 1 1 8 2v1z"/><path d="M8 4.466V.534a.25.25 0 0 1 .41-.192l2.36 1.966c.12.1.12.284 0 .384L8.41 4.658A.25.25 0 0 1 8 4.466z"/></svg>刷新
        </button>
      </div>
      <div v-if="err" class="text-danger small mb-2">{{ err }}（可点右上角刷新重试）</div>
      <div v-if="loading && !doc" class="text-secondary small mb-2">加载中…</div>

      <template v-if="doc">
        <div v-for="g in doc.groups" :key="g.title" class="mb-1">
          <div class="doc-group">{{ g.title }} <span class="text-secondary">{{ g.note }}</span></div>
          <table class="table table-sm mb-2 doc-table">
            <thead><tr><th>功能</th><th style="width:17%">周期</th><th style="width:12%">超时</th><th style="width:9%">状态</th><th style="width:16%">配置入口</th></tr></thead>
            <tbody>
              <tr v-for="r in g.rows" :key="r.name">
                <td>
                  {{ r.name }}
                  <div v-if="r.desc" class="text-secondary small">{{ r.desc }}</div>
                  <div v-if="r.items && r.items.length" class="doc-items">
                    <div v-for="it in r.items" :key="it.name" class="doc-item">
                      <b>{{ it.name }}</b> <span class="text-secondary">{{ it.target }}</span> — 每 {{ it.period }} · 超时 {{ it.timeout }}
                    </div>
                  </div>
                </td>
                <td>{{ r.period }}</td>
                <td>{{ r.timeout || '—' }}</td>
                <td><span class="badge" :class="statusClass(r.status)">{{ statusText(r.status) }}</span></td>
                <td class="text-secondary small">{{ r.source }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <h6>告警触发与自动恢复（触发条件中的数值为实时配置）</h6>
        <table class="table table-sm mb-2 doc-table">
          <thead><tr><th style="width:8%">级别</th><th style="width:19%">告警</th><th>触发条件</th><th style="width:18%">自动恢复</th></tr></thead>
          <tbody>
            <tr v-for="a in doc.alerts" :key="a.level + a.name">
              <td><span class="badge" :class="levelClass(a.level)">{{ a.level }}</span></td>
              <td>{{ a.name }}</td>
              <td>{{ a.trigger }}</td>
              <td>{{ a.recovery }}</td>
            </tr>
          </tbody>
        </table>

        <h6>通用机制</h6>
        <ul class="small doc-ul mb-0">
          <li>触发与恢复均自动发通知（邮件按 P0P1 / P2 分组投递，钉钉/企微恒推 P0P1、P2 需开关）；支持按线路静默。收件组在「告警策略」页配置。</li>
          <li>中心重启时未恢复告警统一收口（历史保留）；目标删除对应告警自动收口。</li>
          <li>探针断网样本本地缓存（≤48MB）恢复自动补传；样本时钟快于服务器 2 分钟按服务器时间钳位（请保持 NTP）。</li>
          <li>数据保留：时序库 <b>2 年</b>；扫描记录 7 天 / 3000 条；告警、审计、报表长期保留于 PostgreSQL。</li>
        </ul>
      </template>
    </div>
    <!-- 纯说明弹窗无「保存」语义：底部只保留一个「确定」= 关闭（无取消按钮，避免两个按钮都只做关闭） -->
    <template #footer>
      <button class="btn btn-sm btn-primary" @click="$emit('close')">确定</button>
    </template>
  </ModalBox>
</template>

<script setup>
// 「监测说明」弹窗：数据全部来自 GET /api/admin/monitor-doc（internal/server/monitordoc.go）。
// ★ 维护约定（2026-11-08 起移交）：周期/超时/状态等机制数据的唯一维护点是后端 monitordoc.go，
//   机制变更改后端行定义即可，本组件只负责渲染（不要在这里写死任何机制数值）。
import { ref, watch } from 'vue'
import ModalBox from './ModalBox.vue'
import { api } from '../api'

const props = defineProps({ show: Boolean })
defineEmits(['close'])

const doc = ref(null)
const loading = ref(false)
const err = ref('')

async function load() {
  loading.value = true
  err.value = ''
  try {
    doc.value = await api.get('/api/admin/monitor-doc')
  } catch (e) {
    err.value = e.message || String(e)
  } finally {
    loading.value = false
  }
}

watch(() => props.show, (v) => { if (v) load() })

function statusClass(s) {
  return { on: 'bg-success', off: 'bg-secondary', fixed: 'bg-dark' }[s] || 'bg-secondary'
}
function statusText(s) {
  return { on: '启用', off: '未启用', fixed: '内置固定' }[s] || s
}
function levelClass(lv) {
  return { P0: 'bg-dark', P1: 'bg-danger', P2: 'bg-warning text-dark' }[lv] || 'bg-secondary'
}
</script>

<style scoped>
.monitor-doc { max-height: 68vh; overflow-y: auto; padding-right: 6px; }
.monitor-doc h6 { margin: 12px 0 6px; color: #1d2635; }
.doc-group { font-weight: 700; color: #1d2635; margin: 10px 0 4px; font-size: .88rem; }
.doc-group .text-secondary { font-weight: 400; font-size: .8rem; }
.doc-table { font-size: .82rem; margin: 0; }
.doc-table td, .doc-table th { padding: .28rem .5rem; vertical-align: middle; }
.doc-items { margin-top: 2px; }
.doc-item { font-size: .78rem; padding-left: 10px; border-left: 2px solid #e3e8ef; margin: 2px 0; }
.doc-ul { padding-left: 18px; }
.doc-ul li { margin-bottom: 2px; }
</style>
