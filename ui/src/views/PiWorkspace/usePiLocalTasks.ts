import { computed, onScopeDispose, shallowRef, watch, type Ref } from 'vue'
import { useEventListener } from '@vueuse/core'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  acknowledgeRequest, ApiError, errorMessage, pendingRequestBody, pendingRequests, resolvePendingRequest
} from '@/services/workbench'
import { agentLocalTasksPath, getAgentLocalTasks, startAgentLocalTask, type LocalProjectTaskInput } from '@/services/workbench-agent'
import { objectValue, requestId, terminalJob, type LocalProjectTaskJob } from '@/services/workbench-platform'

export function usePiLocalTasks(runId: string, open: Readonly<Ref<boolean>>, recheckRound: () => void) {
  const { sourceState } = useWorkbench()
  const jobs = shallowRef<LocalProjectTaskJob[]>([])
  const loaded = shallowRef(false)
  const reading = shallowRef(false)
  const readError = shallowRef('')
  const sending = shallowRef(false)
  const launchError = shallowRef('')
  const running = computed(() => jobs.value.some((job) => !terminalJob(job.status)))
  const path = agentLocalTasksPath(runId)
  const pending = computed(() => pendingRequests.value.find((intent) => intent.path === path && intent.method === 'POST'))
  const pendingInput = computed<LocalProjectTaskInput | undefined>(() => {
    if (!pending.value) return undefined
    const body = pendingRequestBody(path, pending.value.requestId)
    if (!body || (body.action !== 'test' && body.action !== 'build') ||
      typeof body.projectId !== 'string' || typeof body.directory !== 'string' ||
      typeof body.expectedJobId !== 'string') return undefined
    const { rootPath, head, branch } = objectValue(body.expectedCheckout)
    const expectedCheckout = typeof rootPath === 'string' && typeof head === 'string' &&
      (typeof branch === 'string' || branch === null) ? { rootPath, head, branch } : undefined
    if (body.expectedCheckout !== undefined && !expectedCheckout) return undefined
    return {
      action: body.action, projectId: body.projectId, directory: body.directory,
      expectedJobId: body.expectedJobId, expectedCheckout, requestId: pending.value.requestId
    }
  })
  let disposed = false
  let read: AbortController | undefined
  let timer: ReturnType<typeof setTimeout> | undefined

  function stopRead() {
    clearTimeout(timer)
    timer = undefined
    read?.abort()
    read = undefined
    reading.value = false
  }
  function scheduleRead(delay: number) {
    if (!open.value || disposed || timer) return
    timer = setTimeout(() => {
      timer = undefined
      void refresh()
    }, delay)
  }
  async function refresh() {
    if (!open.value || disposed || read) return
    stopRead()
    const current = new AbortController()
    read = current
    reading.value = true
    try {
      const result = await getAgentLocalTasks(runId, current.signal)
      if (current.signal.aborted) return
      jobs.value = result.items
      loaded.value = true
      readError.value = ''
      const id = pending.value?.status === 'uncertain' ? pending.value.requestId : undefined
      if (id && result.items.some((item) => item.requestId === id)) {
        resolvePendingRequest(id)
        void acknowledgeRequest(id)
        launchError.value = ''
      }
    } catch (cause) {
      if (!current.signal.aborted) readError.value = errorMessage(cause)
    } finally {
      if (read === current) {
        read = undefined
        reading.value = false
        if (running.value) scheduleRead(1500)
      }
    }
  }
  async function launch(input: Omit<LocalProjectTaskInput, 'requestId'> | LocalProjectTaskInput) {
    if (disposed || !open.value || sending.value || reading.value) return
    const retrying = 'requestId' in input
    if (retrying ? pending.value?.status !== 'uncertain' || input.requestId !== pending.value.requestId
      : running.value || pending.value) return
    const body = retrying ? input : { ...input, requestId: requestId() }
    sending.value = true
    launchError.value = ''
    try {
      // Let an explicitly started submission finish recording its receipt after navigation.
      const job = await startAgentLocalTask(runId, body)
      if (!disposed) {
        stopRead()
        jobs.value = [job, ...jobs.value.filter((item) => item.id !== job.id)]
      }
    } catch (cause) {
      if (!disposed) {
        launchError.value = errorMessage(cause)
        if (cause instanceof ApiError && cause.status === 409) recheckRound()
      }
    } finally {
      if (!disposed) {
        sending.value = false
        void refresh()
      }
    }
  }

  watch(open, (show) => show ? void refresh() : stopRead())
  watch(() => sourceState.value?.jobsVersion, () => scheduleRead(160))
  useEventListener(window, 'focus', refresh)
  onScopeDispose(() => {
    disposed = true
    stopRead()
  })

  return { jobs, loaded, reading, readError, sending, launchError, running, pending, pendingInput, refresh, launch }
}
