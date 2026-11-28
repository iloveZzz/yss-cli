import { computed, inject, provide, shallowReadonly, shallowRef, type InjectionKey, type Ref } from 'vue'
import {
  ApiError,
  errorMessage,
  executeCommand,
  expired,
  loadSnapshot,
  type Action,
  type ConfirmationRecord,
  type Snapshot
} from '@/services/workbench'
import { getSourceState, readNdjson, type SourceState } from '@/services/workbench-platform'

const sourceState = shallowRef<SourceState>()
const changesError = shallowRef('')
const sourceVersions = computed(() => sourceState.value?.sourceVersions ?? {})
const externalVersion = computed(() => sourceState.value?.externalVersion ?? '')
const contentReceipts = shallowRef(new Map<string, { revision: number; confirmation: ConfirmationRecord }>())
let changesController: AbortController | undefined
let changesConnected = false
let reconnectTimer: ReturnType<typeof setTimeout> | undefined
let reconnectDelay = 500

const snapshot = shallowRef<Snapshot | null>(null)
const loading = shallowRef(false)
const saving = shallowRef(false)
const error = shallowRef('')
let pendingSnapshot: Promise<boolean> | undefined
let snapshotRequired = false
let observed = 0
let loadedAt = -1

function pendingContentConfirmation(specId: string, revision: number) {
  const receipt = contentReceipts.value.get(specId)
  return receipt && revision < receipt.revision ? receipt.confirmation : undefined
}

function contentConfirmationPending(revision: number, includes?: (specId: string) => boolean) {
  return [...contentReceipts.value].some(([id, receipt]) => receipt.revision > revision && (!includes || includes(id)))
}

function refresh(): Promise<boolean> {
  if (expired.value) return Promise.resolve(false)
  if (pendingSnapshot) return pendingSnapshot
  const observing = observed
  loading.value = true
  pendingSnapshot = loadSnapshot()
    .then((next) => {
      snapshot.value = next
      loadedAt = observing
      error.value = ''
      return true
    })
    .catch((cause) => {
      error.value = errorMessage(cause)
      return false
    })
    .finally(() => {
      pendingSnapshot = undefined
      loading.value = false
      if (snapshotRequired && loadedAt === observing && observed > loadedAt) void refresh()
    })
  return pendingSnapshot
}

function requireSnapshot(required: boolean) {
  snapshotRequired = required
  if (required && (!snapshot.value || loadedAt !== observed)) void refresh()
}

async function submit(
  action: Action,
  expectedRevision = snapshot.value?.revision,
  expectedTarget?: string,
  refreshAfter?: (revision?: number) => Promise<unknown>
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
    if (result.confirmation && result.revision >= (contentReceipts.value.get(result.confirmation.specId)?.revision ?? 0)) {
      contentReceipts.value = new Map(contentReceipts.value).set(result.confirmation.specId, {
        revision: result.revision,
        confirmation: result.confirmation
      })
    }
    if (refreshAfter) await refreshAfter(result.revision)
    else if (await refresh() && (snapshot.value?.revision ?? 0) < result.revision) await refresh()
    return result
  } catch (cause) {
    if (cause instanceof ApiError && cause.status === 409) {
      if (refreshAfter) await refreshAfter()
      else await refresh()
    }
    throw cause
  } finally {
    saving.value = false
  }
}

function applySourceState(value: SourceState) {
  const previous = sourceState.value
  // A queued focus request can resolve after the stream delivers a newer change.
  if (previous && value.changeSequence !== undefined && previous.changeSequence !== undefined &&
    value.changeSequence < previous.changeSequence) return
  sourceState.value = value
  changesError.value = ''
  const revisionChanged = previous ? value.revision !== previous.revision : value.revision !== snapshot.value?.revision
  if (revisionChanged ||
    (previous && (
      JSON.stringify(previous.sourceVersions) !== JSON.stringify(value.sourceVersions) ||
      JSON.stringify(previous.progressVersions) !== JSON.stringify(value.progressVersions) ||
      previous.inputVersions !== value.inputVersions
    ))) {
    observed++
    if (snapshotRequired) void refresh()
  }
}

async function checkChanges(signal?: AbortSignal, allowConnected = false) {
  if (expired.value || (changesConnected && !allowConnected) || signal?.aborted) return
  try {
    const state = await getSourceState(signal)
    if (signal?.aborted) return
    applySourceState(state)
    if (snapshotRequired && loadedAt !== observed) await refresh()
  } catch (cause) {
    if (!signal?.aborted) changesError.value = errorMessage(cause)
  }
}

function startChanges() {
  if (changesController || expired.value) return
  const own = new AbortController()
  changesController = own
  void readNdjson<SourceState>('/api/changes', own.signal, (value) => {
    if (own.signal.aborted) return
    changesConnected = true
    reconnectDelay = 500
    applySourceState(value)
    if (snapshotRequired && loadedAt !== observed && !pendingSnapshot) void refresh()
  })
    .catch((cause) => {
      if (!own.signal.aborted) changesError.value = errorMessage(cause)
    })
    .finally(() => {
      if (changesController !== own) return
      changesController = undefined
      changesConnected = false
      if (!own.signal.aborted && !expired.value) {
        changesError.value ||= '实时状态连接中断，正在重连。'
        reconnectTimer = setTimeout(startChanges, reconnectDelay)
        reconnectDelay = Math.min(reconnectDelay * 2, 5000)
      }
    })
}

function stopChanges() {
  clearTimeout(reconnectTimer)
  changesController?.abort()
  changesController = undefined
  changesConnected = false
}

const activeSpecs = computed(
  () =>
    snapshot.value?.specs.filter(
      (s) =>
        s.record.active &&
        snapshot.value?.projects.some((p) => p.record.id === s.record.projectId && p.record.active)
    ) ?? []
)
const workbench = {
  snapshot: shallowReadonly(snapshot),
  loading: shallowReadonly(loading),
  saving: shallowReadonly(saving),
  error: shallowReadonly(error),
  expired: shallowReadonly(expired),
  activeSpecs,
  sourceState: shallowReadonly(sourceState),
  changesError: shallowReadonly(changesError),
  sourceVersions,
  externalVersion,
  pendingContentConfirmation,
  contentConfirmationPending,
  startChanges,
  stopChanges,
  checkChanges,
  requireSnapshot,
  refresh,
  submit
}

interface WorkbenchScope {
  snapshot: Readonly<Ref<Snapshot | null>>
  loading: Readonly<Ref<boolean>>
  error: Readonly<Ref<string>>
  refresh: () => Promise<boolean>
  submit: typeof submit
}

const workbenchScopeKey: InjectionKey<WorkbenchScope> = Symbol('scoped workbench')

export function provideWorkbenchScope(scope: WorkbenchScope) {
  provide(workbenchScopeKey, scope)
  return { ...workbench, ...scope }
}

export function useWorkbench() {
  return { ...workbench, ...inject(workbenchScopeKey, undefined) }
}
