import { computed, onScopeDispose, shallowRef, watch } from 'vue'
import { provideWorkbenchScope, useWorkbench } from './useWorkbench'
import { registerWorkbenchRouteReads, useWorkbenchCatalog } from './useWorkbenchReads'
import { ApiError, errorMessage, type Snapshot } from '@/services/workbench'
import { loadSpecDetail, type SpecDetailRead } from '@/services/workbench-read'

export function provideSpecDetailWorkbench(specId: () => string | undefined) {
  const { expired, sourceState, submit: submitCommand } = useWorkbench()
  const { catalog } = useWorkbenchCatalog()
  const detail = shallowRef<SpecDetailRead>()
  const loading = shallowRef(false)
  const readError = shallowRef('')
  const error = computed(() => readError.value || (sourceState.value?.verification === 'failed'
    ? `工作台后台核验失败。${sourceState.value.verificationError ?? ''}` : ''))
  const version = computed(() => sourceState.value?.projectionVersion
    ? sourceState.value.specVersions?.[specId() ?? '']
    : undefined)
  const observation = computed(() => sourceState.value?.projectionObservationId)
  const snapshot = computed<Snapshot | null>(() => {
    if (!detail.value) return null
    const { snapshot: value, specVersion } = detail.value
    // The published revision belongs to this fingerprint even while a later revision is pending.
    const revision = version.value === specVersion
      ? Math.max(value.revision, sourceState.value?.projectionRevision ?? value.revision)
      : value.revision
    if (catalog.value && catalog.value.revision > value.revision && catalog.value.revision <= revision) {
      const projects = new Map(value.projects.map((project) => [project.record.id, project]))
      return {
        ...value,
        revision,
        projects: catalog.value.projects.map((record) => ({
          available: false,
          sourceDirectories: [],
          message: null,
          ...projects.get(record.id),
          record
        }))
      }
    }
    return revision === value.revision ? value : { ...value, revision }
  })
  let pending: { controller: AbortController; fresh: boolean; result: Promise<boolean> } | undefined
  let disposed = false

  function refreshChangedSpec() {
    const observed = version.value
    const knownMissing = detail.value && sourceState.value?.verification === 'current'
    if (detail.value && observation.value != null && observation.value < detail.value.observationId) return
    if (!pending && (observed || knownMissing) && observed !== detail.value?.specVersion) void read(false)
  }

  function read(fresh: boolean): Promise<boolean> {
    const id = specId()
    if (!id || disposed || expired.value) return Promise.resolve(false)
    if (pending) {
      if (!fresh || pending.fresh) return pending.result
      return pending.result.then(() => specId() === id ? read(true) : false)
    }
    const controller = new AbortController()
    const requestedVersion = version.value
    const requestedObservation = observation.value
    loading.value = true
    const result = loadSpecDetail(id, fresh, controller.signal)
      .then((next) => {
        if (controller.signal.aborted) return false
        const previous = detail.value
        const revision = snapshot.value?.revision ?? -1
        if (!previous || next.snapshot.revision > revision ||
          (next.snapshot.revision === revision && next.observationId >= previous.observationId)) {
          detail.value = next
        }
        readError.value = ''
        return true
      })
      .catch((cause) => {
        if (controller.signal.aborted) return false
        if (cause instanceof ApiError && cause.status === 404) {
          detail.value = undefined
        }
        readError.value = errorMessage(cause)
        return false
      })
      .finally(() => {
        if (pending?.controller !== controller) return
        pending = undefined
        loading.value = false
        if (requestedVersion !== version.value || requestedObservation !== observation.value) refreshChangedSpec()
      })
    pending = { controller, fresh, result }
    return result
  }

  function clearRequest() {
    pending?.controller.abort()
    pending = undefined
    loading.value = false
  }

  watch(specId, () => {
    clearRequest()
    detail.value = undefined
    readError.value = ''
    void read(false)
  }, { immediate: true })
  watch([version, observation], refreshChangedSpec)
  onScopeDispose(() => {
    disposed = true
    clearRequest()
  })

  const refresh = () => read(true)
  async function refreshAfterCommand(id: string | undefined, revision?: number) {
    const ok = await refresh()
    if (ok && !disposed && specId() === id && revision !== undefined &&
      (snapshot.value?.revision ?? -1) < revision) await refresh()
  }
  registerWorkbenchRouteReads({ busy: loading, readAt: computed(() => snapshot.value?.readAt), refresh })

  return provideWorkbenchScope({
    snapshot,
    loading,
    error,
    refresh,
    submit(action, expectedRevision = snapshot.value?.revision, expectedTarget, refreshAfter) {
      if (expectedRevision === undefined) throw new Error('请先读取 Spec')
      const id = specId()
      return submitCommand(action, expectedRevision, expectedTarget, (revision) =>
        !disposed && specId() === id
          ? (refreshAfter ? refreshAfter(revision) : refreshAfterCommand(id, revision))
          : Promise.resolve(false)
      )
    }
  })
}
