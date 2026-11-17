import { computed, shallowReadonly, shallowRef } from 'vue'
import {
  ApiError,
  apiRequest,
  errorMessage,
  executeCommand,
  expired,
  loadSnapshot,
  type Action,
  type Snapshot,
  type SpecView
} from '@/services/workbench'
import { getSourceState, readNdjson, type SourceState } from '@/services/workbench-platform'
const sourceVersions = shallowRef<Record<string, string>>({})
let changesController: AbortController | undefined
let reconnectTimer: ReturnType<typeof setTimeout> | undefined
let changing = false
let pendingSource: SourceState | undefined
const snapshot = shallowRef<Snapshot | null>(null)
const loading = shallowRef(false)
const saving = shallowRef(false)
const error = shallowRef('')
let controller: AbortController | undefined
async function refresh() {
  if (expired.value) return
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  try {
    const next = await loadSnapshot(current.signal)
    if (!current.signal.aborted) {
      snapshot.value = next
      error.value = ''
    }
  } catch (e) {
    if (!current.signal.aborted) error.value = errorMessage(e)
  } finally {
    if (controller === current) loading.value = false
  }
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
  pendingSource = value
  if (changing) return
  changing = true
  try {
    while (pendingSource) {
      const state = pendingSource
      pendingSource = undefined
      const previous = sourceVersions.value
      if (!snapshot.value) {
        sourceVersions.value = state.sourceVersions
        continue
      }
      if (snapshot.value.revision !== state.revision) {
        await refresh()
        if (snapshot.value?.revision === state.revision) sourceVersions.value = state.sourceVersions
        continue
      }
      const changed = Object.keys(state.sourceVersions).filter(
        (id) => previous[id] !== undefined && previous[id] !== state.sourceVersions[id]
      )
      if (!changed.length) {
        sourceVersions.value = state.sourceVersions
        continue
      }
      const revision = snapshot.value.revision
      const views = await Promise.all(
        changed.map((id) => apiRequest<SpecView>(`/api/specs/${encodeURIComponent(id)}/view`))
      )
      if (snapshot.value?.revision !== revision) continue
      const replacements = new Map(views.map((view) => [view.record.id, view]))
      snapshot.value = {
        ...snapshot.value,
        readAt: state.readAt,
        specs: snapshot.value.specs.map((spec) => replacements.get(spec.record.id) ?? spec)
      }
      sourceVersions.value = state.sourceVersions
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
    startChanges,
    stopChanges,
    checkChanges,
    refresh,
    submit
  }
}
