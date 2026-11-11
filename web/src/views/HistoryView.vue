<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <div>
        <h4 class="mb-0">📜 探测历史</h4>
        <div class="text-secondary small mt-1">逐条探测记录查询（服务端分页，单次最长 31 天；时序库存留 2 年）</div>
      </div>
      <button class="nw-btn nw-btn-neutral nw-btn-spin ms-auto" :disabled="loading" @click="reload" title="仅重新拉取页面数据显示，不触发探测">
        <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path fill-rule="evenodd" d="M8 3a5 5 0 1 0 4.546 2.914.5.5 0 0 1 .908-.417A6 6 0 1 1 8 2v1z"/><path d="M8 4.466V.534a.25.25 0 0 1 .41-.192l2.36 1.966c.12.1.12.284 0 .384L8.41 4.658A.25.25 0 0 1 8 4.466z"/></svg>刷新
      </button>
      <button class="nw-btn nw-btn-neutral ms-2" :disabled="loading || csvBusy" @click="exportCSV"
        title="按当前筛选导出最近 2 万条">
        <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><path d="M.5 9.9a.5.5 0 0 1 .5.5v2.5a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-2.5a.5.5 0 0 1 1 0v2.5a2 2 0 0 1-2 2H2a2 2 0 0 1-2-2v-2.5a.5.5 0 0 1 .5-.5z"/><path d="M7.646 11.854a.5.5 0 0 0 .708 0l3-3a.5.5 0 0 0-.708-.708L8.5 10.293V1.5a.5.5 0 0 0-1 0v8.793L5.354 8.146a.5.5 0 1 0-.708.708l3 3z"/></svg>导出 CSV
      </button>
    </div>

    <!-- 类型页签：下划线标题式，替代下拉，点击切换 -->
    <div class="nw-tabs mb-3">
      <button v-for="t in typeTabs" :key="t.v" type="button" :class="{ active: f.type === t.v }"
        @click="switchType(t.v)">{{ t.label }}</button>
    </div>

    <!-- KPI：全窗口统计（不受分页影响） -->
    <div class="row row-cols-2 row-cols-md-4 g-3 mb-3" v-if="stats && f.type !== 'ipscan'">
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">{{ f.type === 'speed' ? '测速轮次' : '探测次数' }}</div>
        <b class="stat-num">{{ stats.total }}</b>
      </div></div>
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">成功率</div>
        <b class="stat-num" :class="okRateClass">{{ okRatePct }}%</b>
      </div></div>
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">{{ f.type === 'speed' ? '平均下载' : '平均时延' }}</div>
        <b class="stat-num" :title="f.type === 'speed' ? '其余探针均值 / probe-12 均值（线路协议带宽不同，分开统计）' : ''">{{ speedAvgText }}<span class="small text-secondary fw-normal ms-1">{{ f.type === 'speed' ? 'Mbps' : 'ms' }}</span></b>
      </div></div>
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">失败次数</div>
        <b class="stat-num" :class="stats.fail > 0 ? 'text-danger' : ''">{{ stats.fail }}</b>
      </div></div>
    </div>

    <!-- IP 扫描页签 KPI：数据源 scan_runs（批次聚合） -->
    <div class="row row-cols-2 row-cols-md-4 g-3 mb-3" v-if="f.type === 'ipscan' && ipSum">
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">扫描批次</div>
        <b class="stat-num">{{ ipSum.n }}</b>
      </div></div>
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">扫描 IP 次</div>
        <b class="stat-num">{{ ipSum.total }}</b>
      </div></div>
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">平均在线率</div>
        <b class="stat-num">{{ ipSum.rate }}%</b>
      </div></div>
      <div class="col"><div class="nw-card h-100">
        <div class="text-secondary small">最近在线</div>
        <b class="stat-num">{{ ipSum.lastUp }}<span class="small text-secondary fw-normal ms-1">/ {{ ipSum.lastTotal }} 台</span></b>
      </div></div>
    </div>

    <!-- 筛选：控件行（时间/探针/结果）+ 多选 chips 独占整行，两层互不混排——
         任意窗口宽度下换行点确定（chips 只在自己那行内换行），不再出现控件被挤散错位；
         容器最小高度为两行，无 chips 的页签（全部/行业网）以留白补齐，页签切换卡高恒定 -->
    <div class="nw-card mb-3">
      <div class="hist-filter">
        <div class="d-flex flex-wrap gap-3 align-items-end">
          <div>
            <label class="form-label small mb-1">时间范围</label>
            <select v-model="f.range" class="form-select form-select-sm w-auto" @change="onRangeChange">
              <option v-for="o in rangeOptions" :key="o[0]" :value="o[0]">{{ o[1] }}</option>
            </select>
          </div>
          <template v-if="f.range === 'custom'">
            <div>
              <label class="form-label small mb-1">开始</label>
              <input type="datetime-local" v-model="f.start" class="form-control form-control-sm w-auto" @change="reload">
            </div>
            <div>
              <label class="form-label small mb-1">结束</label>
              <input type="datetime-local" v-model="f.end" class="form-control form-control-sm w-auto" @change="reload">
            </div>
          </template>
          <!-- 结果：固定第 2 槽位；IP 扫描页签该槽位由「楼层」取代（互斥不并存）。
               零横移且无空挡的关键：**可能缺席的控件必须排行尾**——探针单选只在
               全部/Ping/TCP/DNS/HTTP 五个页签存在且放最后，缺席页签（内→内/行业网/定时测速/IP 扫描）
               的控件行以 结果/楼层 自然收尾，右侧留白是版式而非空挡
               （此前的 visibility 隐形占位方案会把空挡留在行中间，已废弃） -->
          <div v-if="f.type !== 'ipscan'">
            <label class="form-label small mb-1">结果</label>
            <select v-model="f.status" class="form-select form-select-sm w-auto" @change="reload">
              <option value="">全部结果</option>
              <option value="ok">仅成功</option>
              <option value="fail">仅失败</option>
            </select>
          </div>
          <!-- IP 扫描：楼层筛选（与结果同槽位） -->
          <div v-if="f.type === 'ipscan'">
            <label class="form-label small mb-1">楼层</label>
            <select v-model.number="f.floor" class="form-select form-select-sm w-auto" @change="reload">
              <option :value="0">全部楼层</option>
              <option v-for="fl in floors" :key="fl.id" :value="fl.id">{{ fl.name }}</option>
            </select>
          </div>
          <!-- 探针单选：行尾槽位（定时测速的探针多选在下方 chips 行；内→内/行业网/IP 扫描无此维度）；
               存在的页签共享同一探针列表，宽度恒定 -->
          <div v-if="needProbe && f.type !== 'speed'">
            <label class="form-label small mb-1">探针</label>
            <select v-model="f.probe" class="form-select form-select-sm w-auto" @change="onProbeChange">
              <option v-for="p in probes" :key="p.id" :value="p.id">
                {{ p.name || p.hostname || p.id.slice(0, 8) }}
              </option>
            </select>
          </div>
        </div>
        <!-- 定时测速：探针多选 chips，默认空=全部探针的曲线同图对比 -->
        <div v-if="f.type === 'speed'" class="hist-chips d-flex flex-wrap gap-2 align-items-center">
          <span class="text-secondary small">探针</span>
          <button type="button" class="btn btn-sm" :class="!f.probes.length ? 'btn-primary' : 'btn-outline-secondary'"
            title="不选=全部探针一起显示" @click="setProbes([])">全部</button>
          <button v-for="p in probes" :key="p.id" type="button" class="btn btn-sm"
            :class="f.probes.includes(p.id) ? 'btn-primary' : 'btn-outline-secondary'" @click="toggleProbe(p.id)">
            {{ p.name || p.hostname || p.id.slice(0, 8) }}
          </button>
        </div>
        <div v-else-if="showTarget && targetOptions.length" class="hist-chips d-flex flex-wrap gap-2 align-items-center">
          <span class="text-secondary small">目标</span>
          <button type="button" class="btn btn-sm" :class="!f.targets.length ? 'btn-primary' : 'btn-outline-secondary'"
            title="不选=显示全部目标的曲线" @click="setTargets([])">全部</button>
          <button v-for="t in targetOptions" :key="t" type="button" class="btn btn-sm text-truncate" style="max-width: 220px"
            :class="f.targets.includes(t) ? 'btn-primary' : 'btn-outline-secondary'" @click="toggleTarget(t)">{{ t }}</button>
        </div>
      </div>
      <div v-if="err" class="text-danger small mt-2">{{ err }}</div>
      <div v-if="warnings.length" class="text-warning small mt-2">部分类型查询失败：{{ warnings.join('；') }}</div>
    </div>

    <!-- 窗口趋势：VM 页签=可用性方格条+时延/速率折线；IP 扫描页签=批次在线率条+在线数折线 -->
    <div class="nw-card mb-3">
      <div class="d-flex align-items-center flex-wrap gap-2 mb-2">
        <h6 class="mb-0">{{ f.type === 'ipscan' ? '扫描趋势' : '窗口趋势' }}</h6>
        <span v-if="f.type !== 'ipscan'" class="text-secondary small">
          每格 ≈ {{ bucketSpanText }} · {{ f.type === 'speed' ? '折线为下载速率' : '折线为平均时延' }}
        </span>
        <span v-else class="text-secondary small">每格 = 一次扫描批次 · 折线为在线 IP 数</span>
        <template v-if="zoomed">
          <span class="badge text-bg-primary">{{ fmtRange(view.start) }} ~ {{ fmtRange(view.end) }}</span>
          <button type="button" class="btn btn-sm btn-outline-secondary" @click="resetZoom">复位</button>
        </template>
        <span v-if="f.type !== 'ipscan'" class="ms-auto small text-secondary d-inline-flex align-items-center gap-2">
          <span class="hbg-dot hbg-ok"></span>无丢包
          <span class="hbg-dot hbg-partial"></span>有丢包（未超阈值）
          <span class="hbg-dot hbg-degraded"></span>降级（丢包超阈值）
          <span class="hbg-dot hbg-fail"></span>全部失败
          <span class="hbg-dot hbg-nodata"></span>暂停/无探测
        </span>
        <span v-else class="ms-auto small text-secondary d-inline-flex align-items-center gap-2">
          <span class="hbg-dot hbg-ok"></span>较上一批持平/回升
          <span class="hbg-dot hbg-warn"></span>较上一批下降
          <span class="hbg-dot hbg-fail"></span>较上一批骤降
        </span>
      </div>
      <div class="trend-zoom" :class="{ dragging, zoomed }" @wheel.prevent="onWheel" @mousedown.prevent="onDown" @dblclick="resetZoom">
        <!-- VM 页签：方格条（≤240 桶）+ 刻度 + 多系列图例 -->
        <template v-if="f.type !== 'ipscan'">
          <!-- VM 全窗口趋势方格条 -->
          <!-- 方格条槽位固定 22px（有数据=方格条，无数据=同高提示），避免模块跳动 -->
          <div style="height: 22px">
            <div class="hbg hbg-fill" v-if="displayBuckets.length && displayBuckets.some(b => b.n > 0)">
              <i v-for="(b, i) in displayBuckets" :key="i" :class="bucketClass(b)" :title="bucketTitle(b)"></i>
            </div>
            <div v-else class="text-secondary small text-center" style="line-height: 22px">当前查看范围暂无数据</div>
          </div>
          <!-- 方格条时间刻度：缩放时首/中/末刻度随之变化，联动一眼可见；行高恒定防跳动 -->
          <div class="d-flex justify-content-between text-secondary mt-1" style="font-size: 10.5px; line-height: 14px">
            <span>{{ displayBuckets.length ? fmtBucket(displayBuckets[0].ts) : '\u00a0' }}</span>
            <span>{{ displayBuckets.length ? fmtBucket(displayBuckets[Math.floor(displayBuckets.length / 2)].ts) : '\u00a0' }}</span>
            <span>{{ displayBuckets.length ? fmtBucket(displayBuckets[displayBuckets.length - 1].ts + bucketSpan()) : '\u00a0' }}</span>
          </div>
          <!-- 多系列图例：4 列网格整齐分行；常驻三行高度，页签/缩放切换时画布与下方模块不跳；
               点击隐藏或显示对应折线（与 Chart.js 内置图例交互等价，但排布整齐可控）。
               mousedown/dblclick 阻止冒泡：点图例不触发趋势区拖拽/复位 -->
          <div class="hist-legend" @mousedown.stop @dblclick.stop>
            <button v-for="(s, i) in displaySeries" :key="seriesKey(s)" type="button" class="hist-legend-item"
              :class="{ off: hiddenKeys.has(seriesKey(s)) }" :title="seriesName(s) + '（点击隐藏/显示）'"
              @click="toggleSeries(s)">
              <span class="hist-legend-dot" :style="{ background: colorOf(s) }"></span>
              <span class="hist-legend-txt">{{ seriesName(s) }}</span>
            </button>
          </div>
        </template>
        <!-- IP 扫描页签：批次在线率条 + 刻度 + 在线率图例（块高与 VM 页签图例区一致，页签切换不跳） -->
        <template v-else>
          <div style="height: 22px">
            <div class="hbg hbg-fill" v-if="ipCells.length">
              <i v-for="(c, i) in ipCells" :key="i" :class="c.cls" :title="c.title"></i>
            </div>
            <div v-else class="text-secondary small text-center" style="line-height: 22px">窗口内暂无扫描记录</div>
          </div>
          <div class="d-flex justify-content-between text-secondary mt-1" style="font-size: 10.5px; line-height: 14px">
            <span>{{ ipTicks[0] || '\u00a0' }}</span>
            <span>{{ ipTicks[1] || '\u00a0' }}</span>
            <span>{{ ipTicks[2] || '\u00a0' }}</span>
          </div>
          <div class="hist-legend">
            <span class="hist-legend-item" style="cursor: default"><span class="hbg-dot hbg-ok"></span>与上一批次相比 ≥ 90%（持平/回升）</span>
            <span class="hist-legend-item" style="cursor: default"><span class="hbg-dot hbg-warn"></span>与上一批次相比 60% ~ 90%（下降）</span>
            <span class="hist-legend-item" style="cursor: default"><span class="hbg-dot hbg-fail"></span>与上一批次相比 &lt; 60%（骤降，疑似故障）</span>
          </div>
        </template>
        <div style="height: 280px" class="mt-2"><canvas id="histTrend"></canvas></div>
      </div>
      <!-- 放大后的横拉条：拖动滑块在完整窗口内平移查看范围（滚轮缩放后出现） -->
      <div v-if="zoomed && f.type !== 'ipscan'" class="hist-scrub mt-2" title="拖动平移查看范围"
           @mousedown.prevent="onScrubDown" @dblclick.stop="resetZoom">
        <div class="hist-scrub-thumb" :style="scrubThumbStyle"></div>
      </div>
      <div v-if="f.type !== 'ipscan'" class="text-secondary small mt-1">趋势区交互：滚轮缩放 · 拖动平移 · 双击复位（放大后可用下方横拉条平移；KPI 与明细表仍为完整窗口口径）</div>
      <div v-else class="text-secondary small mt-1">批次条颜色 = 本批在线数与「上一批次」（时间上紧邻的前一次扫描，同楼层范围）对比：≥90% 绿 / 60%~90% 黄 / &lt;60% 红，悬停格子可见具体对比数值；首个批次无基准不判色。绝对在线率是子网地址占用率，随昼夜周期天然波动，不作判色依据。点击记录行的「格子墙」前往 IP 扫描页查看逐 IP 明细。</div>
    </div>

    <!-- VM 页签明细表：服务端分页，浏览器任何时刻只渲染一页；加载指示为覆盖层，不挤动布局 -->
    <div class="nw-card" v-if="f.type !== 'ipscan'">
      <div class="position-relative">
        <div v-if="loading" class="nw-loading">加载中…</div>
        <table class="table nw-table table-sm table-fixed mb-0">
        <thead>
          <tr>
            <th style="width:17%">时间</th>
            <th v-if="f.type === 'all'" style="width:9%">类型</th>
            <th :style="f.type === 'all' ? 'width:22%' : 'width:25%'">目标</th>
            <th v-if="showClass" style="width:8%">类别</th>
            <th style="width:11%">结果</th>
            <th style="width:15%">时延 / 数值</th>
            <th>附加信息</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(r, i) in records" :key="i" :class="{ 'rec-fail': !r.ok }">
            <td>{{ fmtTs(r.ts_ms) }}</td>
            <td v-if="f.type === 'all'">{{ typeLabel(r.type) }}</td>
            <td class="text-break">{{ targetCell(r) }}</td>
            <td v-if="showClass">{{ classLabel(r.class) }}</td>
            <td><span :class="r.ok ? 'text-success' : 'text-danger'">{{ r.ok ? '✓ 成功' : '× 失败' }}</span></td>
            <td>{{ valText(r) }}</td>
            <td>{{ extraText(r) }}</td>
          </tr>
          <tr v-if="!records.length && !loading">
            <td :colspan="colCount" class="text-center text-secondary py-3">无匹配记录（可放宽筛选或扩大时间范围）</td>
          </tr>
        </tbody>
      </table>
      </div>
      <!-- 分页：与扫描记录页同款（独立控件 + gap-3） -->
      <div class="d-flex align-items-center flex-wrap gap-3 mt-2" v-if="total > 0">
        <span class="small text-secondary">共 {{ total }} 条</span>
        <select v-model.number="pageSize" class="form-select form-select-sm w-auto" @change="reload" title="每页显示条数">
          <option :value="20">每页 20 条</option>
          <option :value="50">每页 50 条</option>
          <option :value="100">每页 100 条</option>
        </select>
        <div class="ms-auto d-flex align-items-center gap-3">
          <button type="button" class="btn btn-sm btn-outline-secondary" :disabled="page <= 1 || loading" @click="goto(page - 1)">上一页</button>
          <span class="small text-secondary">第 {{ page }} / {{ pageCount }} 页</span>
          <button type="button" class="btn btn-sm btn-outline-secondary" :disabled="page >= pageCount || loading" @click="goto(page + 1)">下一页</button>
        </div>
      </div>
      <div v-if="csvNote" class="text-secondary small mt-1">{{ csvNote }}</div>
    </div>

    <!-- IP 扫描页签：扫描记录表（每网段一行，服务端分页；「格子墙」跳转 IP 扫描页看逐 IP 明细） -->
    <div class="nw-card" v-else>
      <div class="position-relative">
        <div v-if="loading" class="nw-loading">加载中…</div>
        <table class="table nw-table table-sm table-fixed mb-0">
        <thead>
          <tr>
            <th style="width:17%">时间</th>
            <th style="width:13%">楼层</th>
            <th style="width:18%">网段</th>
            <th style="width:9%">在线</th>
            <th style="width:9%">总数</th>
            <th style="width:10%">在线率</th>
            <th style="width:9%">时长</th>
            <th style="width:8%">触发</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in ipRuns" :key="r.id">
            <td>{{ fmtTs(r.created_at) }}</td>
            <td>{{ r.floor_name || '—' }}</td>
            <td class="text-break">{{ r.cidr }}</td>
            <td class="fw-bold text-success">{{ r.up }}</td>
            <td>{{ r.total }}</td>
            <td>{{ r.total ? Number(((r.up * 100) / r.total).toFixed(1)) + '%' : '—' }}</td>
            <td>{{ fmtDur(r.duration_ms) }}</td>
            <td>{{ r.trigger === 'schedule' ? '定时' : '手动' }}</td>
            <td><router-link class="btn btn-sm btn-outline-secondary" :to="r.floor_id ? '/scan?floor=' + r.floor_id : '/scan'">格子墙</router-link></td>
          </tr>
          <tr v-if="!ipRuns.length && !loading">
            <td colspan="9" class="text-center text-secondary py-3">窗口内暂无扫描记录（可扩大时间范围或到 IP 扫描页手动执行）</td>
          </tr>
        </tbody>
      </table>
      </div>
      <div class="d-flex align-items-center flex-wrap gap-3 mt-2" v-if="total > 0">
        <span class="small text-secondary">共 {{ total }} 条</span>
        <select v-model.number="pageSize" class="form-select form-select-sm w-auto" @change="reload" title="每页显示条数">
          <option :value="20">每页 20 条</option>
          <option :value="50">每页 50 条</option>
          <option :value="100">每页 100 条</option>
        </select>
        <div class="ms-auto d-flex align-items-center gap-3">
          <button type="button" class="btn btn-sm btn-outline-secondary" :disabled="page <= 1 || loading" @click="goto(page - 1)">上一页</button>
          <span class="small text-secondary">第 {{ page }} / {{ pageCount }} 页</span>
          <button type="button" class="btn btn-sm btn-outline-secondary" :disabled="page >= pageCount || loading" @click="goto(page + 1)">下一页</button>
        </div>
      </div>
      <div v-if="csvNote" class="text-secondary small mt-1">{{ csvNote }}</div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import dayjs from 'dayjs'
import Chart from 'chart.js/auto'
import { api } from '../api'

const LS_KEY = 'nw_history_filters'
const route = useRoute()
const MAX_BUCKETS = 240 // 与后端 histStatsBuckets 一致

const records = ref([]), buckets = ref([]), stats = ref(null)
const total = ref(0), page = ref(1), pageSize = ref(20)
const err = ref(''), loading = ref(false), csvBusy = ref(false), csvNote = ref('')
const warnings = ref([])
const probes = ref([]), targetOptions = ref([])
const series = ref([]), viewSeries = ref([]) // 折线图多指标系列（类型×目标各一条线）
const bucketSpanSec = ref(0)   // 完整窗口的桶跨度（秒），驱动每格文案/悬停时间
const expectedIntvSec = ref(0) // 类型期望探测间隔（缩放最小范围钳位用）
const lossThresh = ref(10)     // 降级丢包阈值（接口下发，与 P2 告警同口径）
const view = ref(null)         // 当前查看范围 {start,end}（unix 秒）；null=完整窗口
const viewBuckets = ref([])    // 放大后细化的桶（缩放稳定后按查看范围拉取）
const viewSpanSec = ref(0)     // 查看范围的桶跨度
const dragging = ref(false)
// 时间范围统一默认最近 24 小时（2026-11-08 用户要求：所有页签一致，不跨页签/会话记忆）
const f = ref({ range: '86400', start: '', end: '', type: 'all', probe: '', probes: [], targets: [], status: '', floor: 0 })

let trendChart = null
let reqSeq = 0 // 请求序号：筛选快速切换时丢弃过期响应
let queryWin = { start: 0, end: 0 } // 本次查询的完整窗口（缩放平移的边界）
let dragX0 = 0, dragW = 1, dragView = null
let viewSeq = 0, viewTimer = null

// 类型 → VM 指标（labels 下拉枚举用）
const TYPE_METRIC = {
  ping: 'nw_ping_ok', tcp: 'nw_tcp_ok', dns: 'nw_dns_ok', http: 'nw_http_ok',
  monitor: 'nw_monitor_ok', industry: 'nw_industry_ok', floornet: 'nw_floor_net_ok',
  speed: 'nw_speed_down_mbps',
}
const TYPE_LABEL = { ping: 'Ping', tcp: 'TCP', dns: 'DNS', http: 'HTTP', monitor: '内→内', industry: '行业网', floornet: '楼层网络', speed: '测速' }
// 类型页签（标题下方切换，替代下拉）
const typeTabs = [
  { v: 'all', label: '全部' },
  { v: 'ping', label: '连通 Ping' },
  { v: 'tcp', label: 'TCP 端口' },
  { v: 'dns', label: 'DNS 解析' },
  { v: 'http', label: 'HTTP 网页' },
  { v: 'monitor', label: '内→内监控' },
  { v: 'industry', label: '行业网监测' },
  { v: 'floornet', label: '楼层网络' },
  { v: 'speed', label: '定时测速' },
  { v: 'ipscan', label: 'IP 扫描' },
]

const needProbe = computed(() => f.value.type !== 'monitor' && f.value.type !== 'industry' && f.value.type !== 'floornet' && f.value.type !== 'ipscan')
// 时间范围选项：测速周期=1 小时，测速页签删去 1/6 小时（8 小时起步）；默认选择均为最近 24 小时
const rangeOptions = computed(() => {
  const base = [['28800', '最近 8 小时'], ['86400', '最近 24 小时'], ['259200', '最近 3 天'],
    ['604800', '最近 7 天'], ['2592000', '最近 30 天'], ['custom', '自定义']]
  if (f.value.type === 'speed') return base
  return [['3600', '最近 1 小时'], ['21600', '最近 6 小时'], ...base]
})
const showTarget = computed(() => f.value.type !== 'all' && f.value.type !== 'speed' && f.value.type !== 'ipscan')
const showClass = computed(() => f.value.type === 'ping' || f.value.type === 'floornet' || f.value.type === 'all')
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)))
const colCount = computed(() => 5 + (f.value.type === 'all' ? 1 : 0) + (showClass.value ? 1 : 0))
const zoomed = computed(() => !!view.value && queryWin.start > 0 &&
  (view.value.start > queryWin.start || view.value.end < queryWin.end))
const minZoomSpan = computed(() => Math.max(120, expectedIntvSec.value || 0)) // 最小查看范围 ≥ 1 个探测周期
// 趋势展示（缩放性能核心）：基线桶按 720 细粒度加载，任意视图先在客户端
// 即时聚合到 ≤240 显示格（≤6× 缩放零网络请求）；只有更深放大（基线格 <120 个）
// 才按需向服务端细化（250ms 防抖），细化返回前用基线桶即时呈现。
// display = { cells, span(显示格宽度秒), series(聚合后), refined }
const display = computed(() => {
  const v = view.value
  if (v && viewBuckets.value.length) {
    // 深度缩放：服务端细化结果直接用
    return { cells: viewBuckets.value, span: viewSpanSec.value || bucketSpanSec.value, series: viewSeries.value }
  }
  const fs = bucketSpanSec.value || 1
  let base = buckets.value
  let sers = series.value
  if (v) {
    base = base.filter((b) => b.ts + fs > v.start && b.ts < v.end)
    sers = sers
      .map((s) => ({ ...s, buckets: s.buckets.filter((b) => b.ts + fs > v.start && b.ts < v.end) }))
      .filter((s) => s.buckets.length)
  }
  // 聚合到 ≤240 显示格（相邻格合并，n/ok 累加、均值按样本数加权）
  const k = Math.max(1, Math.ceil(base.length / MAX_BUCKETS))
  const merge = (arr) => {
    if (k <= 1) return arr
    const out = []
    for (let i = 0; i < arr.length; i += k) {
      const grp = arr.slice(i, i + k)
      let n = 0, ok = 0, rsum = 0, rn = 0, pn = 0, plsum = 0
      for (const b of grp) {
        n += b.n; ok += b.ok_n
        if (b.n > 0 && b.avg_rtt_ms > 0) { rsum += b.avg_rtt_ms * b.n; rn += b.n }
        if (b.pn > 0) { pn += b.pn; plsum += (b.pl || 0) * b.pn } // 丢包中位数按 ping 样本数加权
      }
      out.push({ ts: grp[0].ts, n, ok_n: ok, avg_rtt_ms: rn ? rsum / rn : 0, w: grp.length * fs,
        pn, pl: pn ? plsum / pn : 0 })
    }
    return out
  }
  return { cells: merge(base), span: fs * k, series: sers.map((s) => ({ ...s, buckets: merge(s.buckets) })) }
})
const displayBuckets = computed(() => display.value.cells)
const displaySeries = computed(() => display.value.series
  .map((s) => ({ ...s, map: seriesMap(s) }))
  .sort((a, b) => seriesName(a).localeCompare(seriesName(b), 'zh-Hans-CN', { numeric: true })))
const dispSpanSec = computed(() => display.value.span || bucketSpanSec.value)
const okRatePct = computed(() => (stats.value && stats.value.total ? Number(((stats.value.ok * 100) / stats.value.total).toFixed(1)) : 0))
const okRateClass = computed(() => {
  if (!stats.value || !stats.value.total) return ''
  if (okRatePct.value >= 99.5) return 'text-success'
  if (okRatePct.value < 95) return 'text-danger'
  return ''
})

// 定时测速平均下载分两组：probe-12 线路协议带宽不同，单独统计，其余探针一组（显示 304.4/95）。
// 用完整窗口的系列桶按样本数加权（与 KPI 完整窗口口径一致，不受趋势区缩放影响）
const SPEED_SPLIT_PROBE = 'probe-12'
const speedAvgSplit = computed(() => {
  if (f.value.type !== 'speed') return null
  let aSum = 0, aN = 0, bSum = 0, bN = 0 // a=其余探针, b=probe-12
  for (const s of series.value) {
    if (s.type !== 'speed') continue
    const isB = (probeNameOf(s.probe) || '').trim() === SPEED_SPLIT_PROBE
    let sum = 0, n = 0
    for (const b of s.buckets || []) {
      if (b.n > 0 && b.avg_rtt_ms > 0) { sum += b.avg_rtt_ms * b.n; n += b.n }
    }
    if (isB) { bSum += sum; bN += n } else { aSum += sum; aN += n }
  }
  return { a: aN ? aSum / aN : 0, b: bN ? bSum / bN : 0 }
})
const speedAvgText = computed(() => {
  if (f.value.type !== 'speed') {
    return stats.value && stats.value.avg_rtt_ms > 0 ? fmtNum(stats.value.avg_rtt_ms) : '—'
  }
  const sp = speedAvgSplit.value
  if (!sp) return '—'
  if (sp.a > 0 && sp.b > 0) return fmtNum(sp.a) + '/' + fmtNum(sp.b)
  if (sp.a > 0 || sp.b > 0) return fmtNum(sp.a || sp.b)
  return '—'
})

// ===== IP 扫描页签：数据源是 scan_runs（PG），非 VM 指标 =====
const floors = ref([])         // 楼层下拉（页签首次进入时加载）
const ipRuns = ref([])         // 扫描记录（服务端分页，每网段一行）
const ipBatches = ref([])      // 批次聚合（KPI/心跳条/在线趋势）
const ipSum = computed(() => {
  const bs = ipBatches.value
  if (!bs || !bs.length) return null
  let up = 0, tot = 0
  for (const b of bs) { up += b.up; tot += b.total }
  const last = bs[bs.length - 1]
  return { n: bs.length, total: tot, rate: tot ? Number(((up * 100) / tot).toFixed(1)) : 0,
    lastUp: last.up, lastTotal: last.total, lastAt: last.t }
})
// 批次心跳条：一格一个批次（>240 批相邻合并，在线数加权）。颜色=本批在线数与上一批次对比
// （绝对在线率是子网地址占用率，天然低值且随昼夜周期波动，不适用阈值着色；只标批次间突降）。
// 悬停写明「与谁比、比的结果」，无需猜「环比」含义；首个批次无基准不判色
const ipCells = computed(() => {
  const bs = ipBatches.value
  const k = Math.max(1, Math.ceil(bs.length / MAX_BUCKETS))
  const raw = []
  for (let i = 0; i < bs.length; i += k) {
    const grp = bs.slice(i, i + k)
    let up = 0, tot = 0
    for (const b of grp) { up += b.up; tot += b.total }
    raw.push({ t: grp[0].t, w: grp[grp.length - 1].t - grp[0].t + 60,
      label: grp.length > 1 ? grp.length + ' 个批次' : grp[0].runs + ' 个网段', up, tot })
  }
  return raw.map((c, i) => {
    const rate = c.tot ? Number(((c.up * 100) / c.tot).toFixed(1)) : 0
    let cls = 'hbg-ok', cmp = '首个批次，无对比基准'
    if (i > 0) {
      const p = raw[i - 1]
      if (p.up > 0) {
        const r = c.up / p.up
        cls = r >= 0.9 ? 'hbg-ok' : r >= 0.6 ? 'hbg-warn' : 'hbg-fail'
        const pct = Number(((r - 1) * 100).toFixed(1))
        cmp = '与上一批 ' + fmtBucket(p.t) + '（在线 ' + p.up + '）相比 ' + (pct > 0 ? '+' : '') + pct + '%'
      } else {
        cls = c.up > 0 ? 'hbg-ok' : 'hbg-fail'
        cmp = '上一批在线为 0' + (c.up > 0 ? '，本批回升' : '')
      }
    }
    return { cls, title: fmtBucket(c.t) + '–' + fmtBucket(c.t + c.w) + ' · ' + c.label +
      ' · 在线 ' + c.up + '/' + c.tot + '（' + rate + '%）· ' + cmp }
  })
})
const ipTicks = computed(() => {
  const bs = ipBatches.value
  if (!bs.length) return ['', '', '']
  const mid = bs[Math.floor(bs.length / 2)]
  return [fmtBucket(bs[0].t), fmtBucket(mid.t), fmtBucket(bs[bs.length - 1].t + 60)]
})
const fmtDur = (ms) => {
  if (!ms || ms < 0) return '—'
  const sec = Math.round(ms / 1000)
  if (sec < 60) return sec + 's'
  return Math.floor(sec / 60) + 'm' + String(sec % 60).padStart(2, '0') + 's'
}
async function loadFloors() {
  if (floors.value.length) return
  try { floors.value = (await api.get('/api/admin/floors')) || [] } catch (e) { /* 下拉失败不阻塞 */ }
}
async function loadIpScan() {
  const w = windowSecs()
  const seq = ++reqSeq
  loading.value = true
  err.value = ''
  try {
    const base = { start: String(w.start), end: String(w.end) }
    if (f.value.floor) base.floor_id = String(f.value.floor)
    const [sum, runs] = await Promise.all([
      api.get('/api/admin/scan/summary?' + new URLSearchParams(base)),
      api.get('/api/admin/scan/runs?' + new URLSearchParams({
        ...base, limit: String(pageSize.value), offset: String((page.value - 1) * pageSize.value) })),
    ])
    if (seq !== reqSeq) return
    ipBatches.value = sum.batches || []
    ipRuns.value = runs.rows || []
    total.value = runs.total || 0
    stats.value = null
    // 清掉上一页签遗留的 VM 桶跨度，否则 fmtBucket 用旧跨度给批次时间戳格式化（出现秒级噪音）
    buckets.value = []; series.value = []; warnings.value = []; bucketSpanSec.value = 0
    queryWin = { start: 0, end: 0 }
    resetZoomState()
    if (page.value > pageCount.value) { page.value = pageCount.value; return load() }
    await nextTick()
    renderTrend()
  } catch (e) { if (seq === reqSeq) err.value = e.message }
  finally { if (seq === reqSeq) loading.value = false }
}
// 扫描批次 CSV（窗口内全部批次，≤2000）
function exportIpCSV() {
  const bs = ipBatches.value || []
  const rows = [['批次时间', '网段数', '在线 IP', '扫描 IP', '在线率%']]
  for (const b of bs) {
    rows.push([fmtTs(b.t * 1000), b.runs, b.up, b.total, b.total ? Number(((b.up * 100) / b.total).toFixed(1)) : ''])
  }
  const blob = new Blob(['\uFEFF' + rows.join('\n')], { type: 'text/csv;charset=utf-8' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `netwatch-ipscan-${dayjs().format('YYYYMMDD-HHmmss')}.csv`
  a.click()
  URL.revokeObjectURL(a.href)
  csvNote.value = `已导出 ${bs.length} 个扫描批次`
}

const fmtNum = (x) => (x === undefined || x === null || isNaN(x)) ? '—' : Number(Number(x).toFixed(1))
const fmtTs = (ms) => dayjs(ms).format('YYYY-MM-DD HH:mm:ss')
const fmtRange = (ts) => dayjs.unix(ts).format('MM-DD HH:mm')
const typeLabel = (t) => TYPE_LABEL[t] || t
const classLabel = (c) => (c === 'gw' ? '网关' : (c === 'public' ? '公网' : (c === 'gateway' ? '核心网关' : (c === 'anchor' ? '汇聚设备' : '—'))))

// 多系列折线：调色板 + 系列名（全部类型时加类型前缀，避免同名目标混淆）
const PALETTE = ['#2f81f7', '#05c96b', '#f0883e', '#d6506e', '#9d7df0', '#00b8d9',
  '#c9d12f', '#ff7043', '#5c6bc0', '#66bb6a', '#ffa726', '#ec407a']
const probeNameOf = (id) => (probes.value.find((p) => p.id === id) || {}).name || (id || '').slice(0, 8)
// 测速目标名（cdn-multi×16conn 等引擎术语）是噪音：仅当同一探针存在多个测速目标时才追加用于区分
const speedTargetCounts = computed(() => {
  const m = {}
  for (const s of display.value.series) { // 读 display（排序前）而非 displaySeries，避免循环依赖
    if (s.type !== 'speed') continue
    const k = s.probe || ''
    m[k] = (m[k] || 0) + 1
  }
  return m
})
const seriesName = (s) => {
  const parts = []
  if (f.value.type === 'all') {
    parts.push(TYPE_LABEL[s.type] || s.type)
    // 测速目标名是引擎术语：该探针仅一个测速目标时省略（与测速页签同规则）
    if (s.type === 'speed' && (speedTargetCounts.value[s.probe || ''] || 0) <= 1) return parts.join(' ')
  }
  if (f.value.type === 'speed') {
    parts.push(probeNameOf(s.probe))
    if ((speedTargetCounts.value[s.probe || ''] || 0) > 1) parts.push(s.target)
    return parts.join(' ')
  }
  parts.push(s.target)
  return parts.join(' ')
}
// 明细表目标列：speed 类型同样简化（probe-N，多目标时才带目标名）
const targetCell = (r) => {
  if (f.value.type === 'speed') {
    const name = probeNameOf(r.probe)
    return (speedTargetCounts.value[r.probe || ''] || 0) > 1 ? name + ' · ' + (r.target || '') : name
  }
  return r.target || '—'
}
const seriesMap = (s) => {
  const m = {}
  for (const b of s.buckets || []) if (b.n > 0 && b.avg_rtt_ms > 0) m[b.ts] = b.avg_rtt_ms
  return m
}

// 图例显隐：点击图例隐藏/显示对应折线；键=类型×探针×目标，切换页签时重置
const hiddenKeys = ref(new Set())
const seriesKey = (s) => (s.type || '') + '\x00' + (s.probe || '') + '\x00' + (s.target || '')
function toggleSeries(s) {
  const k = seriesKey(s)
  const next = new Set(hiddenKeys.value)
  if (next.has(k)) next.delete(k)
  else next.add(k)
  hiddenKeys.value = next
  renderTrend()
}
// 系列配色：按完整窗口的系列序取色——缩放中某系列暂无数据点时不致颜色漂移
const seriesColors = computed(() => {
  const all = display.value.series
    .map((s) => ({ ...s }))
    .sort((a, b) => seriesName(a).localeCompare(seriesName(b), 'zh-Hans-CN', { numeric: true }))
  const m = {}
  all.forEach((s, i) => { m[seriesKey(s)] = PALETTE[i % PALETTE.length] })
  return m
})
const colorOf = (s) => seriesColors.value[seriesKey(s)] || PALETTE[0]

function windowSecs() {
  if (f.value.range === 'custom') {
    return { start: f.value.start ? dayjs(f.value.start).unix() : 0, end: f.value.end ? dayjs(f.value.end).unix() : 0 }
  }
  const end = Math.floor(Date.now() / 1000)
  return { start: end - Number(f.value.range), end }
}

// 桶跨度：优先当前显示聚合的跨度（客户端聚合后=基线格×合并数），其次完整窗口
// 的 bucket_span（已按类型期望探测间隔自适应），缺失时退回窗口/240 估算
function bucketSpan() {
  if (dispSpanSec.value > 0) return dispSpanSec.value
  if (bucketSpanSec.value > 0) return bucketSpanSec.value
  const w = windowSecs()
  return Math.max(1, Math.floor((w.end - w.start) / MAX_BUCKETS))
}
const bucketSpanText = computed(() => humanSpan(bucketSpan()))
function humanSpan(s) {
  if (s % 86400 === 0) return (s / 86400) + ' 天'
  if (s % 3600 === 0) return (s / 3600) + ' 小时'
  if (s % 60 === 0) return (s / 60) + ' 分钟'
  if (s > 120) return Number((s / 60).toFixed(1)) + ' 分钟'
  return s + ' 秒'
}
function fmtBucket(ts) {
  const s = bucketSpan()
  if (s >= 86400) return dayjs.unix(ts).format('MM-DD')
  if (s >= 3600) return dayjs.unix(ts).format('MM-DD HH:mm')
  return dayjs.unix(ts).format('MM-DD HH:mm:ss')
}
function bucketClass(b) {
  if (!b.n) return 'hbg-nodata'
  if (b.ok_n <= 0) return 'hbg-fail'
  // 紫格：桶内按目标丢包率下中位数超阈值（后端 PL，与 P2 告警同口径）
  if ((b.pl || 0) > lossThresh.value) return 'hbg-degraded'
  // 橙格：ping 桶有丢包（PL>0）；非 ping 类型无 PL，退化为"有失败"
  if ((b.pl || 0) > 0 || b.ok_n < b.n) return 'hbg-partial'
  return 'hbg-ok'
}
function bucketTitle(b) {
  const w = b.w || bucketSpan()
  if (!b.n) return fmtBucket(b.ts) + '–' + fmtBucket(b.ts + w) + ' · 无探测（暂停/未启用）'
  const pct = Number(((b.ok_n * 100) / b.n).toFixed(1))
  const val = b.avg_rtt_ms > 0 ? ' · 均 ' + Number(b.avg_rtt_ms.toFixed(1)) + (f.value.type === 'speed' ? ' Mbps' : ' ms') : ''
  const loss = b.pn > 0 ? ' · 丢包 ' + Number((b.pl || 0).toFixed(1)) + '%' : ''
  return fmtBucket(b.ts) + '–' + fmtBucket(b.ts + w) + ' · 成功率 ' + pct + '% · ' + b.n + ' 次' + val + loss
}

function valText(r) {
  if (r.type === 'speed') {
    return '↓ ' + fmtNum(r.rtt_ms) + ' Mbps' + (r.up_mbps > 0 ? '　↑ ' + fmtNum(r.up_mbps) : '')
  }
  if (!r.ok || !r.rtt_ms) return '—'
  return fmtNum(r.rtt_ms) + ' ms'
}
function extraText(r) {
  if (r.type === 'http' && r.ok) {
    const parts = []
    if (r.code) parts.push('HTTP ' + r.code)
    if (r.cert_days > 0) parts.push('证书剩 ' + Math.round(r.cert_days) + ' 天')
    return parts.join(' · ') || '—'
  }
  return '—'
}

async function load() {
  if (f.value.type === 'ipscan') return loadIpScan()
  const w = windowSecs()
  if (f.value.range === 'custom' && (!w.start || !w.end || w.start >= w.end)) {
    err.value = '自定义时间无效：请填写开始/结束，且开始早于结束'
    return
  }
  const seq = ++reqSeq
  loading.value = true
  err.value = ''
  csvNote.value = ''
  try {
    const qs = new URLSearchParams({
      type: f.value.type,
      probe: needProbe.value && f.value.type !== 'speed' ? f.value.probe : '',
      probes: f.value.type === 'speed' ? speedProbesParam() : '',
      targets: f.value.targets.join('\n'),
      status: f.value.status, start: String(w.start), end: String(w.end), by_target: '1', buckets: '720',
      limit: String(pageSize.value), offset: String((page.value - 1) * pageSize.value),
    })
    const d = await api.get('/api/admin/history?' + qs.toString())
    if (seq !== reqSeq) return
    records.value = d.records || []
    buckets.value = d.buckets || []
    series.value = (d.series || []).map((s) => ({ ...s, map: seriesMap(s) }))
    bucketSpanSec.value = d.bucket_span || 0
    expectedIntvSec.value = d.expected_intv || 0
    lossThresh.value = d.loss_thresh || 10
    stats.value = d.stats || null
    warnings.value = d.warnings || []
    total.value = d.total || 0
    queryWin = { start: w.start, end: w.end }
    resetZoomState() // 新查询回到完整窗口视图
    if (page.value > pageCount.value) { page.value = pageCount.value; return load() }
    await nextTick()
    renderTrend()
  } catch (e) { if (seq === reqSeq) err.value = e.message }
  finally { if (seq === reqSeq) loading.value = false }
}

// 筛选变化：回第一页并记忆筛选
function reload() { page.value = 1; saveLS(); load() }
function goto(p) { page.value = p; load() }

function onRangeChange() {
  if (f.value.range === 'custom' && !f.value.start) {
    const now = dayjs()
    f.value.start = now.subtract(24, 'hour').format('YYYY-MM-DDTHH:mm')
    f.value.end = now.format('YYYY-MM-DDTHH:mm')
  }
  reload()
}
// 类型页签切换：各类型的目标集合不同，清空多选与图例隐藏状态；时间范围统一回到默认最近 24 小时
function switchType(v) {
  if (f.value.type === v) return
  f.value.type = v
  f.value.targets = []
  hiddenKeys.value = new Set()
  f.value.range = '86400'
  f.value.start = ''
  f.value.end = ''
  if (v === 'ipscan') loadFloors()
  // 从无探针维度页签（IP 扫描/内→内/行业网，探针可能从未选择）切回时兜底选第一台
  if (needProbe.value && !f.value.probe) {
    f.value.probe = (probes.value[0] || {}).id || ''
  }
  loadTargets()
  reload()
}
function onProbeChange() {
  f.value.targets = [] // 探针不同目标集合也不同
  if (showTarget.value) loadTargets()
  reload()
}
// 探针多选（仅定时测速页签）：空数组=全部探针的曲线都上墙（默认全选效果）
function setProbes(arr) { f.value.probes = arr; reload() }
function toggleProbe(id) {
  const i = f.value.probes.indexOf(id)
  if (i >= 0) f.value.probes.splice(i, 1)
  else f.value.probes.push(id)
  reload()
}
function speedProbesParam() {
  return (f.value.probes.length ? f.value.probes : probes.value.map((p) => p.id)).join('\n')
}

// 目标多选（chips）：空数组=全部目标的曲线都上墙
function setTargets(arr) { f.value.targets = arr; reload() }
function toggleTarget(t) {
  const i = f.value.targets.indexOf(t)
  if (i >= 0) f.value.targets.splice(i, 1)
  else f.value.targets.push(t)
  reload()
}

// 目标/监控项下拉：由 VM 标签值枚举（不依赖查询响应，选中后不消失）
async function loadTargets() {
  targetOptions.value = []
  if (!showTarget.value) return
  try {
    const qs = new URLSearchParams({ metric: TYPE_METRIC[f.value.type], label: f.value.type === 'monitor' ? 'monitor' : (f.value.type === 'industry' ? 'industry' : 'target') })
    if (needProbe.value && f.value.probe) qs.set('probe', f.value.probe)
    const d = await api.get('/api/admin/history/labels?' + qs.toString())
    targetOptions.value = d.values || []
  } catch (e) { /* 下拉枚举失败不阻塞查询，仅无候选 */ }
}

async function loadProbes() {
  try {
    const list = await api.get('/api/admin/probes')
    probes.value = (list || []).map((x) => x.probe).filter(Boolean)
  } catch (e) { /* 探针列表失败不阻塞 */ }
}

// 趋势渲染：图表实例复用（缩放/平移时只换数据，避免频繁重建卡顿）。
// 多指标：每个（类型×目标）系列一条线，颜色/显隐由 HTML 图例区管理（Chart.js 内置图例关闭）；
// 全部页签下测速系列画右侧 Mbps 副轴（y1），与左侧 ms 主轴互不干扰。
function renderTrend() {
  const cv = document.getElementById('histTrend')
  if (!cv) return
  const isIp = f.value.type === 'ipscan'
  // 两种数据源共用一个图表实例（同为折线，仅数据/轴标题不同），页签切换平滑过渡
  let labels = [], datasets = [], yTitle = '', beginZero = false, isAll = false, hasData = false
  let sers = []
  if (isIp) {
    const bs = ipBatches.value || []
    hasData = bs.length > 0
    labels = bs.map((b) => fmtBucket(b.t))
    datasets = [{ label: '在线 IP', data: bs.map((b) => b.up), borderColor: '#05c96b',
      backgroundColor: '#05c96b14', borderWidth: 1.6, pointRadius: 0, tension: .25, spanGaps: true, fill: true }]
    yTitle = '在线 IP 数'
    beginZero = true
  } else {
    const pts = displayBuckets.value || []
    sers = displaySeries.value || []
    labels = pts.map((b) => fmtBucket(b.ts))
    const visN = sers.filter((s) => !hiddenKeys.value.has(seriesKey(s))).length
    isAll = f.value.type === 'all'
    yTitle = f.value.type === 'speed' ? '下载速率 (Mbps)' : '时延 (ms)' // 纵轴单位标注
    datasets = sers.map((s) => ({
      label: seriesName(s),
      data: pts.map((b) => (s.map[b.ts] !== undefined) ? Number(s.map[b.ts].toFixed(2)) : null),
      yAxisID: isAll && s.type === 'speed' ? 'y1' : 'y',
      borderColor: colorOf(s),
      backgroundColor: colorOf(s) + '14',
      borderWidth: 1.6, pointRadius: 0, tension: .25, spanGaps: true,
      fill: visN <= 1,
    }))
    hasData = pts.some((b) => b.n > 0) && sers.length > 0
  }
  // 窗口内完全无数据（空页签/纯暂停区）：销毁图表避免残留空坐标轴；280px 槽位高度不变。
  // 该守卫必须在实例复用分支之前，否则复用路径 return 后永远走不到
  if (!hasData) {
    if (trendChart) { trendChart.destroy(); trendChart = null }
    return
  }
  if (trendChart) {
    trendChart.data.labels = labels
    trendChart.data.datasets = datasets
    trendChart.options.scales.y.title.text = yTitle
    trendChart.options.scales.y.beginAtZero = beginZero
    trendChart.options.scales.y1.display = isAll
    // 显隐必须走 setDatasetVisibility（与 Chart.js 图例点击同机制）：
    // meta.hidden 一旦为布尔，update 路径不会回读 dataset.hidden
    sers.forEach((s, i) => trendChart.setDatasetVisibility(i, !hiddenKeys.value.has(seriesKey(s))))
    trendChart.update('none')
    return
  }
  trendChart = new Chart(cv, {
    type: 'line',
    data: { labels, datasets },
    options: {
      responsive: true, maintainAspectRatio: false, animation: false,
      // 悬停只显示最近一条线的名称，避免 index 模式把全部系列塞进大提示框遮挡图表
      interaction: { mode: 'nearest', intersect: false },
      scales: {
        x: { ticks: { maxTicksLimit: 8, color: '#6c757d', font: { size: 10 } }, grid: { display: false } },
        y: { title: { display: true, text: yTitle, color: '#6c757d', font: { size: 10 } },
          beginZero, ticks: { maxTicksLimit: 5, color: '#6c757d', font: { size: 10 } }, grid: { color: 'rgba(0,0,0,.05)' } },
        y1: { display: isAll, position: 'right', grid: { drawOnChartArea: false },
          title: { display: true, text: '下载 (Mbps)', color: '#6c757d', font: { size: 10 } },
          ticks: { maxTicksLimit: 5, color: '#6c757d', font: { size: 10 } } },
      },
      plugins: {
        legend: { display: false },
        tooltip: {
          displayColors: false,
          position: 'nearest',
          callbacks: {
            title: () => '',
            label: (ctx) => ctx.dataset.label || '',
          },
        },
      },
    },
  })
}

// ---------- 趋势区缩放/平移（滚轮缩放 · 拖动平移 · 双击复位） ----------

function resetZoomState() {
  viewSeq++
  clearTimeout(viewTimer)
  view.value = null
  viewBuckets.value = []
  viewSeries.value = []
  viewSpanSec.value = 0
}

function resetZoom() { resetZoomState(); renderTrend() }

// 设置查看范围：清细化数据 → 客户端即时聚合渲染（rAF 合帧，零网络等待）；
// 仅深度放大时按需细化（250ms 防抖）
function setView(v, scheduleFetch) {
  view.value = v
  viewBuckets.value = []
  viewSpanSec.value = 0
  requestRender()
  if (scheduleFetch) scheduleViewFetch()
}

// 图表渲染合帧：滚轮/拖动高频触发时每帧最多重绘一次
let rafId = 0
function requestRender() {
  if (rafId) return
  rafId = requestAnimationFrame(() => { rafId = 0; renderTrend() })
}

// 缩放稳定后按查看范围拉细分桶（limit=1 只要聚合，不动明细表）。
// 仅当视图内基线格不足 120 个（深度放大 >6×）才需要，常见缩放零请求。
function scheduleViewFetch() {
  clearTimeout(viewTimer)
  const v = view.value
  if (!v) return
  const fs = bucketSpanSec.value || 1
  if ((v.end - v.start) / fs >= 120) return
  viewTimer = setTimeout(fetchViewBuckets, 250)
}
async function fetchViewBuckets() {
  if (!view.value) return
  const seq = ++viewSeq
  const qs = new URLSearchParams({
    type: f.value.type,
    probe: needProbe.value && f.value.type !== 'speed' ? f.value.probe : '',
    probes: f.value.type === 'speed' ? speedProbesParam() : '',
    targets: f.value.targets.join('\n'),
    status: f.value.status, start: String(view.value.start), end: String(view.value.end), by_target: '1',
    limit: '1', offset: '0',
  })
  try {
    const d = await api.get('/api/admin/history?' + qs.toString())
    if (seq !== viewSeq || !view.value) return
    viewBuckets.value = d.buckets || []
    viewSeries.value = (d.series || []).map((s) => ({ ...s, map: seriesMap(s) }))
    viewSpanSec.value = d.bucket_span || 0
    renderTrend()
  } catch (e) { /* 细化失败保留粗筛视图 */ }
}

function onWheel(e) {
  if (!queryWin.start) return
  const rect = e.currentTarget.getBoundingClientRect()
  const frac = Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width))
  const cur = view.value || { start: queryWin.start, end: queryWin.end }
  const span = cur.end - cur.start
  const ns = Math.min(queryWin.end - queryWin.start,
    Math.max(minZoomSpan.value, Math.round(span * (e.deltaY < 0 ? 0.8 : 1.25))))
  let s = Math.round(cur.start + frac * (span - ns))
  let en = s + ns
  if (s < queryWin.start) { s = queryWin.start; en = s + ns }
  if (en > queryWin.end) { en = queryWin.end; s = en - ns }
  setView({ start: s, end: en }, true)
}

function onDown(e) {
  if (!queryWin.start) return
  if (!view.value) view.value = { start: queryWin.start, end: queryWin.end }
  viewBuckets.value = [] // 平移后旧细化数据失效，回退粗筛
  viewSpanSec.value = 0
  dragging.value = true
  dragX0 = e.clientX
  dragW = Math.max(1, e.currentTarget.getBoundingClientRect().width)
  dragView = { ...view.value }
  window.addEventListener('mousemove', onMove)
  window.addEventListener('mouseup', onUp)
}

function onMove(e) {
  if (!dragging.value || !dragView) return
  const span = dragView.end - dragView.start
  const dt = Math.round(-(e.clientX - dragX0) / dragW * span)
  let s = dragView.start + dt
  const maxS = queryWin.end - span
  if (s < queryWin.start) s = queryWin.start
  if (s > maxS) s = maxS
  view.value = { start: s, end: s + span }
  requestRender() // 拖动中每帧最多重绘一次图表（方格条由响应式自动更新）
}

function onUp() {
  if (!dragging.value) return
  dragging.value = false
  dragView = null
  cancelAnimationFrame(rafId)
  rafId = 0
  renderTrend()
  scheduleViewFetch()
  window.removeEventListener('mousemove', onMove)
  window.removeEventListener('mouseup', onUp)
}

// ---------- 横拉条（缩放后显示在横轴下方）：拖动滑块在完整窗口内平移 ----------
let scrubX0 = 0, scrubW = 1, scrubTotal = 0, scrubStart = null
const scrubThumbStyle = computed(() => {
  if (!view.value || !queryWin.start) return { display: 'none' }
  const total = Math.max(1, queryWin.end - queryWin.start)
  const span = Math.min(total, Math.max(1, view.value.end - view.value.start))
  const left = Math.min(100, Math.max(0, ((view.value.start - queryWin.start) / total) * 100))
  const width = Math.min(100 - left, Math.max(1.5, (span / total) * 100))
  return { left: left + '%', width: width + '%' }
})

function onScrubDown(e) {
  if (!view.value || !queryWin.start) return
  scrubX0 = e.clientX
  scrubW = Math.max(1, e.currentTarget.getBoundingClientRect().width)
  scrubTotal = queryWin.end - queryWin.start
  scrubStart = { ...view.value }
  window.addEventListener('mousemove', onScrubMove)
  window.addEventListener('mouseup', onScrubUp)
}

function onScrubMove(e) {
  if (!scrubStart) return
  const span = scrubStart.end - scrubStart.start
  const dt = Math.round((e.clientX - scrubX0) / scrubW * scrubTotal)
  let s = scrubStart.start + dt
  const maxS = queryWin.end - span
  if (s < queryWin.start) s = queryWin.start
  if (s > maxS) s = maxS
  view.value = { start: s, end: s + span }
  requestRender()
}

function onScrubUp() {
  if (!scrubStart) return
  scrubStart = null
  renderTrend()
  scheduleViewFetch()
  window.removeEventListener('mousemove', onScrubMove)
  window.removeEventListener('mouseup', onScrubUp)
}

// CSV：同筛选一次性拉最近 2 万条（服务端分页上限），前端拼装下载
async function exportCSV() {
  if (f.value.type === 'ipscan') return exportIpCSV()
  if (csvBusy.value) return
  const w = windowSecs()
  if (f.value.range === 'custom' && (!w.start || !w.end || w.start >= w.end)) {
    err.value = '自定义时间无效：请填写开始/结束，且开始早于结束'
    return
  }
  csvBusy.value = true
  err.value = ''
  try {
    const qs = new URLSearchParams({
      type: f.value.type,
      probe: needProbe.value && f.value.type !== 'speed' ? f.value.probe : '',
      probes: f.value.type === 'speed' ? speedProbesParam() : '',
      targets: f.value.targets.join('\n'),
      status: f.value.status, start: String(w.start), end: String(w.end), limit: '20000', offset: '0',
    })
    const d = await api.get('/api/admin/history?' + qs.toString())
    const recs = d.records || []
    const cell = (v) => { const s = String(v ?? ''); return /[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s }
    const rows = [['时间', '类型', '探针', '目标', '类别', '结果', '时延ms', '下载Mbps', '上传Mbps', 'HTTP状态码', '证书剩余天']]
    for (const r of recs) {
      rows.push([
        dayjs(r.ts_ms).format('YYYY-MM-DD HH:mm:ss'), typeLabel(r.type), probeNameOf(r.probe), r.target,
        r.class === 'gw' ? '网关' : (r.class === 'public' ? '公网' : (r.class === 'gateway' ? '核心网关' : (r.class === 'anchor' ? '汇聚设备' : ''))),
        r.ok ? '成功' : '失败',
        r.type === 'speed' ? '' : (r.rtt_ms || ''),
        r.type === 'speed' ? (r.rtt_ms || '') : '',
        r.up_mbps || '', r.code || '', r.cert_days > 0 ? Math.round(r.cert_days) : '',
      ].map(cell).join(','))
    }
    const blob = new Blob(['\uFEFF' + rows.join('\n')], { type: 'text/csv;charset=utf-8' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `netwatch-history-${f.value.type}-${dayjs().format('YYYYMMDD-HHmmss')}.csv`
    a.click()
    URL.revokeObjectURL(a.href)
    if ((d.total || 0) > recs.length) {
      csvNote.value = `已导出最近 ${recs.length} 条（窗口内共 ${d.total} 条，超出单次导出上限 2 万，请缩小时间范围或加筛选）`
    } else {
      csvNote.value = `已导出 ${recs.length} 条`
    }
  } catch (e) { err.value = e.message }
  finally { csvBusy.value = false }
}

function saveLS() { try { localStorage.setItem(LS_KEY, JSON.stringify(f.value)) } catch (e) { /* 忽略 */ } }
function loadLS() {
  try {
    const s = localStorage.getItem(LS_KEY)
    if (s) {
      Object.assign(f.value, JSON.parse(s))
      if (!Array.isArray(f.value.targets)) f.value.targets = [] // 旧版存的是单值 target，直接弃用
      if (!Array.isArray(f.value.probes)) f.value.probes = []
      f.value.floor = Number(f.value.floor) || 0
    }
  } catch (e) { /* 忽略 */ }
  // 时间范围不记忆：无论 localStorage 存过什么（含旧版 rangeMemo），进入页面一律默认最近 24 小时
  f.value.range = '86400'
  f.value.start = ''
  f.value.end = ''
}

onMounted(async () => {
  loadLS()
  // URL 查询参数预选（IP 扫描页「历史汇总」入口带 type=ipscan&floor=N 跳转）
  const qt = String(route.query.type || '')
  if (typeTabs.some((t) => t.v === qt)) f.value.type = qt
  const qf = Number(route.query.floor || 0)
  if (qf > 0 && f.value.type === 'ipscan') f.value.floor = qf
  await loadProbes()
  if (needProbe.value) {
    const ids = probes.value.map((p) => p.id)
    if (!ids.includes(f.value.probe)) f.value.probe = ids[0] || ''
  }
  if (f.value.type === 'ipscan') loadFloors()
  loadTargets()
  load()
})
onUnmounted(() => {
  clearTimeout(viewTimer)
  cancelAnimationFrame(rafId)
  window.removeEventListener('mousemove', onScrubMove)
  window.removeEventListener('mouseup', onScrubUp)
  if (trendChart) { trendChart.destroy(); trendChart = null }
})
</script>
