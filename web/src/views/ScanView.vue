<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <div>
        <h4 class="mb-0">🔎 IP 扫描</h4>
        <div class="text-secondary small mt-1">中心服务器对所选楼层子网逐 IP ping（单包）；禁 ping 的主机（如 Windows 默认防火墙）会显示离线；每次扫描按网段各留一条记录，点击记录行可展开查看逐 IP 结果</div>
      </div>
      <router-link class="nw-btn nw-btn-neutral ms-auto" :to="selected.length === 1 ? { path: '/history', query: { type: 'ipscan', floor: selected[0] } } : { path: '/history', query: { type: 'ipscan' } }">
        <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M1 2.5A1.5 1.5 0 0 1 2.5 1h3A1.5 1.5 0 0 1 7 2.5v3A1.5 1.5 0 0 1 5.5 7h-3A1.5 1.5 0 0 1 1 5.5v-3zm8 0A1.5 1.5 0 0 1 10.5 1h3A1.5 1.5 0 0 1 15 2.5v3A1.5 1.5 0 0 1 13.5 7h-3A1.5 1.5 0 0 1 9 5.5v-3zm-8 8A1.5 1.5 0 0 1 2.5 9h3A1.5 1.5 0 0 1 7 10.5v3A1.5 1.5 0 0 1 5.5 15h-3A1.5 1.5 0 0 1 1 13.5v-3zm8 0A1.5 1.5 0 0 1 10.5 9h3a1.5 1.5 0 0 1 1.5 1.5v3a1.5 1.5 0 0 1-1.5 1.5h-3A1.5 1.5 0 0 1 9 13.5v-3z"/></svg>历史汇总
      </router-link>
    </div>

    <!-- 楼层多选 -->
    <div class="nw-card mb-3">
      <div class="d-flex align-items-center flex-wrap gap-2 mb-2">
        <span class="small text-secondary">选择楼层（可多选）：</span>
        <button class="btn btn-sm btn-outline-secondary py-0" @click="selectAll">全选</button>
        <button class="btn btn-sm btn-outline-secondary py-0" @click="selected = []">清空</button>
        <span class="small text-secondary">已选 {{ selected.length }} / {{ floors.length }}</span>
        <button class="btn btn-sm btn-primary ms-auto" :disabled="!selected.length || !!activeBatch" @click="startScan">
          {{ activeBatch ? '扫描中…' : '开始扫描' }}
        </button>
      </div>
      <div class="d-flex flex-wrap gap-2">
        <button v-for="f in floors" :key="f.id" class="btn btn-sm"
                :class="selected.includes(f.id) ? 'btn-primary' : 'btn-outline-secondary'"
                :title="f.cidr || '未配 CIDR（扫描时该网段将报错跳过）'"
                @click="toggleFloor(f.id)">
          {{ f.name }}<span v-if="!f.cidr" class="ms-1 small">（未配置网段）</span>
        </button>
      </div>
      <div class="small text-secondary mt-2" v-if="selected.length">将扫描：{{ selectedFloors.map(f => f.name).join('、') }}</div>
    </div>

    <!-- 进行中批次的实时进度 -->
    <div class="nw-card mb-3" v-if="activeBatch">
      <div class="d-flex align-items-center flex-wrap mb-2">
        <span class="spinner-border spinner-border-sm text-primary me-2"></span>
        <b class="small">{{ activeBatch.trigger === 'schedule' ? '定时扫描进行中' : '扫描进行中' }}</b>
        <span class="small text-secondary ms-2">
          已完成 {{ doneCount }} / {{ activeBatch.rows.length }} 个网段 · 在线 {{ sumUp }} / {{ sumTotal }}
        </span>
        <span class="small text-secondary ms-2" v-if="activeBatch.trigger === 'schedule'">批次 {{ activeBatch.batch }}</span>
      </div>
      <div class="d-flex flex-wrap gap-2">
        <span v-for="r in activeBatch.rows" :key="r.id" class="badge rounded-pill" :class="badgeClass(r.status)">
          {{ r.floor_name }}
          <template v-if="r.status === 'done'">　在线 {{ r.up }}/{{ r.total }} · {{ (r.duration_ms/1000).toFixed(1) }}s</template>
          <template v-else-if="r.status === 'running'">　扫描中…</template>
          <template v-else-if="r.status === 'error'">　失败</template>
          <template v-else-if="r.status === 'interrupted'">　已中断</template>
          <template v-else>　等待</template>
        </span>
      </div>
    </div>

    <!-- 扫描记录：一个网段一行 -->
    <div class="nw-card">
      <div class="d-flex align-items-center flex-wrap gap-3 mb-2">
        <h6 class="mb-0">扫描记录</h6>
        <select v-model.number="floorFilter" class="form-select form-select-sm w-auto" @change="reloadFromFirstPage" title="按楼层筛选">
          <option :value="0">全部楼层</option>
          <option v-for="f in floors" :key="f.id" :value="f.id">{{ f.name }}</option>
        </select>
        <select v-model.number="daysFilter" class="form-select form-select-sm w-auto" @change="reloadFromFirstPage" title="按扫描时间筛选">
          <option :value="1">今天</option>
          <option :value="3">3天内</option>
          <option :value="7">7天内</option>
        </select>
        <select v-model="triggerFilter" class="form-select form-select-sm w-auto" @change="reloadFromFirstPage">
          <option value="all">全部来源</option>
          <option value="manual">手动扫描</option>
          <option value="schedule">定时扫描</option>
        </select>
        <button type="button" class="nw-btn nw-btn-neutral" @click="loadRuns" title="仅重新拉取扫描记录显示，不触发探测">
          <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path fill-rule="evenodd" d="M8 3a5 5 0 1 0 4.546 2.914.5.5 0 0 1 .908-.417A6 6 0 1 1 8 2v1z"/><path d="M8 4.466V.534a.25.25 0 0 1 .41-.192l2.36 1.966c.12.1.12.284 0 .384L8.41 4.658A.25.25 0 0 1 8 4.466z"/></svg>刷新
        </button>
      </div>
      <table class="table nw-table table-sm align-middle mb-0" v-if="runs.length">
        <thead>
          <tr>
            <th style="width:11%">时间</th>
            <th style="width:26%">楼层 / 网段</th>
            <th style="width:11%">在线/总数</th>
            <th style="width:9%">耗时</th>
            <th style="width:9%">来源</th>
            <th style="width:11%">操作人</th>
            <th style="width:10%">状态</th>
            <th style="width:4%"></th>
          </tr>
        </thead>
        <tbody>
          <template v-for="r in runs" :key="r.id">
            <tr class="run-row" @click="toggleRun(r)" :title="isPending(r) ? '扫描进行中' : '点击展开/收起逐 IP 结果'">
              <td class="text-nowrap">{{ fmtTime(r.created_at) }}</td>
              <td class="text-truncate">
                <b>{{ r.floor_name || '（楼层已删除）' }}</b>
                <code class="ms-1 small">{{ r.cidr || '' }}</code>
              </td>
              <td>
                <template v-if="r.status === 'done'"><b style="color:#0a8f4d">{{ r.up }}</b> / {{ r.total }}</template>
                <span class="text-secondary" v-else>—</span>
              </td>
              <td>{{ r.status === 'done' || r.status === 'error' ? (r.duration_ms/1000).toFixed(1) + 's' : '—' }}</td>
              <td>
                <span class="badge" :class="r.trigger === 'schedule' ? 'bg-info-subtle text-info-emphasis' : 'bg-secondary-subtle text-secondary-emphasis'">
                  {{ r.trigger === 'schedule' ? '定时' : '手动' }}
                </span>
              </td>
              <td>{{ r.created_by }}</td>
              <td><span :class="runStatusClass(r.status)">{{ runStatusText(r.status) }}</span></td>
              <td class="text-end text-secondary">{{ expanded[r.id] ? '▾' : '▸' }}</td>
            </tr>
            <tr v-if="expanded[r.id]">
              <td colspan="8" style="background:#f8f9fb">
                <div v-if="detailLoading[r.id]" class="text-secondary small py-2">加载明细中…</div>
                <template v-else-if="details[r.id]">
                  <div class="d-flex align-items-center flex-wrap small mb-2">
                    <b>{{ details[r.id].floor_name }}</b>
                    <span class="text-secondary ms-2">{{ details[r.id].cidr }}</span>
                    <template v-if="details[r.id].status === 'done'">
                      <span class="ms-2">在线 <b style="color:#0a8f4d">{{ details[r.id].up }}</b> / {{ details[r.id].total }} · 耗时 {{ (details[r.id].duration_ms/1000).toFixed(1) }}s</span>
                    </template>
                    <span class="text-secondary ms-2" v-else-if="isPending(details[r.id])">{{ runStatusText(details[r.id].status) }}，每 2 秒自动刷新…</span>
                    <span class="text-danger ms-2" v-else>{{ runStatusText(details[r.id].status) }}</span>
                  </div>
                  <div class="ip-grid" v-if="(details[r.id].results || []).length">
                    <div v-for="res in details[r.id].results" :key="res.ip" class="ip-cell"
                         :class="res.online ? 'on' : 'off'"
                         :title="res.ip + (res.online ? ' · 在线 · ' + res.rtt_ms.toFixed(1) + 'ms' : ' · 离线')">
                      {{ cellLabel(res.ip, details[r.id].cidr) }}
                    </div>
                  </div>
                  <div class="text-secondary small" v-else>无结果数据</div>
                </template>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
      <div class="text-secondary small py-4 text-center" v-else>暂无扫描记录，选择楼层并点击"开始扫描"（定时任务也会写入记录）。</div>
      <!-- 分页：控制一页条数避免页面过长 -->
      <div class="d-flex align-items-center flex-wrap gap-3 mt-2" v-if="total > 0">
        <span class="small text-secondary">共 {{ total }} 条</span>
        <select v-model.number="pageSize" class="form-select form-select-sm w-auto" @change="reloadFromFirstPage" title="每页显示条数">
          <option :value="10">每页 10 条</option>
          <option :value="20">每页 20 条</option>
          <option :value="50">每页 50 条</option>
        </select>
        <div class="ms-auto d-flex align-items-center gap-3">
          <button type="button" class="btn btn-sm btn-outline-secondary" :disabled="page <= 1" @click="gotoPage(page - 1)">上一页</button>
          <span class="small text-secondary">第 {{ page }} / {{ pageCount }} 页</span>
          <button type="button" class="btn btn-sm btn-outline-secondary" :disabled="page >= pageCount" @click="gotoPage(page + 1)">下一页</button>
        </div>
      </div>
    </div>
    <div v-if="scanErr" class="text-danger small mt-2">{{ scanErr }}</div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../api'

const route = useRoute()
const floors = ref([])
const selected = ref([])
const runs = ref([])
const triggerFilter = ref('all')
const floorFilter = ref(0)       // 楼层筛选：0=全部楼层，其余为楼层 id
const daysFilter = ref(1)        // 时间筛选：1=今天 3=近三天 7=近七天
const page = ref(1)              // 当前页（从 1 起）
const pageSize = ref(10)         // 每页条数
const total = ref(0)             // 满足筛选条件的记录总数
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)))
const expanded = reactive({})      // 记录 id → 是否展开
const details = reactive({})       // 记录 id → 明细（含逐 IP 结果；已完成结果缓存）
const detailLoading = reactive({})
const scanErr = ref('')
let pollTimer = null
let watchBatch = null              // 本次发起的批次：单网段时自动展开

const selectedFloors = computed(() => floors.value.filter(f => selected.value.includes(f.id)))
function isPending(r) { return r.status === 'pending' || r.status === 'running' }
// 进行中批次 = 最新一条未完成记录所属批次（手动与定时共用展示）；rows 含该批次已完成的网段
const activeRows = computed(() => runs.value.filter(isPending))
const activeBatch = computed(() => {
  const pending = activeRows.value
  if (!pending.length) return null
  const batch = pending[0].batch
  // 按 id 升序 = 建记录顺序 = 实际扫描顺序（记录列表本身是 id 倒序）
  const rows = runs.value.filter(r => r.batch === batch).slice().sort((a, b) => a.id - b.id)
  return { batch, trigger: pending[0].trigger, rows }
})
const doneCount = computed(() => activeBatch.value ? activeBatch.value.rows.filter(r => !isPending(r)).length : 0)
const sumUp = computed(() => activeBatch.value ? activeBatch.value.rows.reduce((a, r) => a + r.up, 0) : 0)
const sumTotal = computed(() => activeBatch.value ? activeBatch.value.rows.reduce((a, r) => a + r.total, 0) : 0)

function toggleFloor(id) {
  const i = selected.value.indexOf(id)
  if (i >= 0) selected.value.splice(i, 1)
  else selected.value.push(id)
}
function selectAll() { selected.value = floors.value.map(f => f.id) }

async function loadRuns() {
  try {
    const q = `floor_id=${floorFilter.value}&days=${daysFilter.value}` +
      (triggerFilter.value === 'all' ? '' : `&trigger=${triggerFilter.value}`) +
      `&limit=${pageSize.value}&offset=${(page.value - 1) * pageSize.value}`
    const res = await api.get(`/api/admin/scan/runs?${q}`)
    runs.value = res.rows
    total.value = res.total
  } catch (e) { scanErr.value = e.message; return }
  // 页码越界收敛（筛选后记录变少等）：回到有效页重取一次
  if (page.value > pageCount.value) {
    page.value = pageCount.value
    return loadRuns()
  }
  // 本次发起的批次：单网段自动展开（多网段逐行展开会堆满页面，交给进度卡+手动点击）
  if (watchBatch) {
    const rows = runs.value.filter(r => r.batch === watchBatch)
    if (rows.length === 1) { expanded[rows[0].id] = true; openDetail(rows[0]) }
    if (rows.length) watchBatch = null
  }
  // 已展开且仍在进行的记录：随进度刷新；收口后补最后一次
  for (const r of runs.value) {
    if (!expanded[r.id]) continue
    if (isPending(r) || (details[r.id] && isPending(details[r.id]))) openDetail(r)
  }
  schedulePoll()
}
// 有未完成记录时每 2s 链式轮询（setTimeout 避免请求重叠）
function schedulePoll() {
  if (pollTimer) { clearTimeout(pollTimer); pollTimer = null }
  if (activeRows.value.length) pollTimer = setTimeout(loadRuns, 2000)
}
// 筛选/每页条数变化：回到第一页；翻页：保持筛选重取
function reloadFromFirstPage() { page.value = 1; loadRuns() }
function gotoPage(p) { page.value = p; loadRuns() }
async function startScan() {
  scanErr.value = ''
  try {
    const res = await api.post('/api/admin/scan', { floor_ids: selected.value })
    watchBatch = res.batch
    page.value = 1 // 新批次记录在最前，回到第一页才能看到进度与自动展开
    await loadRuns()
  } catch (e) { scanErr.value = e.message }
}
function toggleRun(r) {
  expanded[r.id] = !expanded[r.id]
  if (expanded[r.id]) openDetail(r)
}
async function openDetail(r) {
  const d = details[r.id]
  if (d && !isPending(d)) return // 已收口的记录明细不再变化，走缓存
  detailLoading[r.id] = true
  try { details[r.id] = await api.get(`/api/admin/scan/runs/${r.id}`) }
  catch (e) { scanErr.value = e.message }
  finally { detailLoading[r.id] = false }
}
// 格子标签：/24 及更窄只显示末字节（如 37）；比 /24 更宽（/23、/22）显示末两字节（如 14.37），
// 否则同一网段两个 /24 段会出现两遍 1~254 无法区分。
function cellLabel(ip, cidr) {
  const parts = ip.split('.')
  const mask = parseInt((cidr || '').split('/')[1], 10)
  return mask && mask < 24 ? `${parts[2]}.${parts[3]}` : parts[3]
}
function runStatusText(st) {
  return { done: '已完成', pending: '等待', running: '进行中', interrupted: '已中断', error: '失败' }[st] || st
}
function runStatusClass(st) {
  return { done: 'text-success', running: 'text-primary', pending: 'text-secondary',
    interrupted: 'text-warning', error: 'text-danger' }[st] || ''
}
function badgeClass(st) {
  return { running: 'bg-primary', done: 'bg-success', error: 'bg-danger', interrupted: 'bg-warning text-dark' }[st] || 'bg-secondary'
}
function fmtTime(s) {
  return new Date(s).toLocaleString('zh-CN', { hour12: false, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

onMounted(async () => {
  try {
    floors.value = await api.get('/api/admin/floors')
    await loadRuns()
    // 楼层状态页卡片页脚"IP 扫描 →"带 ?floor=<id> 进入：预选该楼层
    const qf = Number(route.query.floor)
    if (qf && floors.value.some(f => f.id === qf)) selected.value = [qf]
  } catch (e) { scanErr.value = e.message }
})
onUnmounted(() => { if (pollTimer) clearTimeout(pollTimer) })
</script>
