import { afterEach, expect, test, vi } from 'vitest'
import { APIError, auth, request } from './api'
afterEach(() => vi.unstubAllGlobals())
test('登录通过同源会话发送账号密码', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ username: '管理员' }), { status: 200 }))
  vi.stubGlobal('fetch', fetch)
  expect(await auth.login('管理员', 'password')).toEqual({ username: '管理员' })
  expect(fetch).toHaveBeenCalledWith('/api/v1/auth/login', expect.objectContaining({ credentials: 'same-origin', method: 'POST', body: JSON.stringify({ username: '管理员', password: 'password' }) }))
})
test('启用专用登录地址时通过该地址发送账号密码', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ username: '管理员' }), { status: 200 }))
  vi.stubGlobal('fetch', fetch)
  await auth.login('管理员', 'password', '/private/login')
  expect(fetch).toHaveBeenCalledWith('/private/login', expect.objectContaining({ credentials: 'same-origin', method: 'POST', body: JSON.stringify({ username: '管理员', password: 'password' }) }))
})
test('错误响应保留状态、错误码和中文信息', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { code: 'unauthorized', message: '请先登录' } }), { status: 401 })))
  await expect(request('/auth/me')).rejects.toEqual(new APIError(401, 'unauthorized', '请先登录'))
})
test('Nginx HTML 错误页保留 HTTP 状态并显示中文', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html>502 Bad Gateway</html>', { status: 502 })))
  await expect(request('/auth/me')).rejects.toEqual(new APIError(502, 'invalid_response', '服务器返回异常（HTTP 502），请稍后重试'))
})
test('网络中断转换为中文错误', async () => {
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
  await expect(request('/auth/me')).rejects.toEqual(new APIError(0, 'network_error', '无法连接服务器，请检查网络后重试'))
})
