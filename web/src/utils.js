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
