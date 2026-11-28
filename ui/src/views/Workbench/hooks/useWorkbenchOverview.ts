import { computed, onScopeDispose, shallowRef, watch } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { useVerifiedWorkbenchRefresh } from '@/hooks/useWorkbenchVerification'
import { errorMessage } from '@/services/workbench'
import { loadWorkbenchOverview, type WorkbenchOverview } from '@/services/workbench-read'

let cachedOverview: WorkbenchOverview | undefined

export function useWorkbenchOverview() {
  const { sourceState, expired, contentConfirmationPending } = useWorkbench()
  const overview = shallowRef(cachedOverview)
  const reading = shallowRef(false)
  const readError = shallowRef('')
  const { refresh, refreshing, refreshError } = useVerifiedWorkbenchRefresh(() => read(true))
  const loading = computed(() => reading.value || refreshing.value)
  const error = computed(() => refreshError.value || readError.value)
  const confirmationUpdating = computed(() =>
    contentConfirmationPending(overview.value?.projectionVersion ? overview.value.revision : -1)
  )
  const verification = computed(() => {
    const observed = sourceState.value
    const current = overview.value
    if (!observed?.verification || (observed.changeSequence ?? 0) <= (current?.changeSequence ?? 0))
      return current?.verification ?? observed?.verification
    if (current && observed.verification === 'current') {
      if (!current.projectionVersion) return 'pending'
      if (current.verification === 'stale' || current.projectionVersion !== observed.projectionVersion)
        return 'stale'
    }
    return observed.verification
  })
  let controller: AbortController | undefined
  let pending: Promise<void> | undefined
  let invalidatedWhileReading = false

  function matchesObservation() {
    const observed = sourceState.value
    const current = overview.value
    return current && observed && !readError.value && (
      (current.changeSequence ?? 0) > (observed.changeSequence ?? 0) ||
      (current.revision === observed.revision &&
        current.projectionVersion === observed.projectionVersion && current.verification === observed.verification &&
        current.verificationError === observed.verificationError)
    )
  }

  function read(replacePending = false): Promise<void> {
    if (expired.value) return Promise.resolve()
    if (pending && !replacePending) {
      invalidatedWhileReading = true
      return pending
    }
    controller?.abort()
    invalidatedWhileReading = false
    const request = new AbortController()
    controller = request
    reading.value = true
    pending = loadWorkbenchOverview(request.signal)
      .then((value) => {
        if (request.signal.aborted) return
        const previous = cachedOverview
        if (previous?.projectionVersion && (
          !value.projectionVersion || value.revision < previous.revision ||
          (value.revision === previous.revision && (value.changeSequence ?? 0) < (previous.changeSequence ?? 0))
        )) return
        cachedOverview = value
        overview.value = value
        readError.value = ''
      })
      .catch((cause) => {
        if (!request.signal.aborted) readError.value = errorMessage(cause)
      })
      .finally(() => {
        if (controller !== request) return
        controller = undefined
        pending = undefined
        reading.value = false
        if (!request.signal.aborted && invalidatedWhileReading) {
          invalidatedWhileReading = false
          if (!matchesObservation()) void read()
        }
      })
    return pending
  }

  watch([
    () => sourceState.value?.revision,
    () => sourceState.value?.projectionVersion,
    () => sourceState.value?.projectionObservationId,
    () => sourceState.value?.verification,
    () => sourceState.value?.verificationError,
    confirmationUpdating,
    refreshing
  ], () => {
    if (!refreshing.value && (confirmationUpdating.value || !matchesObservation())) void read()
  }, { immediate: true })
  onScopeDispose(() => controller?.abort())

  return { overview, loading, error, verification, refresh, confirmationUpdating }
}
