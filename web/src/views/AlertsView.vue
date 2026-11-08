<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <div>
        <h4 class="mb-0">告警中心</h4>
        <div class="text-secondary small mt-1">维护期需要暂停某线路告警？到 <router-link to="/settings/alert">系统设置 → 告警策略</router-link> 创建静默窗口。</div>
      </div>
      <button class="nw-btn nw-btn-neutral ms-auto" @click="load" title="仅重新拉取页面数据显示，不触发探测">
        <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path fill-rule="evenodd" d="M8 3a5 5 0 1 0 4.546 2.914.5.5 0 0 1 .908-.417A6 6 0 1 1 8 2v1z"/><path d="M8 4.466V.534a.25.25 0 0 1 .41-.192l2.36 1.966c.12.1.12.284 0 .384L8.41 4.658A.25.25 0 0 1 8 4.466z"/></svg>刷新
      </button>
    </div>

    <div class="nw-card">
      <!-- 筛选栏：时间窗（按告警开始时间，默认近 24 小时）/ 级别 / 状态 / 类型 -->
      <div class="d-flex flex-wrap align-items-center gap-3 mb-3">
        <select v-model.number="f.hours" class="form-select form-select-sm w-auto" @change="reload" title="按告警开始时间过滤">
          <option :value="24">近 24 小时</option>
          <option :value="72">近 3 天</option>
          <option :value="168">近 7 天</option>
          <option :value="720">近 30 天</option>
          <option :value="0">全部时间</option>
        </select>
        <select v-model="f.severity" class="form-select form-select-sm w-auto" @change="reload">
          <option value="">全部级别</option>
          <option value="P0">P0</option>
          <option value="P1">P1</option>
          <option value="P2">P2</option>
        </select>
        <select v-model="f.status" class="form-select form-select-sm w-auto" @change="reload">
          <option value="">全部状态</option>
          <option value="firing">进行中</option>
          <option value="resolved">已恢复</option>
        </select>
        <select v-model="f.kind" class="form-select form-select-sm w-auto" @change="reload">
          <option value="">全部类型</option>
          <option v-for="k in kindList" :key="k[0]" :value="k[0]">{{ k[1] }}</option>
        </select>
        <span class="text-secondary small ms-auto">时间按告警开始时间过滤</span>
      </div>

      <table class="table nw-table table-sm align-middle table-fixed">
        <thead><tr><th style="width:7%">级别</th><th style="width:9%">状态</th><th style="width:11%">类型</th><th>标题</th><th style="width:15%">开始时间</th><th style="width:14%">恢复时间</th></tr></thead>
        <tbody>
          <tr v-for="a in alerts" :key="a.id">
            <td><span class="badge" :class="sevCls(a.severity)">{{ a.severity }}</span></td>
            <td>
              <span class="badge" :class="a.status === 'firing' ? 'bg-danger' : 'bg-success'">
                {{ a.status === 'firing' ? '进行中' : '已恢复' }}
              </span>
            </td>
            <td class="small">{{ kindText(a.kind) }}</td>
            <td>{{ a.title }}</td>
            <td class="small">{{ fmt(a.starts_at) }}</td>
            <td class="small">{{ a.ends_at ? fmt(a.ends_at) : '—' }}</td>
          </tr>
          <tr v-if="!alerts.length"><td colspan="6" class="text-secondary text-center py-4">暂无告警</td></tr>
        </tbody>
      </table>

      <!-- 分页（与扫描记录页同款：独立控件 + gap 留白） -->
      <div class="d-flex flex-wrap align-items-center gap-3 mt-2">
        <span class="small text-secondary">共 {{ total }} 条</span>
        <select v-model.number="f.limit" class="form-select form-select-sm w-auto" @change="reload" title="每页显示条数">
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
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import dayjs from 'dayjs'
import { api } from '../api'

// 告警中心：筛选（时间窗/级别/状态/类型）+ 服务端分页，默认近 24 小时、每页 20 条、按开始时间倒序。
const alerts = ref([]), total = ref(0), page = ref(1)
const f = reactive({ hours: 24, severity: '', status: '', kind: '', limit: 20 })

// 全部告警类型（与 alert.go kind 常量对齐，按严重程度排列）
const kindList = [
  ['blind', '监控盲区'],
  ['line_down', '线路中断'],
  ['floor_down', '楼层中断'],
  ['degraded', '线路降质'],
  ['speed_low', '带宽不达标'],
  ['probe_conflict', '探针数据冲突'],
  ['monitor_down', '内网服务不可达'],
  ['industry_down', '行业网不可达'],
  ['probe_type_fail', '探测异常'],
  ['probe_resource', '探针资源紧张'],
]
const kinds = Object.fromEntries(kindList)
function kindText(k) { return kinds[k] || k }
function sevCls(s) { return { P0: 'bg-dark', P1: 'bg-danger', P2: 'bg-warning text-dark' }[s] || 'bg-secondary' }
function fmt(t) { return dayjs(t).format('YYYY-MM-DD HH:mm:ss') }

const pageCount = computed(() => Math.max(1, Math.ceil(total.value / f.limit)))

async function load() {
  const d = await api.get(`/api/admin/alerts?status=${f.status}&severity=${f.severity}&kind=${f.kind}` +
    `&hours=${f.hours}&limit=${f.limit}&offset=${(page.value - 1) * f.limit}`)
  total.value = d.total || 0
  alerts.value = d.rows || []
  // 页码越界收敛：筛选后结果变少时回落到最后一页，重取一次
  if (page.value > pageCount.value && total.value > 0) {
    page.value = pageCount.value
    return load()
  }
}

// 筛选/每页条数变化：回到第一页；翻页：保持筛选重取
function reload() { page.value = 1; load() }
function gotoPage(p) { page.value = p; load() }

onMounted(load)
</script>
