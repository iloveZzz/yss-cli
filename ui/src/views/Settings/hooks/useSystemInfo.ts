import { onBeforeUnmount, onMounted, shallowRef } from 'vue'
import { getSystem, type SystemInfo } from '@/services/workbench-platform'
import { errorMessage } from '@/services/workbench'
export function useSystemInfo() {
  const info = shallowRef<SystemInfo>()
  const readAt = shallowRef<string>()
  const loading = shallowRef(false)
  const error = shallowRef('')
  let controller: AbortController | undefined
  async function refresh() {
    if (loading.value) return
    const current = new AbortController()
    controller = current
    loading.value = true
    try {
      const result = await getSystem(current.signal)
      if (!current.signal.aborted) {
        info.value = result
        readAt.value = new Date().toISOString()
        error.value = ''
      }
    } catch (cause) {
      if (!current.signal.aborted) error.value = errorMessage(cause)
    } finally {
      if (controller === current) loading.value = false
    }
  }
  onMounted(refresh)
  onBeforeUnmount(() => controller?.abort())
  return { info, readAt, loading, error, refresh }
}
