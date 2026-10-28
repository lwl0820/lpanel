import { request, type Operation } from './api'
export interface Binding { id: string; kind: 'panel'|'project'|'site'; resource_id: string; domain: string; upstream: string; directory: string; spa: boolean; service: string; container_port: number; auto_certificate: boolean; certificate_status: string; certificate_error: string; expires_at: string }
export interface Access { panel_port: number; panel_domain: string; https: boolean; acme_email: string; acme_server: string; agree_tos: boolean }
export interface BindingResult { binding: Binding; operation: Operation|null; access_url: string }
export const domains = {
 list: () => request<Binding[]>('/bindings'),
 save: (binding: Partial<Binding>) => request<BindingResult>('/bindings', { method:'POST', body:JSON.stringify(binding) }),
 remove: (id:string) => request<{ok:boolean}>(`/bindings/${id}`, {method:'DELETE'}),
 certificate: (id:string) => request<Operation>(`/bindings/${id}/certificate`,{method:'POST'}),
 access: () => request<Access>('/system/access'),
 settings: (value: Pick<Access,'acme_email'|'acme_server'|'agree_tos'>) => request<Access>('/system/access',{method:'PUT',body:JSON.stringify(value)}),
 operation: (id:string) => request<Operation>(`/operations/${id}`),
}
