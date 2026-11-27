import { onScopeDispose, shallowRef, watch, type Ref } from 'vue'
import { errorMessage } from '@/services/workbench'
import { getAgentProjects, type AgentProjectCandidate, type AgentProjects } from '@/services/workbench-agent'

export interface PiCheckout {
  rootPath: string
  branch: string | null
  head: string
  repositoryKey?: string | null
}
export function projectCheckout(project?: AgentProjectCandidate): PiCheckout | undefined {
  if (project?.status !== 'ready' || !project.head) return undefined
  const { rootPath, branch, head, repositoryKey } = project
  return { rootPath, branch, head, repositoryKey }
}

export function usePiProjects(specId: Readonly<Ref<string>>) {
  const projectScope = shallowRef<AgentProjects>()
  const projectBusy = shallowRef(false)
  const projectError = shallowRef('')
  let request: AbortController | undefined

  async function refreshProjects() {
    request?.abort()
    const current = new AbortController()
    request = current
    projectBusy.value = true
    projectError.value = ''
    try {
      const result = await getAgentProjects(specId.value, current.signal)
      if (!current.signal.aborted) {
        projectScope.value = result
        return result
      }
    } catch (cause) {
      if (!current.signal.aborted) projectError.value = errorMessage(cause)
    } finally {
      if (request === current) {
        request = undefined
        projectBusy.value = false
      }
    }
  }
  function invalidateProjects() {
    projectScope.value = undefined
    return refreshProjects()
  }
  watch(specId, () => {
    request?.abort()
    request = undefined
    projectBusy.value = false
    projectScope.value = undefined
    projectError.value = ''
  }, { flush: 'sync' })
  onScopeDispose(() => request?.abort())
  return { projectScope, projectBusy, projectError, refreshProjects, invalidateProjects }
}
