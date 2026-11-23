import { computed, shallowReadonly, shallowRef } from 'vue'
import {
  ApiError,
  errorMessage,
  executeCommand,
  expired,
  loadSnapshot,
  type Action,
  type Snapshot
} from '@/services/workbench'
import { getSourceState, readNdjson, type SourceState } from '@/services/workbench-platform'
const sourceVersions = shallowRef<Record<string, string>>({})
const externalVersion = shallowRef('')
let progressVersions = ''
let changesController: AbortController | undefined
let reconnectTimer: ReturnType<typeof setTimeout> | undefined
let sourceRetryTimer: ReturnType<typeof setTimeout> | undefined
let changing = false
let pendingSource: SourceState | undefined
const snapshot = shallowRef<Snapshot | null>(null)
const loading = shallowRef(false)
const saving = shallowRef(false)
const error = shallowRef('')
let controller: AbortController | undefined
async function refresh(): Promise<boolean> {
  if (expired.value) return false
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  try {
    const next = await loadSnapshot(current.signal)
    if (!current.signal.aborted) {
      snapshot.value = next
      error.value = ''
      return true
    }
  } catch (e) {
    if (!current.signal.aborted) error.value = errorMessage(e)
  } finally {
    if (controller === current) loading.value = false
  }
  return false
}
async function submit(
  action: Action,
  expectedRevision = snapshot.value?.revision,
  expectedTarget?: string
) {
  if (saving.value) throw new Error('请等待当前保存完成')
  if (expectedRevision === undefined) throw new Error('请先读取工作台')
  saving.value = true
  try {
    const result = await executeCommand({
      action,
      expectedRevision,
      expectedTarget,
      requestId: crypto.randomUUID()
    })
    await refresh()
    return result
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) await refresh()
    throw e
  } finally {
    saving.value = false
  }
}
async function applySourceState(value: SourceState): Promise<void> {
  clearTimeout(sourceRetryTimer)
  pendingSource = value
  if (changing) return
  changing = true
  try {
    while (pendingSource) {
      const state = pendingSource
      pendingSource = undefined
      const previous = sourceVersions.value
      const incomingProgress = JSON.stringify([state.progressVersions ?? [], state.inputVersions ?? ''])
      const progressChanged = progressVersions !== incomingProgress
      if (!snapshot.value) {
        sourceVersions.value = state.sourceVersions
        progressVersions = incomingProgress
        externalVersion.value = state.externalVersion ?? ''
        continue
      }
      if (snapshot.value.revision !== state.revision) {
        const refreshed = await refresh()
        if (!refreshed && !expired.value) sourceRetryTimer = setTimeout(() => void checkChanges(), 2000)
        if (snapshot.value?.revision === state.revision) {
          sourceVersions.value = state.sourceVersions
          progressVersions = incomingProgress
          externalVersion.value = state.externalVersion ?? ''
        }
        continue
      }
      const changed = Object.keys(state.sourceVersions).filter(
        (id) => previous[id] !== undefined && previous[id] !== state.sourceVersions[id]
      )
      if (!changed.length && !progressChanged) {
        sourceVersions.value = state.sourceVersions
        progressVersions = incomingProgress
        externalVersion.value = state.externalVersion ?? ''
        continue
      }
      if (await refresh()) {
        sourceVersions.value = state.sourceVersions
        progressVersions = incomingProgress
        externalVersion.value = state.externalVersion ?? ''
      } else if (!expired.value) {
        sourceRetryTimer = setTimeout(() => void checkChanges(), 2000)
      }
    }
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    changing = false
  }
}
async function checkChanges() {
  try {
    await applySourceState(await getSourceState())
  } catch (cause) {
    error.value = errorMessage(cause)
  }
}
function startChanges() {
  if (changesController) return
  const own = new AbortController()
  changesController = own
  void readNdjson<SourceState>('/api/changes', own.signal, (value) => void applySourceState(value))
    .catch((cause) => {
      if (!own.signal.aborted) error.value = errorMessage(cause)
    })
    .finally(() => {
      if (changesController === own) {
        changesController = undefined
        if (!own.signal.aborted && !expired.value) reconnectTimer = setTimeout(startChanges, 2000)
      }
    })
}
function stopChanges() {
  clearTimeout(reconnectTimer)
  clearTimeout(sourceRetryTimer)
  changesController?.abort()
  changesController = undefined
}
const activeSpecs = computed(
  () =>
    snapshot.value?.specs.filter(
      (s) =>
        s.record.active &&
        snapshot.value?.projects.some((p) => p.record.id === s.record.projectId && p.record.active)
    ) ?? []
)
export function useWorkbench() {
  return {
    snapshot: shallowReadonly(snapshot),
    loading: shallowReadonly(loading),
    saving: shallowReadonly(saving),
    error: shallowReadonly(error),
    expired: shallowReadonly(expired),
    activeSpecs,
    sourceVersions: shallowReadonly(sourceVersions),
    externalVersion: shallowReadonly(externalVersion),
    startChanges,
    stopChanges,
    checkChanges,
    refresh,
    submit
  }
}
