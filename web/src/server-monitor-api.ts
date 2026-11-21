import { request } from './api'

export interface MonitorDisk {
  mount: string
  total_bytes: number
  used_bytes: number
}

interface MonitorUsage {
  at: number
  cpu_percent: number | null
  memory_used_bytes: number
  memory_total_bytes: number
}

export interface MonitorSummary extends MonitorUsage {
  root_disk: MonitorDisk | null
}

export interface MonitorSample extends MonitorUsage {
  disks: MonitorDisk[]
}

export interface MonitorProcess {
  pid: number
  name: string
  cpu_percent: number | null
  rss_bytes: number
  read_bps: number | null
  write_bps: number | null
}

export interface MonitorRange {
  from: number
  to: number
}

const prefix = '/server/monitor'
export const serverMonitor = {
  summary: (signal: AbortSignal) => request<MonitorSummary>(`${prefix}/summary`, { signal }),
  disks: (signal: AbortSignal) => request<{ at: number; disks: MonitorDisk[] }>(`${prefix}/disks`, { signal }),
  processes: (signal: AbortSignal) => request<{ at: number; processes: MonitorProcess[] }>(`${prefix}/processes`, { signal }),
  history: ({ from, to }: MonitorRange, signal: AbortSignal) =>
    request<{ samples: MonitorSample[] }>(`${prefix}/history?from=${from}&to=${to}`, { signal }),
}
