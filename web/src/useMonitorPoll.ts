import { onMounted, onUnmounted, ref, shallowRef } from 'vue'
import { errorText } from './format'

export function useMonitorPoll<T>(read: (signal: AbortSignal) => Promise<T>, fallback: string) {
  const data = shallowRef<T>()
  const error = ref('')
  const loading = ref(true)
  const controller = new AbortController()
  let timer: number | undefined

  async function refresh() {
    window.clearTimeout(timer)
    try {
      data.value = await read(controller.signal)
      error.value = ''
    } catch (e) {
      if (!controller.signal.aborted) error.value = errorText(e, fallback)
    } finally {
      loading.value = false
      if (!controller.signal.aborted) timer = window.setTimeout(refresh, 5000)
    }
  }

  onMounted(refresh)
  onUnmounted(() => {
    controller.abort()
    window.clearTimeout(timer)
  })
  return { data, error, loading }
}
