import { request, type Operation } from './api'
export interface Binding { id: string; kind: 'panel'|'project'|'site'; resource_id: string; domain: string; upstream: string; directory: string; spa: boolean; service: string; container_port: number; auto_certificate: boolean; certificate_status: string; certificate_error: string; expires_at: string; certificate_account_id?: string }
export interface Access { panel_port: number; panel_domain: string; https: boolean }
export interface BindingResult { binding: Binding; operation: Operation|null; access_url: string }
export const domains = {
 list: () => request<Binding[]>('/bindings'),
 save: (binding: Partial<Binding> & { acme_account_id?: string }) => request<BindingResult>('/bindings', { method:'POST', body:JSON.stringify(binding) }),
 remove: (id:string) => request<{ok:boolean}>(`/bindings/${id}`, {method:'DELETE'}),
 certificate: (id:string, accountId:string) => request<Operation>(`/bindings/${id}/certificate`, { method:'POST', body:JSON.stringify({ acme_account_id: accountId }) }),
 access: () => request<Access>('/system/access'),
 operation: (id:string) => request<Operation>(`/operations/${id}`),
}
