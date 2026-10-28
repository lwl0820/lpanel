export interface User { username: string }
export interface Operation { id: string; kind: string; status: 'queued' | 'running' | 'success' | 'failed'; log: string; error: string; created_at: string; updated_at: string }
export class APIError extends Error {
  constructor(public status: number, public code: string, message: string) { super(message) }
}
export async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api/v1${path}`, { ...options, credentials: 'same-origin', headers: { 'Content-Type': 'application/json', ...options.headers } }).catch(() => {
    throw new APIError(0, 'network_error', '无法连接服务器，请检查网络后重试')
  })
  const body = await response.json().catch(() => {
    throw new APIError(response.status, 'invalid_response', `服务器返回异常（HTTP ${response.status}），请稍后重试`)
  })
  if (!response.ok) throw new APIError(response.status, body.error?.code ?? 'request_failed', body.error?.message ?? '请求失败，请重试')
  return body as T
}
export const auth = {
  me: () => request<User>('/auth/me'),
  login: (username: string, password: string) => request<User>('/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }),
  logout: () => request<{ ok: boolean }>('/auth/logout', { method: 'POST' }),
}
