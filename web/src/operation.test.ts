import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import type { Operation } from './api'
import { isAbortError, waitOperation } from './operation'
const op = (status: Operation['status']): Operation => ({ id: 'op1', kind: 'deploy', status, log: status, error: '', created_at: '', updated_at: '' })
beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())
test('queued → running → success 后才 resolve', async () => {
  const get = vi.fn().mockResolvedValueOnce(op('running')).mockResolvedValueOnce(op('success'))
  const onUpdate = vi.fn()
  let result: Operation | undefined
  waitOperation(get, op('queued'), { onUpdate }).then(r => { result = r })
  await vi.advanceTimersByTimeAsync(1500)
  expect(get).toHaveBeenCalledTimes(1)
  expect(result).toBeUndefined()
  await vi.advanceTimersByTimeAsync(1500)
  expect(get).toHaveBeenCalledWith('op1')
  expect(result?.status).toBe('success')
  expect(onUpdate.mock.calls.map(c => c[0].status)).toEqual(['queued', 'running', 'success'])
})
test('failed 时 resolve', async () => {
  const get = vi.fn().mockResolvedValue(op('failed'))
  const wait = waitOperation(get, op('running'), { interval: 100 })
  await vi.advanceTimersByTimeAsync(100)
  await expect(wait).resolves.toMatchObject({ status: 'failed' })
})
test('已结束的操作不轮询', async () => {
  const get = vi.fn()
  await expect(waitOperation(get, op('success'))).resolves.toMatchObject({ status: 'success' })
  expect(get).not.toHaveBeenCalled()
})
test('请求出错时 reject', async () => {
  const get = vi.fn().mockRejectedValue(new Error('网络错误'))
  const wait = waitOperation(get, op('queued'))
  const assertion = expect(wait).rejects.toThrow('网络错误')
  await vi.advanceTimersByTimeAsync(1500)
  await assertion
})
test('abort 后停止轮询并 reject AbortError', async () => {
  const get = vi.fn().mockResolvedValue(op('running'))
  const controller = new AbortController()
  const wait = waitOperation(get, op('running'), { signal: controller.signal })
  const caught = wait.catch(e => e)
  await vi.advanceTimersByTimeAsync(1500)
  controller.abort()
  expect(isAbortError(await caught)).toBe(true)
  await vi.advanceTimersByTimeAsync(10000)
  expect(get).toHaveBeenCalledTimes(1)
})
