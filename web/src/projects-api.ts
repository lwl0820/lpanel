import { request, type Operation } from './api'
export interface Project { id: string; name: string; compose?: string; environment?: string; updated_at: string; directory?: string }
export interface ProjectInput { id?: string; name: string; compose: string; environment: string }
export interface ProjectPort { service: string; container_port: number; published: string; host_ip: string; protocol: string; source: 'ports' | 'expose' }
export interface ProjectContainer { ID: string; Name: string; Service: string; State: string; Health: string; ExitCode: number; Publishers: { URL: string; TargetPort: number; PublishedPort: number; Protocol: string }[] | null }
type ProjectSaveResult = Project & { operation?: Operation; deployment_error?: string }
export const projects = {
 list: () => request<Project[]>('/projects'),
 get: (id: string) => request<Project>(`/projects/${id}`),
 create: (input: ProjectInput) => request<ProjectSaveResult>('/projects', { method: 'POST', body: JSON.stringify(input) }),
 update: (id: string, input: ProjectInput) => request<ProjectSaveResult>(`/projects/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
 scan: (input: ProjectInput) => request<ProjectPort[]>('/projects/scan', { method: 'POST', body: JSON.stringify(input) }),
 action: (id: string, action: string, removeFiles = false) => request<Operation>(`/projects/${id}/actions/${action}${removeFiles ? '?remove_files=true' : ''}`, { method: 'POST' }),
 status: (id: string) => request<ProjectContainer[]>(`/projects/${id}/status`),
 logs: (id: string) => request<{ log: string }>(`/projects/${id}/logs`),
 containerLogs: (id: string, containerId: string) => request<{ log: string }>(`/projects/${id}/containers/${encodeURIComponent(containerId)}/logs`),
 bind: (id: string, input: { domain: string; service: string; container_port: number; scheme: string; auto_certificate: boolean; acme_account_id?: string }) => request<{ operation: Operation | null }>(`/projects/${id}/bindings`, { method: 'POST', body: JSON.stringify(input) }),
 operation: (id: string) => request<Operation>(`/operations/${id}`),
}
