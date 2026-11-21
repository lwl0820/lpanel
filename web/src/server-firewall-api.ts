import { request } from './api'

export interface FirewallRule {
  id: string
  description: string
  port?: number
  protocol?: string
  action?: string
  source?: string
  removable: boolean
}

export interface FirewallStatus {
  available: boolean
  enabled: boolean
  panel_port: number
  message?: string
  rules: FirewallRule[]
}

export interface FirewallRuleInput {
  port: number
  protocol: 'tcp' | 'udp'
  action: 'allow' | 'deny'
}

const prefix = '/server/firewall'
type WriteResult = { ok: true }

export const serverFirewall = {
  status: () => request<FirewallStatus>(prefix),
  setEnabled: (enabled: boolean) => request<WriteResult>(`${prefix}/${enabled ? 'enable' : 'disable'}`, { method: 'POST' }),
  addRule: (rule: FirewallRuleInput) => request<WriteResult>(`${prefix}/rules`, { method: 'POST', body: JSON.stringify(rule) }),
  removeRule: (id: string) => request<WriteResult>(`${prefix}/rules?id=${encodeURIComponent(id)}`, { method: 'DELETE' }),
}
