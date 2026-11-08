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
// 状态五色：ok=全部成功(深绿) / partial=存在失败已恢复(橙) / degraded=降级(黄，丢包超阈值) /
// fail=全部失败(红) / nodata=暂停探测(灰)
// 语义（用户定义 2026-11-07）：一次探测只有全部目标都失败才算失败，有一个目标成功即联通。
// 降级口径与 P2 告警一致：按目标丢包率的下中位数超阈值（lossThresh%，后端随接口下发
// loss_thresh，与告警 loss_pct_thresh 同源）——单目标持续全断不标黄也不告警。

// 探针引擎逐分钟汇总 {t,n,f,m}（n=样本数 f=失败数 m=该分钟按告警滚动窗口算的
// 各目标丢包率下中位数，后端 minuteStats 产出）→ 稀疏格子；n=0 不产出（组件补灰格）
export function minuteCells(mins, lossThresh = 10) {
  const out = []
  for (const m of mins || []) {
    if (!m.n) continue
    let s
    if (m.f >= m.n) s = 'fail'
    else if ((m.m || 0) > lossThresh) s = 'degraded'
    else if (m.f > 0) s = 'partial'
    else s = 'ok'
    out.push({ t: m.t, s })
  }
  return out
}

// VM avg_over_time(nw_ping_ok[300s]) step=60 序列（每目标一条，值=该点滚动窗口成功率）→ 稀疏格子：
// 逐点对目标丢包率（1-v）取下中位数，超阈值 → degraded（与告警同口径）；
// 全部≈1 → ok；全部≈0 → fail；其余（有失败但未超阈值）→ partial。分钟内无样本不产出。
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
    if (vals.every(v => v <= 0.001)) s = 'fail'
    else {
      const losses = vals.map(v => 1 - v).sort((a, b) => a - b)
      const median = losses[(losses.length - 1) >> 1] // 下中位数，与告警 lossStats 同规则
      if (median > lossThresh / 100) s = 'degraded'
      else if (vals.every(v => v >= 0.999)) s = 'ok'
      else s = 'partial'
    }
    out.push({ t: ts, s })
  }
  return out
}
