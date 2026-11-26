import { until } from '@vueuse/core'
import { computed, onScopeDispose, shallowRef } from 'vue'
import { errorMessage } from '@/services/workbench'
import { requestWorkbenchReadRefresh } from '@/services/workbench-read'
import { useWorkbench } from './useWorkbench'
import { withTimeout } from '@/lib/abort'

export function useWorkbenchVerification() {
  const { expired, sourceState, checkChanges } = useWorkbench()

  async function verify(signal: AbortSignal, failureMessage = '工作台进度核验失败。'): Promise<void> {
    const refreshDeadline = withTimeout(signal, 10_000)
    let minimumObservationId: number
    try {
      ;({ minimumObservationId } = await requestWorkbenchReadRefresh(refreshDeadline.signal))
    } finally {
      refreshDeadline.dispose()
    }
    if (!Number.isSafeInteger(minimumObservationId) || minimumObservationId < 1)
      throw new Error('工作台未返回核验序号。')

    const waitDeadline = withTimeout(signal, 60_000)
    const waitSignal = waitDeadline.signal
    const stopped = shallowRef(waitSignal.aborted)
    const stop = () => { stopped.value = true }
    waitSignal.addEventListener('abort', stop, { once: true })
    const completion = computed(() => {
      if (stopped.value || expired.value) return 'stopped'
      const published = sourceState.value?.projectionObservationId ?? 0
      const failed = sourceState.value?.failedObservationId ?? 0
      if (failed >= minimumObservationId && failed > published) return 'failed'
      if (published >= minimumObservationId) return 'success'
      return undefined
    })
    let statusTimer: ReturnType<typeof setInterval> | undefined
    let checkingStatus = false
    const statusController = new AbortController()
    try {
      if (!completion.value) {
        // checkChanges only polls when the live stream is disconnected.
        statusTimer = setInterval(async () => {
          if (checkingStatus || completion.value) return
          checkingStatus = true
          try {
            const statusDeadline = withTimeout(statusController.signal, 5_000)
            try {
              await checkChanges(statusDeadline.signal)
            } finally {
              statusDeadline.dispose()
            }
          } finally {
            checkingStatus = false
          }
        }, 2_000)
        await until(completion).toBeTruthy()
      }
      if (completion.value === 'stopped') {
        if (waitSignal.reason instanceof DOMException && waitSignal.reason.name === 'TimeoutError')
          throw new Error('核验仍在进行，请稍后查看结果。')
        throw new DOMException('核验已取消。', 'AbortError')
      }
      if (completion.value === 'failed') throw new Error(sourceState.value?.verificationError || failureMessage)
    } finally {
      clearInterval(statusTimer)
      statusController.abort()
      waitSignal.removeEventListener('abort', stop)
      waitDeadline.dispose()
    }
  }

  return { verify }
}

export function useVerifiedWorkbenchRefresh(
  read: () => Promise<void>,
  enabled: () => boolean = () => true
) {
  const { expired } = useWorkbench()
  const { verify } = useWorkbenchVerification()
  const refreshing = shallowRef(false)
  const refreshError = shallowRef('')
  let controller: AbortController | undefined
  let pending: Promise<void> | undefined
  let disposed = false

  function refresh(): Promise<void> {
    if (pending) return pending
    if (expired.value || disposed || !enabled()) return Promise.resolve()
    const own = new AbortController()
    controller = own
    refreshing.value = true
    refreshError.value = ''
    pending = verify(own.signal)
      .then(async () => {
        if (!own.signal.aborted && !expired.value && enabled()) await read()
      })
      .catch((cause) => {
        if (own.signal.aborted || expired.value) return
        refreshError.value = cause instanceof DOMException && cause.name === 'TimeoutError'
          ? '请求核验超时，请重试。'
          : errorMessage(cause)
      })
      .finally(() => {
        controller = undefined
        pending = undefined
        refreshing.value = false
      })
    return pending
  }

  onScopeDispose(() => {
    disposed = true
    controller?.abort()
  })
  return { refresh, refreshing, refreshError }
}
