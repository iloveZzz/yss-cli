import { onScopeDispose, shallowRef, watch } from 'vue'
import { withTimeout } from '@/lib/abort'
import { errorMessage, expired, type ProjectRecord } from '@/services/workbench'
import { loadProjectDirectoryStatus } from '@/services/workbench-read'

interface DirectoryCheck {
  rootPath: string
  available: boolean | null
  message: string | null
}

export function useProjectDirectoryChecks(projects: () => readonly ProjectRecord[]) {
  const results = shallowRef(new Map<string, DirectoryCheck>())
  const checking = shallowRef(false)
  let current: AbortController | undefined

  function directoryCheck(project: ProjectRecord) {
    const result = results.value.get(project.id)
    return result?.rootPath === project.rootPath ? result : undefined
  }

  async function read(project: ProjectRecord, owner: AbortController) {
    const deadline = withTimeout(owner.signal, 5000)
    let result: DirectoryCheck
    try {
      const value = await loadProjectDirectoryStatus(project.id, deadline.signal)
      result = value.rootPath === project.rootPath ? value : {
        rootPath: project.rootPath,
        available: null,
        message: '工程目录已变更，请刷新后核验'
      }
    } catch (cause) {
      result = {
        rootPath: project.rootPath,
        available: null,
        message: cause instanceof DOMException && cause.name === 'TimeoutError'
          ? '目录核验超时' : errorMessage(cause)
      }
    } finally {
      deadline.dispose()
    }
    if (!owner.signal.aborted && !expired.value)
      results.value = new Map(results.value).set(project.id, result)
  }

  async function refreshDirectories(mode: 'new' | 'failed' | 'all' = 'new') {
    current?.abort()
    if (expired.value) {
      results.value = new Map()
      checking.value = false
      return
    }
    const pending = projects().filter((project) => {
      const status = directoryCheck(project)
      return mode === 'all' || !status || (mode === 'failed' && status.available === null)
    })
    const queue = pending.values()
    const owner = new AbortController()
    current = owner
    checking.value = pending.length > 0
    const worker = async () => {
      for (const project of queue) {
        if (owner.signal.aborted || expired.value) return
        await read(project, owner)
      }
    }
    await Promise.all(Array.from({ length: Math.min(4, pending.length) }, worker))
    if (current === owner) {
      current = undefined
      checking.value = false
    }
  }

  watch([
    () => JSON.stringify(projects().map(({ id, rootPath }) => [id, rootPath])),
    expired
  ], () => void refreshDirectories(), { immediate: true })
  onScopeDispose(() => current?.abort())
  return { directoryCheck, refreshDirectories, checking }
}
