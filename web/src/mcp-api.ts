import { request } from './api'

export interface MCPToken { id: string; name: string }
export interface MCPSettings { enabled: boolean; tokens: MCPToken[]; path: string }

export const mcp = {
  settings: () => request<MCPSettings>('/mcp/settings'),
  setEnabled: (enabled: boolean) => request<MCPSettings>('/mcp/settings', {
    method: 'PUT', body: JSON.stringify({ enabled }),
  }),
  createToken: (name: string) => request<{ token: string }>('/mcp/tokens', {
    method: 'POST', body: JSON.stringify({ name }),
  }),
  revokeToken: (id: string) => request<{ ok: boolean }>(`/mcp/tokens/${encodeURIComponent(id)}`, { method: 'DELETE' }),
}
