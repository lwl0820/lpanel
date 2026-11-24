import { request, type Operation } from './api'
export interface Project { id: string; name: string; compose?: string; environment?: string; custom_ports?: number[]; updated_at: string; directory?: string; legacy_directory: boolean }
export interface ProjectInput { id?: string; name: string; compose: string; environment: string; custom_ports?: number[] }
export interface ProjectPort { service: string; container_port: number; published: string; host_ip: string; protocol: string; source: 'ports' | 'expose' }
type ProjectBindingTarget = { host_port: number; service?: never; container_port?: never }
  | (Pick<ProjectPort, 'service' | 'container_port'> & { host_port?: never })
type ProjectBindingInput = ProjectBindingTarget & { domain: string; scheme: string; auto_certificate: boolean; acme_account_id?: string }
export interface ProjectContainer { ID: string; Name: string; Service: string; State: string; Health: string; ExitCode: number; Publishers: { URL: string; TargetPort: number; PublishedPort: number; Protocol: string }[] | null }
export type ContainerAction = 'start' | 'stop'
export type ProjectSaveResult = Project & { operation?: Operation; deployment_error?: string }
export const projects = {
 list: () => request<Project[]>('/projects'),
 get: (id: string) => request<Project>(`/projects/${id}`),
 create: (input: ProjectInput) => request<ProjectSaveResult>('/projects', { method: 'POST', body: JSON.stringify(input) }),
 update: (id: string, input: ProjectInput) => request<ProjectSaveResult>(`/projects/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
 exportURL: (id: string) => `/api/v1/projects/${encodeURIComponent(id)}/export`,
 importArchive: (file: File) => request<ProjectSaveResult>('/projects/import', { method: 'POST', headers: { 'Content-Type': 'application/zip' }, body: file }),
 scan: (input: ProjectInput) => request<ProjectPort[]>('/projects/scan', { method: 'POST', body: JSON.stringify(input) }),
 action: (id: string, action: string, removeFiles = false) => request<Operation>(`/projects/${id}/actions/${action}${removeFiles ? '?remove_files=true' : ''}`, { method: 'POST' }),
 status: (id: string) => request<ProjectContainer[]>(`/projects/${id}/status`),
 logs: (id: string) => request<{ log: string }>(`/projects/${id}/logs`),
 containerLogs: (id: string, containerId: string) => request<{ log: string }>(`/projects/${id}/containers/${encodeURIComponent(containerId)}/logs`),
 containerAction: (id: string, containerId: string, action: ContainerAction) => request<{ message: string }>(`/projects/${id}/containers/${encodeURIComponent(containerId)}/actions/${action}`, { method: 'POST' }),
 bind: (id: string, input: ProjectBindingInput) => request<{ operation: Operation | null }>(`/projects/${id}/bindings`, { method: 'POST', body: JSON.stringify(input) }),
 operation: (id: string) => request<Operation>(`/operations/${id}`),
}
