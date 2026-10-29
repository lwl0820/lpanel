import { onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import type { Operation } from './api'
import { isAbortError, waitOperation } from './operation'
import { errorText } from './format'

// 页面内跟踪长操作：queued / running 期间 busy，成功后才提示，失败原因交给 OperationBanner，卸载时停止轮询
export function useOperation(get: (id: string) => Promise<Operation>) {
  const operation = ref<Operation | null>(null)
  const label = ref('')
  const busy = ref(false)
  let controller: AbortController | undefined
  onUnmounted(() => controller?.abort())
  // 返回最终状态；读取失败或被取消时返回 null
  async function track(op: Operation, text: string, success: string, requestError = '读取操作结果失败') {
    controller?.abort()
    const mine = controller = new AbortController()
    label.value = text; busy.value = true
    try {
      const result = await waitOperation(get, op, { signal: mine.signal, onUpdate: value => { operation.value = value } })
      if (result.status === 'success') { ElMessage.success(success); operation.value = null }
      return result
    } catch (e) {
      if (!isAbortError(e)) ElMessage.error(errorText(e, requestError))
      return null
    } finally { if (controller === mine) busy.value = false }
  }
  return { operation, label, busy, track }
}
