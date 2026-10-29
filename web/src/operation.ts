import type { Operation } from './api'
export interface WaitOptions { interval?: number; signal?: AbortSignal; onUpdate?: (op: Operation) => void }
const pending = (op: Operation) => op.status === 'queued' || op.status === 'running'
function abortError() { return new DOMException('操作等待已取消', 'AbortError') }
function sleep(ms: number, signal?: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal?.aborted) { reject(abortError()); return }
    const timer = setTimeout(() => { signal?.removeEventListener('abort', onAbort); resolve() }, ms)
    function onAbort() { clearTimeout(timer); reject(abortError()) }
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}
// 轮询直到 success / failed；queued、running 都视为未结束
export async function waitOperation(get: (id: string) => Promise<Operation>, op: Operation, opts: WaitOptions = {}): Promise<Operation> {
  const { interval = 1500, signal, onUpdate } = opts
  let current = op
  onUpdate?.(current)
  while (pending(current)) {
    await sleep(interval, signal)
    current = await get(current.id)
    if (signal?.aborted) throw abortError()
    onUpdate?.(current)
  }
  return current
}
export const isAbortError = (e: unknown) => e instanceof DOMException && e.name === 'AbortError'
