import { onMounted, shallowRef } from 'vue'
import { getSystem, type SystemInfo } from '@/services/workbench-platform'
import { errorMessage } from '@/services/workbench'
export function useSystemInfo() {
  const info = shallowRef<SystemInfo>()
  const loading = shallowRef(false)
  const error = shallowRef('')
  async function refresh() {
    loading.value = true
    try {
      info.value = await getSystem()
      error.value = ''
    } catch (cause) {
      error.value = errorMessage(cause)
    } finally {
      loading.value = false
    }
  }
  onMounted(refresh)
  return { info, loading, error, refresh }
}
