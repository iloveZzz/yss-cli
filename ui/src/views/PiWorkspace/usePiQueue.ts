import { computed, onScopeDispose, shallowRef, watch, type Ref } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { ApiError, errorMessage } from '@/services/workbench'
import {
  getPiQueue, putPiQueueItem, removePiQueueItem, resumePiQueue,
  type PiQueue, type PiQueueDraft
} from '@/services/workbench-agent'
import { requestId } from '@/services/workbench-platform'

export type PiEnqueueInput = Omit<PiQueueDraft, 'version' | 'clientId'>
type PendingEnqueue = PiEnqueueInput & { clientId: string }
const enqueueKey = (id: string) => 'yss:pi:enqueue:1:' + encodeURIComponent(id)

function readPending(id: string): PendingEnqueue | undefined {
  if (!id) return
  try {
    const stored = sessionStorage.getItem(enqueueKey(id))
    const value: PendingEnqueue | undefined = stored ? JSON.parse(stored) : undefined
    if (typeof value?.clientId === 'string' && typeof value.prompt === 'string' &&
      typeof value.readOnly === 'boolean' && (!value.citations || Array.isArray(value.citations))) return value
  } catch {
    // An unavailable storage entry cannot be resumed.
  }
}

export function usePiQueue(run: Readonly<Ref<string>>, onChanged: () => void) {
  const { sourceState, expired } = useWorkbench()
  const queue = shallowRef<PiQueue>()
  const error = shallowRef('')
  const uncertain = shallowRef(false)
  const pendingEnqueue = shallowRef<PendingEnqueue>()
  const reading = shallowRef(false)
  const changing = shallowRef(false)
  const busy = computed(() => reading.value || changing.value)
  const observed = computed(() => sourceState.value?.agentQueueVersions?.[run.value])
  let request: AbortController | undefined

  function set(value: PiQueue) {
    if (run.value !== value.runId || (queue.value && value.version < queue.value.version)) return
    queue.value = value
  }
  async function refresh() {
    const id = run.value
    if (!id || expired.value) return false
    request?.abort()
    const own = new AbortController()
    request = own
    reading.value = true
    try {
      const value = await getPiQueue(id, own.signal)
      if (own.signal.aborted || run.value !== id) return false
      set(value)
      return true
    } catch (cause) {
      if (!own.signal.aborted) error.value = errorMessage(cause)
      return false
    } finally {
      if (request === own) reading.value = false
    }
  }

  async function retryRead() {
    if (await refresh()) {
      uncertain.value = false
      error.value = ''
    }
  }

  function clearPending(id: string) {
    try {
      sessionStorage.removeItem(enqueueKey(id))
    } catch {
      // A retained entry can still be retried safely with the same client ID.
    }
    if (id === run.value) pendingEnqueue.value = undefined
  }
  async function change(write: (id: string) => Promise<PiQueue>, enqueue = false) {
    const id = run.value
    if (!id || busy.value || (!enqueue && (uncertain.value || pendingEnqueue.value)) || expired.value) return false
    changing.value = true
    error.value = ''
    try {
      const next = await write(id)
      if (enqueue) clearPending(id)
      if (id === run.value) {
        uncertain.value = false
        set(next)
        onChanged()
      }
      return true
    } catch (cause) {
      const unclear = !(cause instanceof ApiError) || cause.status >= 500 || cause.status === 408 ||
        (enqueue && cause.status === 409 && cause.context.queueRejected !== 'true')
      if (enqueue && !unclear) clearPending(id)
      if (id !== run.value) return false
      if (unclear) {
        uncertain.value = true
        error.value = enqueue ? '入队结果待核对，可以按原任务重试。' : '队列操作结果待核对，请读取最新队列后确认。'
      } else {
        uncertain.value = false
        if (cause.status === 409) await refresh()
        if (id === run.value) error.value = errorMessage(cause)
      }
      return false
    } finally {
      changing.value = false
    }
  }
  async function enqueue(input?: PiEnqueueInput) {
    const id = run.value
    const version = queue.value?.version
    if (!id || version === undefined || busy.value || expired.value) return false
    let pending = pendingEnqueue.value
    if (!pending) {
      if (!input || uncertain.value) return false
      pending = { ...input, clientId: requestId() }
      try {
        sessionStorage.setItem(enqueueKey(id), JSON.stringify(pending))
      } catch {
        error.value = '此标签页无法保存入队请求，原任务仍保留。'
        return false
      }
      pendingEnqueue.value = pending
    }
    const requestBody = { ...pending, version }
    return change((id) => putPiQueueItem(id, requestBody), true)
  }

  watch(run, (id) => {
    request?.abort()
    queue.value = undefined
    error.value = ''
    uncertain.value = false
    pendingEnqueue.value = readPending(id)
    reading.value = false
    void refresh()
  }, { immediate: true })
  watch(observed, (version) => {
    if (version !== undefined && version !== String(queue.value?.version)) void refresh()
  })
  onScopeDispose(() => request?.abort())

  return {
    queue, error, busy, uncertain, pendingEnqueue, retryRead, enqueue,
    put: (draft: PiQueueDraft, itemId?: string) => change((id) => putPiQueueItem(id, draft, itemId)),
    remove: (itemId: string, version: number) => change((id) => removePiQueueItem(id, itemId, version)),
    resume: (version: number, confirmDirectEdit?: boolean) => change((id) => resumePiQueue(id, version, confirmDirectEdit))
  }
}
