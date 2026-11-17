import { onBeforeUnmount, shallowRef, watch, type Ref } from 'vue'
import { apiRequest, errorMessage } from '@/services/workbench'
import type { JobEvent } from '@/services/workbench-platform'

interface EventPage {
  items: JobEvent[]
  nextAfter: number
  hasMore: boolean
}

export function useJobHistory(jobId: Readonly<Ref<string>>) {
  const items = shallowRef<JobEvent[]>([])
  const loading = shallowRef(false)
  const error = shallowRef('')
  const page = shallowRef(0)
  const hasMore = shallowRef(false)
  const loaded = shallowRef(false)
  let cursors = [0]
  let controller: AbortController | undefined
  let generation = 0
  async function read(target = page.value) {
    controller?.abort()
    const connection = new AbortController()
    controller = connection
    const own = ++generation
    loading.value = true
    try {
      const result = await apiRequest<EventPage>(
        `/api/jobs/${encodeURIComponent(jobId.value)}/event-page?${new URLSearchParams({ after: String(cursors[target] ?? 0) })}`,
        { signal: connection.signal }
      )
      if (own !== generation) return
      items.value = result.items
      hasMore.value = result.hasMore
      cursors[target + 1] = result.nextAfter
      page.value = target
      loaded.value = true
      error.value = ''
    } catch (cause) {
      if (own === generation && !connection.signal.aborted) error.value = errorMessage(cause)
    } finally {
      if (own === generation) loading.value = false
    }
  }
  watch(jobId, () => {
    generation++
    controller?.abort()
    items.value = []
    cursors = [0]
    page.value = 0
    loaded.value = false
    error.value = ''
    loading.value = false
    hasMore.value = false
  })
  onBeforeUnmount(() => {
    generation++
    controller?.abort()
  })
  return { items, loading, error, page, hasMore, loaded, read }
}
