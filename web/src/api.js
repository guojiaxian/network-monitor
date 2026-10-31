// 统一 API 封装：会话 Cookie 认证；401 时跳转登录页
async function req(method, url, body) {
  const opt = { method, headers: {}, credentials: 'same-origin' }
  if (body !== undefined) {
    opt.headers['Content-Type'] = 'application/json'
    opt.body = JSON.stringify(body)
  }
  const r = await fetch(url, opt)
  if (r.status === 401 && !url.startsWith('/api/admin/login') && !url.startsWith('/api/admin/bootstrap')) {
    window.location.hash = '#/login'
    throw new Error('请先登录')
  }
  const data = await r.json().catch(() => ({}))
  if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`)
  return data
}

export const api = {
  get: (u) => req('GET', u),
  post: (u, b) => req('POST', u, b),
  put: (u, b) => req('PUT', u, b),
  del: (u) => req('DELETE', u),
}

export async function bootstrap() {
  return api.get('/api/admin/bootstrap')
}
