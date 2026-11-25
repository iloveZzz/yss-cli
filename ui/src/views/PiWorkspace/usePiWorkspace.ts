import { computed, onScopeDispose, shallowRef, watch, type Ref } from 'vue'
import { useEventListener } from '@vueuse/core'
import { ApiError, errorMessage } from '@/services/workbench'
import {
  getAgentRun, getAgentRounds, getAgentWorkspace,
  type AgentRun, type AgentWorkspace
} from '@/services/workbench-agent'
import { getJob, terminalJob, type JobRecord } from '@/services/workbench-platform'

export function usePiWorkspace(specId: Readonly<Ref<string>>, sessionId: Readonly<Ref<string>>) {
  const context = shallowRef<AgentWorkspace>()
  const contextError = shallowRef('')
  const run = shallowRef<AgentRun>()
  const rounds = shallowRef<JobRecord[]>([])
  const snapshotJobs = shallowRef(new Set<string>())
  const runError = shallowRef('')
  const launchError = shallowRef('')
  const accepted = shallowRef<{ spec: string; session: string; job: JobRecord }>()
  const summary = computed(() => context.value?.sessions.find((item) => item.id === sessionId.value))
  const latest = computed(() => rounds.value.at(-1))
  const reading = shallowRef(false)
  let contextRequest: AbortController | undefined
  let roundRequest: AbortController | undefined
  let timer: ReturnType<typeof setTimeout> | undefined

  async function refreshContext() {
    contextRequest?.abort()
    const request = new AbortController()
    contextRequest = request
    try {
      const result = await getAgentWorkspace(specId.value, request.signal)
      if (request.signal.aborted) return
      context.value = result
      contextError.value = ''
    } catch (cause) {
      if (!request.signal.aborted) contextError.value = errorMessage(cause)
    }
  }

  async function refreshRounds() {
    const id = sessionId.value
    const spec = specId.value
    if (!id) return
    roundRequest?.abort()
    const request = new AbortController()
    roundRequest = request
    const tracked = accepted.value
    const pending = tracked?.session === id && tracked.spec === spec ? tracked.job : undefined
    reading.value = true
    try {
      const [nextRun, list] = await Promise.all([
        getAgentRun(id, request.signal),
        getAgentRounds(id, request.signal)
      ])
      if (request.signal.aborted || id !== sessionId.value || spec !== specId.value) return
      if (nextRun.host !== 'pi' || nextRun.specId !== spec) {
        rounds.value = []
        snapshotJobs.value = new Set()
        run.value = undefined
        throw new Error('此 Pi 会话不属于当前 Spec')
      }
      run.value = nextRun
      rounds.value = list.rounds
      snapshotJobs.value = new Set(list.snapshotJobIds)
      if (pending) {
        if (list.rounds.some((job) => job.id === pending.id)) {
          if (accepted.value === tracked) accepted.value = undefined
          launchError.value = ''
        } else {
          const job = await getJob(pending.id, request.signal)
          if (request.signal.aborted) return
          if (terminalJob(job.status)) {
            launchError.value = job.error?.message || '新执行未接入此会话，请查看执行记录。'
            if (accepted.value === tracked) accepted.value = undefined
          } else {
            rounds.value = [...list.rounds, job]
          }
        }
      }
      if (!request.signal.aborted) runError.value = ''
    } catch (cause) {
      if (request.signal.aborted) return
      if (cause instanceof ApiError && cause.status === 404 && pending) {
        try {
          const job = await getJob(pending.id, request.signal)
          if (request.signal.aborted) return
          rounds.value = [job]
          runError.value = terminalJob(job.status) && job.error ? job.error.message : ''
        } catch (error) {
          if (!request.signal.aborted) runError.value = errorMessage(error)
        }
      } else {
        runError.value = errorMessage(cause)
      }
    } finally {
      if (roundRequest === request) reading.value = false
    }
  }

  async function refresh() {
    await Promise.all([refreshContext(), refreshRounds()])
  }

  function scheduleRefresh() {
    if (timer) return
    timer = setTimeout(() => {
      timer = undefined
      void refresh()
    }, 160)
  }

  function recordAccepted(spec: string, session: string, job: JobRecord) {
    if (spec !== specId.value) return
    accepted.value = { spec, session, job }
    launchError.value = ''
    if (session === sessionId.value) {
      roundRequest?.abort()
      rounds.value = [...rounds.value.filter((round) => round.id !== job.id), job]
    }
    scheduleRefresh()
  }

  function forgetSession(spec: string, id: string) {
    if (accepted.value?.spec === spec && accepted.value.session === id) accepted.value = undefined
    if (context.value?.spec.id !== spec) return
    contextRequest?.abort()
    context.value = { ...context.value, sessions: context.value.sessions.filter((session) => session.id !== id) }
  }

  watch(specId, () => {
    context.value = undefined
    contextError.value = ''
    void refreshContext()
  }, { immediate: true })
  watch([specId, sessionId], ([spec, id]) => {
    roundRequest?.abort()
    run.value = undefined
    runError.value = ''
    launchError.value = ''
    reading.value = false
    snapshotJobs.value = new Set()
    rounds.value = id && accepted.value?.session === id && accepted.value.spec === spec ? [accepted.value.job] : []
    void refreshRounds()
  }, { immediate: true })
  useEventListener(window, 'focus', scheduleRefresh)
  onScopeDispose(() => {
    contextRequest?.abort()
    roundRequest?.abort()
    clearTimeout(timer)
  })

  return { context, contextError, run, rounds, latest, summary, snapshotJobs, runError, launchError, reading,
    refresh, scheduleRefresh, recordAccepted, forgetSession }
}
