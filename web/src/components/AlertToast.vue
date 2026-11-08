<template>
  <Teleport to="body">
    <div class="alert-toast-wrap" aria-live="polite" aria-atomic="true">
      <Transition name="at" mode="out-in" @after-leave="advance">
        <div
          v-if="cur" :key="cur.id" class="alert-toast" role="status"
          :class="'at-' + (cur.severity || 'P2').toLowerCase()"
          title="点击进入告警中心"
          @click="goCenter"
        >
          <span class="badge at-badge" :class="sevCls(cur.severity)">{{ cur.severity }}</span>
          <div class="at-main">
            <div class="at-title">{{ cur.title }}</div>
            <div class="at-meta">{{ hhmmss(cur.starts_at) }} · 点击查看告警中心</div>
          </div>
          <span v-if="queue.length" class="at-more">还有 {{ queue.length }} 条</span>
        </div>
      </Transition>
    </div>
  </Teleport>
</template>

<script setup>
// 新告警顶部提示（2026-11-08 用户要求）：
//   - 轮询 firing 告警（5s），仅对"页面打开后新出现"的告警弹窗（首轮只做基准，不补弹历史）；
//   - 每条停留 3s 自动消失；多条依次弹出（前一条退场后后一条进场），右侧显示剩余条数；
//   - 容器 pointer-events:none、仅提示卡片自身可点，不遮挡/不阻塞页面浏览；
//   - 点击任意位置进入告警中心（/alerts）。
// 只在管理布局内挂载（App.vue 的 showLayout && user），大屏/登录页不轮询。
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import dayjs from 'dayjs'
import { api } from '../api'

const router = useRouter()
const queue = ref([])
const cur = ref(null)
let timer = 0
let pollTimer = 0
let stopped = false

const SHOW_MS = 3000
const POLL_MS = 5000
const seen = new Set()
let seeded = false

function hhmmss(t) { return dayjs(t).format('HH:mm:ss') }
function sevCls(s) { return { P0: 'bg-dark', P1: 'bg-danger', P2: 'bg-warning text-dark' }[s] || 'bg-secondary' }

// 显示队列下一条；停留 SHOW_MS 后置空，离场动画结束由 @after-leave 递归取下一条
function advance() {
  clearTimeout(timer)
  cur.value = queue.value.shift() || null
  if (cur.value) timer = setTimeout(() => { cur.value = null }, SHOW_MS)
}

function goCenter() {
  queue.value = []
  cur.value = null
  clearTimeout(timer)
  router.push('/alerts')
}

async function poll() {
  if (stopped || document.hidden) return
  let rows = []
  try {
    const d = await api.get('/api/admin/alerts?status=firing&hours=0&limit=200&offset=0')
    rows = (d.rows || []).slice().sort((a, b) => new Date(a.starts_at) - new Date(b.starts_at))
  } catch (e) {
    return // 未登录/网络异常：静默跳过，下轮再试
  }
  // 首轮只登记基准，把打开页面前就已存在的告警排除掉
  if (!seeded) {
    rows.forEach(r => seen.add(r.id))
    seeded = true
    return
  }
  const fresh = rows.filter(r => !seen.has(r.id))
  if (!fresh.length) return
  if (seen.size > 5000) { seen.clear(); rows.forEach(r => seen.add(r.id)) } // 极端长会话兜底
  fresh.forEach(r => seen.add(r.id))
  queue.value.push(...fresh)
  if (!cur.value) advance()
}

onMounted(() => {
  poll()
  pollTimer = setInterval(poll, POLL_MS)
})
onBeforeUnmount(() => {
  stopped = true
  clearInterval(pollTimer)
  clearTimeout(timer)
})
</script>

<style scoped>
/* 容器固定页面顶部居中、不接收鼠标事件；只有提示卡片本身可点 */
.alert-toast-wrap {
  position: fixed; top: 12px; left: 50%; transform: translateX(-50%);
  z-index: 1030; display: flex; justify-content: center;
  width: min(560px, calc(100vw - 32px)); pointer-events: none;
}
.alert-toast {
  pointer-events: auto; cursor: pointer; user-select: none;
  display: flex; align-items: center; gap: 10px; width: 100%;
  background: #fff; border: 1px solid #e3e8ef; border-left: 4px solid #dc3545;
  border-radius: 10px; padding: 10px 14px;
  box-shadow: 0 10px 30px rgba(16, 24, 40, .18);
}
.alert-toast:hover { box-shadow: 0 12px 34px rgba(16, 24, 40, .26); }
.at-p0 { border-left-color: #1d2635; }
.at-p1 { border-left-color: #dc3545; }
.at-p2 { border-left-color: #ffc107; }
.at-badge { flex: 0 0 auto; }
.at-main { flex: 1 1 auto; min-width: 0; }
.at-title {
  font-size: .9rem; font-weight: 600; color: #1d2635; line-height: 1.35;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.at-meta { font-size: .76rem; color: #8a94a6; margin-top: 2px; font-variant-numeric: tabular-nums; }
.at-more { flex: 0 0 auto; font-size: .76rem; color: #6c7a8d; background: #f1f4f8; border-radius: 999px; padding: 2px 8px; white-space: nowrap; }
/* 进场：从顶部滑下淡入；退场：上移淡出（留 0.16s 间隔，多条依次覆盖出现） */
.at-enter-active, .at-leave-active { transition: opacity .18s ease, transform .18s ease; }
.at-enter-from { opacity: 0; transform: translateY(-10px); }
.at-leave-to { opacity: 0; transform: translateY(-8px); }
@media (prefers-reduced-motion: reduce) {
  .at-enter-active, .at-leave-active { transition: none; }
}
</style>
