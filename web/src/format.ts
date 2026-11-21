const pad = (n: number) => String(n).padStart(2, '0')
// 接口时间为 RFC 3339，界面统一显示本地时间 YYYY-MM-DD HH:mm；空值或无效值显示 —
export function formatTime(value: string) {
  const d = new Date(value)
  if (!value || Number.isNaN(d.getTime())) return '—'
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
export function formatDate(value: string) {
  const text = formatTime(value)
  return text === '—' ? text : text.slice(0, 10)
}
export const errorText = (e: unknown, fallback: string) => e instanceof Error && e.message ? e.message : fallback

const capacityFormat = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 })
const capacityUnits = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
export function formatBytes(bytes: number) {
  const unit = bytes < 1 ? 0 : Math.min(capacityUnits.length - 1, Math.floor(Math.log2(bytes) / 10))
  return `${capacityFormat.format(bytes / 1024 ** unit)} ${capacityUnits[unit]}`
}

export const formatPercent = (percent: number | null) => percent === null ? '暂不可用' : `${capacityFormat.format(percent)}%`
export const usagePercent = (used: number, total: number) => total > 0 ? used / total * 100 : null
export const formatSampleTime = (at: number) => new Date(at * 1000).toLocaleTimeString('zh-CN', { hour12: false })
