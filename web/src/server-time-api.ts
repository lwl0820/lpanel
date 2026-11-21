import { request, type Operation } from './api'

export interface ServerTimeStatus {
  server_time: string
  timezone: string
  auto_sync: boolean | null
  synchronized: boolean | null
  can_set_auto: boolean
  can_sync_now: boolean
  message?: string
}

const prefix = '/server/time'

export const serverTime = {
  status: (signal: AbortSignal) => request<ServerTimeStatus>(prefix, { signal, cache: 'no-store' }),
  zones: (signal: AbortSignal) => request<{ zones: string[] }>(`${prefix}/zones`, { signal }),
  setTimezone: (timezone: string, signal: AbortSignal) => request<ServerTimeStatus>(`${prefix}/timezone`, {
    method: 'PUT', body: JSON.stringify({ timezone }), signal,
  }),
  setAuto: (enabled: boolean, signal: AbortSignal) => request<ServerTimeStatus>(`${prefix}/auto`, {
    method: 'PUT', body: JSON.stringify({ enabled }), signal,
  }),
  sync: (signal: AbortSignal) => request<Operation>(`${prefix}/sync`, { method: 'POST', signal }),
  operation: (id: string, signal: AbortSignal) => request<Operation>(`/operations/${id}`, { signal }),
}
