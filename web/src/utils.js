// 前端公共小工具：多行文本 / CSV 数组互转、时间格式化（设置各 Tab 复用）
import dayjs from 'dayjs'

// ['a','b'] → 'a\nb'（textarea 展示用）
export const lines = (s) => (s || []).join('\n')
// 'a\nb' → ['a','b']（去掉空行与首尾空白）
export const parseLines = (s) => s.split('\n').map(x => x.trim()).filter(Boolean)
// ['a','b'] → 'a, b'（收件人等 CSV 展示用）
export const toCSV = (a) => (a || []).join(', ')
// 'a，b' → ['a','b']（中英文逗号均支持）
export const parseCSV = (s) => s.split(/[,，]/).map(x => x.trim()).filter(Boolean)
// 时间格式化（各页统一 MM-DD HH:mm:ss）
export const fmtTime = (t) => dayjs(t).format('MM-DD HH:mm:ss')

// 返回按钮统一逻辑：优先 router.back() 回到用户刚离开的页面。
// vue-router 4 在 history.state.back 记录上一条路由（新标签/直达进入时为 null），
// 此时退到 fallback 固定页，避免把用户带出应用（history.back() 会离开站点）。
export const goBack = (router, fallback) => {
  if (window.history.state && window.history.state.back) router.back()
  else router.replace(fallback)
}

// ===== 方格心跳图（全站统一规则：1 格=1 分钟，正方形，滑动窗口） =====
// 状态五色：ok=无丢包(深绿) / partial=有丢包未超阈值(橙) /
// degraded=降级(紫，该分钟两个目标丢包率下中位数超阈值) / fail=全部失败(红) / nodata=暂停探测(灰)
// 方案C：探针一轮 5 包；后端 minuteStats 产出每分钟 L（轮丢包率加权）与
// P（该分钟各目标丢包率下中位数，每目标 ≥5 轮）。橙/紫均按单分钟判定，不做跨分钟滚动。
// 紫格 = P > lossThresh；橙 = 未紫且 L > 0；lossThresh 由接口下发，与告警阈值同源。

// 探针引擎逐分钟汇总 {t,n,f,l,p}（n=轮次 f=全丢轮次 l=该分钟丢包率 p=单分钟下中位数）
// → 稀疏格子；n=0 不产出（组件补灰格）
export function minuteCells(mins, lossThresh = 10) {
  const out = []
  const now = Date.now() / 1000
  for (const m of mins || []) {
    if (!m.n) continue
    let s
    // 红只用于“已结束且至少 3 轮全部失败”的分钟；当前分钟还在采样，
    // 一轮全丢就画红会随后续成功样本变色，先按橙/紫展示，真实中断由状态兜底。
    const complete = m.t + 60 <= now
    if (complete && m.f >= m.n && m.n >= 3) s = 'fail'
    else if ((m.p || 0) > lossThresh) s = 'degraded'
    else if ((m.l || 0) > 0) s = 'partial'
    else s = 'ok'
    out.push({ t: m.t, s, loss: m.l || 0, p: m.p || 0 })
  }
  return out
}

// 中心内置监控（无轮丢包率数据，仅有 VM 成功率）仍按成功率判定：
// 逐点对目标丢包率（1-v）取下中位数，超阈值 → degraded；全部≈1 → ok；
// 全部≈0 → fail；其余 → partial。分钟内无样本不产出。
// 时间轴取各序列时间戳的并集（不同目标探测周期/断档会造成网格不一致，只按首条遍历会丢分钟）
export function vmMinuteCells(series, lossThresh = 10) {
  if (!series || !series.length) return []
  const maps = series.map(s => new Map(s.values.map(v => [v[0], v[1] == null ? NaN : Number(v[1])])))
  const tsSet = new Set()
  for (const s of series) for (const [ts] of s.values) tsSet.add(ts)
  const out = []
  for (const ts of [...tsSet].sort((a, b) => a - b)) {
    const vals = []
    for (const m of maps) {
      const v = m.get(ts)
      if (v !== undefined && !isNaN(v)) vals.push(v)
    }
    if (!vals.length) continue
    let s
    let p = 0
    if (vals.every(v => v <= 0.001)) s = 'fail'
    else {
      const losses = vals.map(v => 1 - v).sort((a, b) => a - b)
      const median = losses[(losses.length - 1) >> 1] // 下中位数，与告警 lossStats 同规则
      p = median * 100
      if (median > lossThresh / 100) s = 'degraded'
      else if (vals.every(v => v >= 0.999)) s = 'ok'
      else s = 'partial'
    }
    out.push({ t: ts, s, p })
  }
  return out
}

// applyStatusToCurrent 活动 P1/P2 期间当前分钟不得显示绿色：状态（告警）与心跳来自同一份
// 分钟统计，但仍可能因分钟桶/滚动窗口的相位差在阈值边界出现绿色；这里按状态兜底。
// 只改"当前分钟"且只把绿/橙提升为红/紫/灰，不动历史分钟，也不覆盖已更严重的颜色。
export function applyStatusToCurrent(cells, status, nowMs = Date.now()) {
  if (!cells || !cells.length) return cells
  const down = status === 'down' || status === 'floor_down'
  const deg = status === 'degraded'
  const blind = status === 'blind'
  if (!down && !deg && !blind) return cells
  const nowMin = Math.floor(nowMs / 60000) * 60
  const i = cells.findIndex(c => c.t === nowMin)
  if (i < 0) return cells
  const s = cells[i].s
  if (s !== 'ok' && s !== 'partial') return cells
  const out = cells.slice()
  out[i] = { ...out[i], s: down ? 'fail' : deg ? 'degraded' : 'nodata' }
  return out
}
