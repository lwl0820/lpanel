import { request } from './api'

export interface ServerDNSStatus {
  servers: string[]
  can_edit: boolean
  can_restore: boolean
  max_servers?: number
  message?: string
}

const path = '/server/dns'

export const serverDNS = {
  status: (signal: AbortSignal) => request<ServerDNSStatus>(path, { signal, cache: 'no-store' }),
  save: (servers: string[], signal: AbortSignal) => request<ServerDNSStatus>(path, {
    method: 'PUT', body: JSON.stringify({ servers }), signal,
  }),
  restore: (signal: AbortSignal) => request<ServerDNSStatus>(`${path}/restore`, { method: 'POST', signal }),
}
