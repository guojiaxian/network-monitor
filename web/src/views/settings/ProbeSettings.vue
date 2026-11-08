<template>
  <div>
    <div class="d-flex align-items-center mb-3">
      <div>
        <h4 class="mb-0">探测配置</h4>
        <div class="text-secondary small mt-1">全部探测/监测的周期、间隔、超时在此集中配置；探针侧约 60 秒下发生效，中心侧约 30 秒生效</div>
      </div>
    </div>

    <!-- 分区页签：下划线标题式（与探测历史一致）；sec 同步到路由 query 可深链 -->
    <div class="nw-tabs mb-2">
      <button v-for="s in SECS" :key="s.v" type="button" :class="{ active: sec === s.v }" @click="switchSec(s.v)">{{ s.label }}</button>
    </div>
    <div class="text-secondary small mb-3">{{ activeNote }}</div>

    <!-- ═══════════ 分区一：楼层探针 ═══════════ -->
    <div v-show="sec === 'floor'">
    <div class="nw-card mb-3">
      <h6>探测周期 · 超时 · 重试 · 上报</h6>
      <div class="row g-2">
        <div class="col-md-2"><label class="form-label small mb-1">Ping 间隔(秒)</label><input v-model.number="ac.interval_ping_s" type="number" min="5" max="3600" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">TCP 间隔(秒)</label><input v-model.number="ac.interval_tcp_s" type="number" min="5" max="3600" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">DNS 间隔(秒)</label><input v-model.number="ac.interval_dns_s" type="number" min="5" max="3600" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">HTTP 间隔(秒)</label><input v-model.number="ac.interval_http_s" type="number" min="5" max="3600" class="form-control form-control-sm" /></div>
        <div class="col-md-2"><label class="form-label small mb-1">探测超时(秒)</label><input v-model.number="ac.timeout_s" type="number" min="1" max="10" class="form-control form-control-sm" /><div class="text-help">四类探测共用，1 ~ 10，默认 3</div></div>
        <div class="col-md-2"><label class="form-label small mb-1">失败重试次数</label><input v-model.number="ac.retry_n" type="number" min="1" max="3" class="form-control form-control-sm" /><div class="text-help">1 ~ 3，默认 1</div></div>
        <div class="col-md-2"><label class="form-label small mb-1">重试间隔(毫秒)</label><input v-model.number="ac.retry_pause_ms" type="number" min="100" max="2000" class="form-control form-control-sm" /><div class="text-help">100 ~ 2000，默认 300</div></div>
        <div class="col-md-2"><label class="form-label small mb-1">心跳间隔(秒)</label><input v-model.number="ac.heartbeat_sec" type="number" min="5" max="60" class="form-control form-control-sm" /><div class="text-help">5 ~ 60，默认 15</div></div>
        <div class="col-md-2"><label class="form-label small mb-1">出口识别间隔(分)</label><input v-model.number="ac.egress_min" type="number" min="1" max="60" class="form-control form-control-sm" /><div class="text-help">1 ~ 60，默认 5</div></div>
      </div>
      <div class="row g-2 mt-0">
        <div class="col-12 text-end">
          <span class="text-secondary small me-2">任一失败重试后仍失败才判断连；心跳驱动盲区判定与资源告警；改完点「保存并下发」</span>
          <button class="btn btn-sm btn-primary" @click="saveAC">保存并下发</button>
        </div>
      </div>
      <div v-if="acMsg" class="small mt-2" :class="acOk ? 'text-success' : 'text-danger'">{{ acMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>探测目标与接入</h6>
      <div class="row g-3 mt-0">
        <div class="col-md-6">
          <label class="form-label small mb-1">ICMP 目标（IP）</label>
          <target-list :items="ac.icmp_targets" add-title="新增 ICMP 目标" add-placeholder="IP，如 223.5.5.5" @add="openTarget" @edit="editTarget" @remove="removeItem" />
        </div>
        <div class="col-md-6">
          <label class="form-label small mb-1">TCP 目标（host:port）</label>
          <target-list :items="ac.tcp_targets" add-title="新增 TCP 目标" add-placeholder="host:port，如 www.qq.com:443" @add="openTarget" @edit="editTarget" @remove="removeItem" />
        </div>
        <div class="col-md-4">
          <label class="form-label small mb-1">DNS 服务器</label>
          <target-list :items="ac.dns_servers" add-title="新增 DNS 服务器" add-placeholder="IP，如 223.5.5.5" @add="openTarget" @edit="editTarget" @remove="removeItem" />
        </div>
        <div class="col-md-4"><label class="form-label small mb-1">解析测试域名</label><input v-model="ac.dns_domain" class="form-control form-control-sm" /></div>
        <div class="col-md-4">
          <label class="form-label small mb-1">HTTP 目标（URL）</label>
          <target-list :items="ac.http_targets" add-title="新增 HTTP 目标" add-placeholder="URL，如 https://www.baidu.com" @add="openTarget" @edit="editTarget" @remove="removeItem" />
        </div>
        <div class="col-md-4"><label class="form-label small mb-1">注册令牌（探针接入用）</label><input v-model="enrollToken" class="form-control form-control-sm font-monospace" /></div>
        <div class="col-md-8 offset-md-4 text-end align-self-end">
          <span class="text-secondary small me-2">多目标投票防单目标误判；改完点「保存并下发」</span>
          <button class="btn btn-sm btn-primary" @click="saveAC">保存并下发</button>
        </div>
      </div>
      <div v-if="acMsg2" class="small mt-2" :class="acOk2 ? 'text-success' : 'text-danger'">{{ acMsg2 }}</div>
    </div>

    </div><!-- /分区一 楼层探针 -->

    <!-- ═══════════ 分区二：中心监测 ═══════════ -->
    <div v-show="sec === 'center'">
    <div class="nw-card mb-3">
      <h6>内网服务监控（内→内 · Uptime Kuma 模式）</h6>
      <div class="d-flex align-items-center mb-2">
        <p class="text-secondary small mb-0">中心服务器自己作为探针监控内网服务（OA、业务系统、交换机等）；连续 3 次失败触发 P2，恢复自动解除。</p>
        <div class="form-check ms-auto me-3">
          <input id="im1" v-model="im.enabled" type="checkbox" class="form-check-input" />
          <label for="im1" class="form-check-label">启用</label>
        </div>
        <button class="btn btn-sm btn-outline-primary me-2" @click="openIM()">＋ 新增</button>
        <button class="btn btn-sm btn-primary" @click="saveIM">保存</button>
      </div>
      <table class="table nw-table table-sm align-middle" v-if="(im.items || []).length">
        <thead><tr><th>名称</th><th style="width:80px">类型</th><th>目标</th><th style="width:80px">间隔(秒)</th><th style="width:80px">超时(秒)</th><th style="width:60px"></th></tr></thead>
        <tbody>
          <tr v-for="(it, i) in im.items" :key="i">
            <td>{{ it.name }}</td><td><span class="badge bg-secondary">{{ it.type }}</span></td>
            <td><code>{{ it.target }}</code></td><td>{{ it.interval_s || 30 }}</td><td>{{ it.timeout_s || 5 }}</td>
            <td><a href="javascript:void(0)" class="text-primary text-decoration-none me-2" @click="openIM(it, i)">编辑</a><a href="javascript:void(0)" class="text-danger text-decoration-none" @click="im.items.splice(i, 1)">删除</a></td>
          </tr>
        </tbody>
      </table>
      <p v-else class="text-secondary small mb-2">暂无监控目标。</p>
      <div v-if="imMsg" class="small mt-2" :class="imOk ? 'text-success' : 'text-danger'">{{ imMsg }}</div>
    </div>

    <div class="nw-card mb-2">
      <h6>行业网监测（中心 → 行业网 · 仅 ICMP）</h6>
      <div class="d-flex align-items-center mb-2">
        <p class="text-secondary small mb-0">对行业网指定 IP <b>仅做 ICMP ping</b>（不做 TCP/HTTP 以免被封）；连续 3 次失败触发 P2，恢复自动解除。</p>
        <div class="form-check ms-auto me-3">
          <input id="ind1" v-model="ind.enabled" type="checkbox" class="form-check-input" />
          <label for="ind1" class="form-check-label">启用</label>
        </div>
        <button class="btn btn-sm btn-outline-primary me-2" @click="openInd()">＋ 新增</button>
        <button class="btn btn-sm btn-primary" @click="saveInd">保存</button>
      </div>
      <table class="table nw-table table-sm align-middle" v-if="(ind.items || []).length">
        <thead><tr><th>名称</th><th>监测 IP</th><th style="width:80px">间隔(秒)</th><th style="width:80px">超时(秒)</th><th style="width:60px"></th></tr></thead>
        <tbody>
          <tr v-for="(it, i) in ind.items" :key="i">
            <td>{{ it.name }}</td><td><code>{{ it.target }}</code></td><td>{{ it.interval_s || 30 }}</td><td>{{ it.timeout_s || 5 }}</td>
            <td><a href="javascript:void(0)" class="text-primary text-decoration-none me-2" @click="openInd(it, i)">编辑</a><a href="javascript:void(0)" class="text-danger text-decoration-none" @click="ind.items.splice(i, 1)">删除</a></td>
          </tr>
        </tbody>
      </table>
      <p v-else class="text-secondary small mb-2">暂无监测目标。</p>
      <div v-if="indMsg" class="small mt-2" :class="indOk ? 'text-success' : 'text-danger'">{{ indMsg }}</div>
    </div>

    <div class="nw-card mb-3">
      <h6>中心引擎参数（告警判定节奏）</h6>
      <div class="row g-2 align-items-end">
        <div class="col-md-3"><label class="form-label small mb-1">告警评估周期(秒)</label><input v-model.number="als.eval_interval_s" type="number" min="2" max="60" class="form-control form-control-sm" /><div class="text-help">2 ~ 60，默认 5；每轮评估全部探测数据</div></div>
        <div class="col-md-3"><label class="form-label small mb-1">探测异常判定窗口(秒)</label><input v-model.number="als.type_fail_window_s" type="number" min="30" max="600" class="form-control form-control-sm" /><div class="text-help">30 ~ 600，默认 60；某类探测全部目标连续失败达此时长才告警</div></div>
        <div class="col-md-6 text-end">
          <span class="text-secondary small me-2">保存后下一轮评估即按新值执行</span>
          <button class="btn btn-sm btn-primary" @click="saveEngine">保存引擎参数</button>
        </div>
      </div>
      <div v-if="engMsg" class="small mt-2" :class="engOk ? 'text-success' : 'text-danger'">{{ engMsg }}</div>
    </div>

    </div><!-- /分区二 中心监测 -->

    <!-- ═══════════ 分区三：定时任务 ═══════════ -->
    <div v-show="sec === 'sched'">
    <!-- 定时测速：每小时各探针并行连测取平均，低速复测确认后告警 -->
    <div class="nw-card mb-3">
      <h6>定时测速（各探针并行 · 多连接 CDN 测速）</h6>
      <div class="d-flex align-items-center mb-2">
        <p class="text-secondary small mb-0">
          到期后全部绑定探针<b>并行</b>开测（各走各自出口线路），每轮连测 N 次取平均；均值低于阈值时 <b>1 分钟后自动复测</b>（固定），仍低 → P2「网速慢」。
          「立即探测」按钮（探针状态/线路状态/楼层状态/探针配置页）也会排程测速。默认引擎为<b>多连接 CDN 测速</b>（16 连接限时采样，与测速网站同原理；Ookla 大陆节点已退网仅境外备用）。
        </p>
        <div class="form-check ms-auto me-3 flex-shrink-0">
          <input id="sp1" v-model="sp.enabled" type="checkbox" class="form-check-input" />
          <label for="sp1" class="form-check-label">启用</label>
        </div>
        <button class="btn btn-sm btn-primary flex-shrink-0" @click="saveSP">保存测速计划</button>
      </div>
      <div class="row g-2">
        <div class="col-md-2">
          <label class="form-label small mb-1">测速引擎</label>
          <select v-model="sp.engine" class="form-select form-select-sm">
            <option value="http">多连接 CDN（推荐）</option>
            <option value="ookla">Ookla（境外备用）</option>
          </select>
        </div>
        <div class="col-md-2"><label class="form-label small mb-1">间隔(分钟)</label><input v-model.number="sp.interval_min" type="number" class="form-control form-control-sm" /><div class="text-help">默认 60</div></div>
        <div class="col-md-2"><label class="form-label small mb-1">每轮连测次数</label><input v-model.number="sp.runs" type="number" min="1" max="5" class="form-control form-control-sm" /><div class="text-help">1 ~ 5，默认 3</div></div>
        <div class="col-md-2"><label class="form-label small mb-1">网速慢阈值(Mbps)</label><input v-model.number="sp.slow_min_mbps" type="number" class="form-control form-control-sm" /><div class="text-help">默认 10</div></div>
        <div class="col-md-2"><label class="form-label small mb-1">并行数上限</label><input v-model.number="sp.max_concurrent" type="number" class="form-control form-control-sm" /><div class="text-help">同时在途任务数，默认 12</div></div>
        <div class="col-md-2"><label class="form-label small mb-1">单轮超时(分钟)</label><input v-model.number="sp.timeout_min" type="number" class="form-control form-control-sm" /><div class="text-help">默认 10</div></div>
        <div class="col-12" v-if="sp.engine === 'http'">
          <label class="form-label small mb-1">CDN 测速源（每行一个 URL；连接按行分摊并循环拉取，文件下完自动重发；404 源会自动退化，请定期复核有效性）</label>
          <textarea v-model="spUrlStr" class="form-control form-control-sm font-monospace" rows="2"></textarea>
        </div>
      </div>
      <div v-if="spMsg" class="small mt-2" :class="spOk ? 'text-success' : 'text-danger'">{{ spMsg }}</div>
    </div>

    <!-- IP 扫描计划：中心定时逐网段 ping，结果用于线路"在线 IP 设备数" -->
    <div class="nw-card mb-3">
      <h6>IP 扫描计划（中心 → 楼层网段 · 逐 IP ping）</h6>
      <div class="d-flex align-items-center mb-2">
        <p class="text-secondary small mb-0">
          中心按设定间隔对楼层子网<b>逐网段</b>扫描，每次按网段各留一条记录；「线路状态 / 详情」的<b>在线 IP 设备数</b>即取最近一次扫描结果。
          手动扫描（监控展示 → IP 扫描）与本计划共用同一执行队列，不会并发叠加。
        </p>
        <div class="form-check ms-auto me-3 flex-shrink-0">
          <input id="sc1" v-model="scan.enabled" type="checkbox" class="form-check-input" />
          <label for="sc1" class="form-check-label">启用</label>
        </div>
        <button class="btn btn-sm btn-primary flex-shrink-0" @click="saveScan">保存扫描计划</button>
      </div>
      <div class="row g-2">
        <div class="col-md-3"><label class="form-label small mb-1">扫描间隔（分钟）</label><input v-model.number="scan.interval_min" type="number" min="5" max="1440" class="form-control form-control-sm" /><div class="text-help">5 ~ 1440，默认 60</div></div>
        <div class="col-md-3"><label class="form-label small mb-1">单 IP 超时（毫秒）</label><input v-model.number="scan.timeout_ms" type="number" min="200" max="5000" class="form-control form-control-sm" /><div class="text-help">200 ~ 5000，默认 1000</div></div>
      </div>
      <div class="mt-2">
        <label class="form-label small mb-1">
          扫描范围
          <span class="text-secondary">（不选 = 全部已配置 CIDR 的楼层；当前共 {{ scanFloors.length }} 个可用网段）</span>
        </label>
        <div class="d-flex flex-wrap gap-2">
          <button v-for="f in scanFloors" :key="f.id" type="button" class="btn btn-sm"
                  :class="scan.floor_ids.includes(f.id) ? 'btn-primary' : 'btn-outline-secondary'"
                  :title="f.cidr" @click="toggleScanFloor(f.id)">
            {{ f.name }} <span class="small opacity-75">{{ f.cidr }}</span>
          </button>
          <span v-if="!scanFloors.length" class="text-secondary small">暂无已配置 CIDR 的楼层，请先到「楼层配置」填写子网</span>
        </div>
      </div>
      <div class="small mt-2 text-secondary">
        最近一次定时扫描：{{ scanLast || '尚未执行' }}
        <span v-if="scan.enabled"> · 每 {{ scan.interval_min }} 分钟一轮</span>
        <span v-else> · 定时扫描已关闭</span>
      </div>
      <div v-if="scanMsg" class="small mt-2" :class="scanOk ? 'text-success' : 'text-danger'">{{ scanMsg }}</div>
    </div>
    </div><!-- /分区三 定时任务 -->

    <!-- 新增探测目标（单输入，六类目标共用） -->
    <ModalBox :show="tModal.show" :title="tModal.editIdx >= 0 ? '修改目标' : tModal.title" @close="tModal.show = false" @ok="confirmTarget">
      <input v-model="tModal.val" class="form-control form-control-sm font-monospace" :placeholder="tModal.placeholder"
        @keyup.enter="confirmTarget" />
      <div v-if="tModal.err" class="text-danger small mt-2">{{ tModal.err }}</div>
    </ModalBox>

    <!-- 新增内网服务监控目标 -->
    <ModalBox :show="imModal.show" :title="imModal.idx >= 0 ? '编辑内网服务监控目标' : '新增内网服务监控目标'" @close="imModal.show = false" @ok="confirmIM">
      <div class="row g-2">
        <div class="col-md-6"><label class="form-label small mb-1">名称 <span class="text-danger">*</span></label>
          <input v-model="imModal.name" class="form-control form-control-sm" placeholder="如 OA系统" /></div>
        <div class="col-md-6"><label class="form-label small mb-1">类型</label>
          <select v-model="imModal.type" class="form-select form-select-sm">
            <option value="http">http</option><option value="tcp">tcp</option><option value="icmp">icmp</option>
          </select></div>
        <div class="col-md-6"><label class="form-label small mb-1">目标 <span class="text-danger">*</span></label>
          <input v-model="imModal.target" class="form-control form-control-sm font-monospace" placeholder="http: URL / tcp: host:port / icmp: IP" /></div>
        <div class="col-md-3"><label class="form-label small mb-1">间隔(秒)</label>
          <input v-model.number="imModal.interval_s" type="number" class="form-control form-control-sm" placeholder="30" /><div class="text-help">≥10，默认 30</div></div>
        <div class="col-md-3"><label class="form-label small mb-1">超时(秒)</label>
          <input v-model.number="imModal.timeout_s" type="number" class="form-control form-control-sm" placeholder="5" /><div class="text-help">1 ~ 60，默认 5</div></div>
      </div>
      <div v-if="imModal.err" class="text-danger small mt-2">{{ imModal.err }}</div>
    </ModalBox>

    <!-- 新增行业网监测 IP -->
    <ModalBox :show="indModal.show" :title="indModal.idx >= 0 ? '编辑行业网监测 IP' : '新增行业网监测 IP（仅 ICMP）'" @close="indModal.show = false" @ok="confirmInd">
      <div class="row g-2">
        <div class="col-md-6"><label class="form-label small mb-1">名称 <span class="text-danger">*</span></label>
          <input v-model="indModal.name" class="form-control form-control-sm" placeholder="如 行业网核心交换" /></div>
        <div class="col-md-6"><label class="form-label small mb-1">监测 IP <span class="text-danger">*</span></label>
          <input v-model="indModal.target" class="form-control form-control-sm font-monospace" placeholder="10.5.0.1" /></div>
        <div class="col-md-6"><label class="form-label small mb-1">间隔(秒)</label>
          <input v-model.number="indModal.interval_s" type="number" class="form-control form-control-sm" placeholder="30" /><div class="text-help">≥10，默认 30</div></div>
        <div class="col-md-6"><label class="form-label small mb-1">超时(秒)</label>
          <input v-model.number="indModal.timeout_s" type="number" class="form-control form-control-sm" placeholder="5" /><div class="text-help">1 ~ 60，默认 5</div></div>
      </div>
      <div v-if="indModal.err" class="text-danger small mt-2">{{ indModal.err }}</div>
    </ModalBox>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../../api'
import ModalBox from '../../components/ModalBox.vue'
import TargetList from '../../components/TargetList.vue'
import { fmtTime, lines, parseLines } from '../../utils'

// 分区页签（横向标题式，与探测历史同款）；sec 写回路由 query，刷新/分享链接保持在原分区
const SECS = [
  { v: 'floor', label: '楼层探针', note: '下发至全部楼层探针（agent_config）' },
  { v: 'center', label: '中心监测', note: '中心服务器自身执行（内→内 / 行业网 / 引擎参数）' },
  { v: 'sched', label: '定时任务', note: '按计划自动执行（定时测速 / IP 扫描），保存后约 30 秒生效' },
]
const route = useRoute(), router = useRouter()
const sec = ref(SECS.some(s => s.v === route.query.sec) ? route.query.sec : 'floor')
const activeNote = computed(() => (SECS.find(s => s.v === sec.value) || {}).note || '')
function switchSec(v) {
  sec.value = v
  router.replace({ query: { ...route.query, sec: v } })
}

const ac = ref({ icmp_targets: [], tcp_targets: [], dns_servers: [], http_targets: [], retry_n: 1, retry_pause_ms: 300, heartbeat_sec: 15, egress_min: 5 })
const enrollToken = ref(''), acMsg = ref(''), acOk = ref(false), acMsg2 = ref(''), acOk2 = ref(false)
const im = ref({ enabled: false, items: [] }), imMsg = ref(''), imOk = ref(false)
const ind = ref({ enabled: false, items: [] }), indMsg = ref(''), indOk = ref(false)
// 中心引擎参数（alert_settings 的周期类字段；保存时展开携带其余字段防覆盖）
const als = ref({ eval_interval_s: 5, type_fail_window_s: 60 }), engMsg = ref(''), engOk = ref(false)
// IP 扫描计划：中心定时逐网段扫描（结果用于线路"在线 IP 设备数"）
const scan = ref({ enabled: true, interval_min: 60, timeout_ms: 1000, floor_ids: [] }), scanMsg = ref(''), scanOk = ref(false)
// 定时测速计划（speed_settings；默认与后端 DefaultSpeedSettings 对齐）
const sp = ref({ enabled: true, engine: 'http', interval_min: 60, timeout_min: 10, runs: 3, slow_min_mbps: 10, max_concurrent: 12, http_urls: [] })
const spUrlStr = ref(''), spMsg = ref(''), spOk = ref(false)
const scanFloors = ref([]), scanLast = ref('')

const tModal = reactive({ show: false, title: '', placeholder: '', arr: null, val: '', err: '', editIdx: -1 })
const imModal = reactive({ show: false, name: '', type: 'http', target: '', interval_s: 30, timeout_s: 5, err: '', idx: -1 })
const indModal = reactive({ show: false, name: '', target: '', interval_s: 30, timeout_s: 5, err: '', idx: -1 })

async function load() {
  const s = await api.get('/api/admin/settings')
  ac.value = { ...ac.value, ...(s.agent_config || {}) }
  for (const k of ['icmp_targets', 'tcp_targets', 'dns_servers', 'http_targets'])
    if (!Array.isArray(ac.value[k])) ac.value[k] = []
  enrollToken.value = s['agent.enroll_token'] ? JSON.parse(JSON.stringify(s['agent.enroll_token'])) : ''
  im.value = s.internal_monitors || {}
  if (!Array.isArray(im.value.items)) im.value.items = []
  ind.value = s.industry_monitors || {}
  if (!Array.isArray(ind.value.items)) ind.value.items = []
  als.value = { ...als.value, ...(s.alert_settings || {}) }
  scan.value = { ...scan.value, ...(s.scan_settings || {}) }
  if (!Array.isArray(scan.value.floor_ids)) scan.value.floor_ids = []
  sp.value = { ...sp.value, ...(s.speed_settings || {}) }
  if (!Array.isArray(sp.value.http_urls)) sp.value.http_urls = []
  spUrlStr.value = lines(sp.value.http_urls)
  // 楼层列表（只列配了 CIDR 的可扫描网段）+ 最近一次定时扫描时间
  const [fs, runs] = await Promise.all([
    api.get('/api/admin/floors'),
    api.get('/api/admin/scan/runs?trigger=schedule&limit=1').catch(() => []),
  ])
  scanFloors.value = (fs || []).filter(f => f.cidr)
  scanLast.value = (runs && runs.length) ? fmtTime(runs[0].created_at) : ''
}

// num 数值钳位：非法/越界回落默认（与后端 normalize 同口径）
function num(v, lo, hi, dft) {
  const x = Number(v)
  return Number.isFinite(x) && x >= lo && x <= hi ? x : dft
}

function toggleScanFloor(id) {
  const i = scan.value.floor_ids.indexOf(id)
  if (i >= 0) scan.value.floor_ids.splice(i, 1)
  else scan.value.floor_ids.push(id)
}
async function saveScan() {
  scanMsg.value = ''
  const raw = Number(scan.value.interval_min)
  if (!Number.isFinite(raw) || raw < 5 || raw > 1440) {
    scanOk.value = false; scanMsg.value = '扫描间隔需在 5 ~ 1440 分钟之间'; return
  }
  try {
    await api.put('/api/admin/settings', {
      scan_settings: {
        enabled: !!scan.value.enabled,
        interval_min: raw,
        timeout_ms: num(scan.value.timeout_ms, 200, 5000, 1000),
        floor_ids: scan.value.floor_ids.slice(),
      },
    })
    scanOk.value = true
    scanMsg.value = '已保存，约 30 秒内生效'
  } catch (e) { scanOk.value = false; scanMsg.value = e.message }
}

async function saveSP() {
  spMsg.value = ''; spOk.value = false
  try {
    await api.put('/api/admin/settings', {
      speed_settings: {
        enabled: !!sp.value.enabled,
        engine: sp.value.engine === 'http' ? 'http' : 'ookla',
        interval_min: num(sp.value.interval_min, 5, 1440, 60),
        timeout_min: num(sp.value.timeout_min, 2, 60, 10),
        runs: num(sp.value.runs, 1, 5, 3),
        slow_min_mbps: num(sp.value.slow_min_mbps, 1, 10000, 10),
        max_concurrent: num(sp.value.max_concurrent, 1, 256, 12),
        http_urls: parseLines(spUrlStr.value),
      },
    })
    spOk.value = true
    spMsg.value = '已保存，约 30 秒内生效'
  } catch (e) { spOk.value = false; spMsg.value = e.message }
}

// ---- 中心引擎参数（alert_settings 展开保存，保留阈值/收件组等其余字段） ----
async function saveEngine() {
  engMsg.value = ''; engOk.value = false
  try {
    await api.put('/api/admin/settings', {
      alert_settings: {
        ...JSON.parse(JSON.stringify(als.value)),
        eval_interval_s: num(als.value.eval_interval_s, 2, 60, 5),
        type_fail_window_s: num(als.value.type_fail_window_s, 30, 600, 60),
      },
    })
    engOk.value = true
    engMsg.value = '已保存，下一轮评估即生效'
  } catch (e) { engOk.value = false; engMsg.value = e.message }
}

// ---- 目标弹窗（新增 + 点击 badge 编辑） ----
function openTarget(title, placeholder, arr) {
  Object.assign(tModal, { show: true, title, placeholder, arr, val: '', err: '', editIdx: -1 })
}
function editTarget(arr, i) {
  Object.assign(tModal, { show: true, title: '修改目标', placeholder: '', arr, val: arr[i], err: '', editIdx: i })
}
function confirmTarget() {
  const v = (tModal.val || '').trim()
  if (!v) { tModal.err = '请输入目标'; return }
  if (tModal.arr.includes(v) && tModal.arr[tModal.editIdx] !== v) { tModal.err = '该目标已存在'; return }
  if (tModal.editIdx >= 0) tModal.arr.splice(tModal.editIdx, 1, v)
  else tModal.arr.push(v)
  tModal.show = false
}
function removeItem(arr, i) { arr.splice(i, 1) }

// ---- 内网监控/行业网弹窗 ----
function openIM(it, idx) {
  Object.assign(imModal, it ? { show: true, name: it.name, type: it.type, target: it.target, interval_s: it.interval_s, timeout_s: it.timeout_s || 5, err: '', idx }
                           : { show: true, name: '', type: 'http', target: '', interval_s: 30, timeout_s: 5, err: '', idx: -1 })
}
function confirmIM() {
  imModal.err = ''
  if (!imModal.name.trim()) { imModal.err = '请填写名称'; return }
  if (!imModal.target.trim()) { imModal.err = '请填写目标'; return }
  const row = { name: imModal.name.trim(), type: imModal.type, target: imModal.target.trim(), interval_s: num(imModal.interval_s, 10, 86400, 30), timeout_s: num(imModal.timeout_s, 1, 60, 5) }
  if (imModal.idx >= 0) im.value.items.splice(imModal.idx, 1, row)
  else im.value.items.push(row)
  imModal.show = false
}
function openInd(it, idx) {
  Object.assign(indModal, it ? { show: true, name: it.name, target: it.target, interval_s: it.interval_s, timeout_s: it.timeout_s || 5, err: '', idx }
                             : { show: true, name: '', target: '', interval_s: 30, timeout_s: 5, err: '', idx: -1 })
}
function confirmInd() {
  indModal.err = ''
  if (!indModal.name.trim()) { indModal.err = '请填写名称'; return }
  if (!indModal.target.trim()) { indModal.err = '请填写监测 IP'; return }
  const row = { name: indModal.name.trim(), target: indModal.target.trim(), interval_s: num(indModal.interval_s, 10, 86400, 30), timeout_s: num(indModal.timeout_s, 1, 60, 5) }
  if (indModal.idx >= 0) ind.value.items.splice(indModal.idx, 1, row)
  else ind.value.items.push(row)
  indModal.show = false
}

function bump(v) { v.version = (v.version || 0) + 1; return v }

async function saveAC() {
  acMsg.value = ''; acOk.value = false; acMsg2.value = ''; acOk2.value = false
  const cfg = bump(JSON.parse(JSON.stringify(ac.value)))
  // 周期/超时/重试字段钳位（与后端 putSettings 同口径，防手滑输入 0/1 秒高频探测）
  cfg.interval_ping_s = num(cfg.interval_ping_s, 5, 3600, 10)
  cfg.interval_tcp_s = num(cfg.interval_tcp_s, 5, 3600, 30)
  cfg.interval_dns_s = num(cfg.interval_dns_s, 5, 3600, 60)
  cfg.interval_http_s = num(cfg.interval_http_s, 5, 3600, 60)
  cfg.timeout_s = num(cfg.timeout_s, 1, 10, 3)
  cfg.retry_n = num(cfg.retry_n, 1, 3, 1)
  cfg.retry_pause_ms = num(cfg.retry_pause_ms, 100, 2000, 300)
  cfg.heartbeat_sec = num(cfg.heartbeat_sec, 5, 60, 15)
  cfg.egress_min = num(cfg.egress_min, 1, 60, 5)
  try {
    const body = { agent_config: cfg }
    if (enrollToken.value) body['agent.enroll_token'] = enrollToken.value
    await api.put('/api/admin/settings', body)
    acMsg.value = '已保存，探针将在 60 秒内应用新配置'; acOk.value = true
    acMsg2.value = '已保存，探针将在 60 秒内应用新配置'; acOk2.value = true
  } catch (e) { acMsg.value = e.message; acMsg2.value = e.message }
}

async function saveIM() {
  imMsg.value = ''; imOk.value = false
  try {
    const items = (im.value.items || []).filter(x => x.name && x.target)
    await api.put('/api/admin/settings', { internal_monitors: { enabled: im.value.enabled, items } })
    imMsg.value = `已保存 ${items.length} 个目标，30 秒内生效`; imOk.value = true
  } catch (e) { imMsg.value = e.message }
}

async function saveInd() {
  indMsg.value = ''; indOk.value = false
  try {
    const items = (ind.value.items || []).filter(x => x.name && x.target)
    await api.put('/api/admin/settings', { industry_monitors: { enabled: ind.value.enabled, items } })
    indMsg.value = `已保存 ${items.length} 个目标，30 秒内生效（仅 ICMP ping）`; indOk.value = true
  } catch (e) { indMsg.value = e.message }
}

onMounted(load)
</script>

<style scoped>
/* 分区标题已改为页内横向页签（nw-tabs，全局样式）；此处仅留通用帮助文字 */
/* 数字输入下的范围帮助文字（统一小号灰字） */
.text-help { font-size: .75rem; color: #6c757d; }
</style>
