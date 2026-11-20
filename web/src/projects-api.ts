import { request, type Operation } from './api'
export interface Project { id: string; name: string; compose?: string; environment?: string; updated_at: string }
export interface ProjectInput { id?: string; name: string; compose: string; environment: string }
export interface ProjectPort { service: string; container_port: number; published: string; host_ip: string; protocol: string; source: 'ports' | 'expose' }
export interface ProjectContainer { Name: string; Service: string; State: string; Health: string; ExitCode: number; Publishers: { URL: string; TargetPort: number; PublishedPort: number; Protocol: string }[] | null }
export const projects = {
 list: () => request<Project[]>('/projects'),
 get: (id: string) => request<Project>(`/projects/${id}`),
 save: (id: string, input: ProjectInput) => request<Project>(`/projects${id ? `/${id}` : ''}`, { method: id ? 'PUT' : 'POST', body: JSON.stringify(input) }),
 scan: (input: ProjectInput) => request<ProjectPort[]>('/projects/scan', { method: 'POST', body: JSON.stringify(input) }),
 action: (id: string, action: string) => request<Operation>(`/projects/${id}/actions/${action}`, { method: 'POST' }),
 status: (id: string) => request<ProjectContainer[]>(`/projects/${id}/status`),
 logs: (id: string) => request<{ log: string }>(`/projects/${id}/logs`),
 bind: (id: string, input: { domain: string; service: string; container_port: number; scheme: string; auto_certificate: boolean; acme_account_id?: string }) => request<{ operation: Operation | null }>(`/projects/${id}/bindings`, { method: 'POST', body: JSON.stringify(input) }),
 operation: (id: string) => request<Operation>(`/operations/${id}`),
}
