import { computed, onScopeDispose, shallowRef, watch } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { errorMessage } from '@/services/workbench'
import {
  loadWorkbenchOverview,
  requestWorkbenchReadRefresh,
  type WorkbenchOverview
} from '@/services/workbench-read'

let cachedOverview: WorkbenchOverview | undefined

export function useWorkbenchOverview() {
  const { sourceState, expired } = useWorkbench()
  const overview = shallowRef(cachedOverview)
  const loading = shallowRef(false)
  const error = shallowRef('')
  const verification = computed(() => {
    const observed = sourceState.value
    const current = overview.value
    if (!observed?.verification || (observed.changeSequence ?? 0) < (current?.changeSequence ?? 0))
      return current?.verification
    return observed.verification
  })
  let controller: AbortController | undefined
  let pending: Promise<void> | undefined
  let invalidatedWhileReading = false

  function matchesObservation() {
    const observed = sourceState.value
    const current = overview.value
    return current && observed && !error.value && (
      (current.changeSequence ?? 0) > (observed.changeSequence ?? 0) ||
      (current.revision === observed.revision &&
        current.projectionVersion === observed.projectionVersion && current.verification === observed.verification &&
        current.verificationError === observed.verificationError)
    )
  }

  function read(): Promise<void> {
    if (expired.value) return Promise.resolve()
    if (pending) {
      invalidatedWhileReading = true
      return pending
    }
    const request = new AbortController()
    controller = request
    loading.value = true
    pending = loadWorkbenchOverview(request.signal)
      .then((value) => {
        if (request.signal.aborted) return
        cachedOverview = value
        overview.value = value
        error.value = ''
      })
      .catch((cause) => {
        if (!request.signal.aborted) error.value = errorMessage(cause)
      })
      .finally(() => {
        pending = undefined
        loading.value = false
        if (!request.signal.aborted && invalidatedWhileReading) {
          invalidatedWhileReading = false
          if (!matchesObservation()) void read()
        }
      })
    return pending
  }

  async function refresh() {
    try {
      await requestWorkbenchReadRefresh()
      await read()
    } catch (cause) {
      error.value = errorMessage(cause)
    }
  }

  watch([
    () => sourceState.value?.revision,
    () => sourceState.value?.projectionVersion,
    () => sourceState.value?.verification,
    () => sourceState.value?.verificationError
  ], () => {
    if (!matchesObservation()) void read()
  }, { immediate: true })
  onScopeDispose(() => controller?.abort())

  return { overview, loading, error, verification, refresh }
}
