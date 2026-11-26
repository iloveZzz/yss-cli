import { computed, onScopeDispose, shallowRef, watch } from 'vue'
import { provideWorkbenchScope, useWorkbench } from './useWorkbench'
import { registerWorkbenchRouteReads, useWorkbenchCatalog } from './useWorkbenchReads'
import { useWorkbenchVerification } from './useWorkbenchVerification'
import { ApiError, errorMessage, type Snapshot } from '@/services/workbench'
import { loadProjectSummary, type ProjectSummary } from '@/services/workbench-read'

export function provideProjectDetailWorkbench(
  projectId: () => string | undefined,
  afterVerification?: () => Promise<unknown> | undefined
) {
  const { expired, sourceState, submit: submitCommand } = useWorkbench()
  const { verify } = useWorkbenchVerification()
  const { catalog, refreshCatalog } = useWorkbenchCatalog()
  const summary = shallowRef<ProjectSummary>()
  const loading = shallowRef(false)
  const error = shallowRef('')
  const missing = shallowRef(false)
  const refreshOwner = shallowRef<AbortController>()
  const refreshing = computed(() => refreshOwner.value !== undefined)
  const refreshError = shallowRef('')
  let pending: { controller: AbortController; result: Promise<boolean> } | undefined
  let disposed = false

  const catalogProject = computed(() => catalog.value?.projects.find((record) => record.id === projectId()))
  const catalogRevision = computed(() => catalogProject.value ? catalog.value?.revision : undefined)
  const project = computed(() => {
    if (missing.value) return undefined
    const current = summary.value?.project.record.id === projectId() ? summary.value : undefined
    const record = catalogProject.value
    if (!record || (current && current.revision >= (catalogRevision.value ?? -1))) return current?.project
    if (current?.project.record.rootPath === record.rootPath) return { ...current.project, record }
    return { record, available: false, message: null, sourceDirectories: [] }
  })
  const directoryKnown = computed(() => {
    const record = project.value?.record
    const readRecord = summary.value?.project.record
    return !!record && readRecord?.id === record.id && readRecord.rootPath === record.rootPath
  })
  const registeredCount = computed(() => catalog.value?.specs.filter((spec) => spec.projectId === projectId()).length)
  const snapshot = computed<Snapshot | null>(() => project.value ? {
    revision: Math.max(summary.value?.revision ?? 0, catalogRevision.value ?? 0),
    readAt: summary.value?.readAt ?? catalog.value?.readAt ?? '',
    projects: [project.value],
    progress: {},
    specs: [],
    history: [],
    tasks: [],
    stageRecords: [],
    savedViews: []
  } : null)

  function reloadProject(minimumRevision?: number): Promise<boolean> {
    const id = projectId()
    if (!id || expired.value || disposed) return Promise.resolve(false)
    if (pending) {
      if (minimumRevision === undefined) return pending.result
      return pending.result.then((ok) => projectId() === id &&
        (summary.value?.revision ?? -1) < minimumRevision ? reloadProject() : ok)
    }
    const controller = new AbortController()
    loading.value = true
    const result = loadProjectSummary(id, AbortSignal.any([controller.signal, AbortSignal.timeout(10_000)]))
      .then((value) => {
        if (controller.signal.aborted || projectId() !== id) return false
        summary.value = value
        missing.value = false
        error.value = ''
        return true
      })
      .catch((cause) => {
        if (controller.signal.aborted || projectId() !== id) return false
        if (cause instanceof ApiError && cause.status === 404) {
          summary.value = undefined
          missing.value = true
          error.value = ''
        } else {
          error.value = cause instanceof DOMException && cause.name === 'TimeoutError'
            ? '读取工程摘要超时，请重试。' : errorMessage(cause)
        }
        return false
      })
      .finally(() => {
        if (pending?.controller !== controller) return
        pending = undefined
        loading.value = false
      })
    pending = { controller, result }
    return result
  }

  function resetRequests() {
    pending?.controller.abort()
    pending = undefined
    loading.value = false
    refreshOwner.value?.abort()
    refreshOwner.value = undefined
  }
  watch(projectId, () => {
    resetRequests()
    summary.value = undefined
    missing.value = false
    error.value = ''
    refreshError.value = ''
    void reloadProject()
  }, { immediate: true })
  watch(() => catalogProject.value?.rootPath, (rootPath) => {
    if (rootPath && summary.value?.project.record.rootPath !== rootPath) void reloadProject(catalogRevision.value)
  })
  onScopeDispose(() => {
    disposed = true
    resetRequests()
  })

  async function refresh(): Promise<boolean> {
    if (!projectId() || expired.value || disposed || refreshing.value) return false
    const own = new AbortController()
    refreshOwner.value = own
    refreshError.value = ''
    const projectRead = reloadProject()
    try {
      await verify(own.signal, '工程 Spec 核验失败。')
      const summaryRead = await projectRead
      if (refreshOwner.value !== own || expired.value) return false
      await afterVerification?.()
      return summaryRead && refreshOwner.value === own && !expired.value
    } catch (cause) {
      if (refreshOwner.value === own) refreshError.value = cause instanceof DOMException && cause.name === 'TimeoutError'
        ? '请求核验超时，请重试。' : errorMessage(cause)
      return false
    } finally {
      own.abort()
      if (refreshOwner.value === own) refreshOwner.value = undefined
    }
  }

  registerWorkbenchRouteReads({
    busy: computed(() => loading.value || refreshing.value),
    readAt: computed(() => sourceState.value?.projectionVersion ? sourceState.value.readAt : undefined),
    refresh
  })
  const workbench = provideWorkbenchScope({
    snapshot,
    loading,
    error,
    refresh: reloadProject,
    submit(action, expectedRevision = snapshot.value?.revision, expectedTarget, refreshAfter) {
      if (expectedRevision === undefined) throw new Error('请先读取工程。')
      const id = projectId()
      return submitCommand(action, expectedRevision, expectedTarget, async (revision) => {
        if (disposed || projectId() !== id) return
        if (refreshAfter) await refreshAfter(revision)
        else await Promise.all([reloadProject(revision), refreshCatalog(revision)])
      })
    }
  })
  return { ...workbench, project, directoryKnown, registeredCount, reloadProject, refresh, refreshing, refreshError }
}
