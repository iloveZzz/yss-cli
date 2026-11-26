import { computed, onScopeDispose, shallowRef, watch, type Ref } from 'vue'
import { useDebounceFn, useEventListener } from '@vueuse/core'
import { useWorkbench } from '@/hooks/useWorkbench'
import { ApiError, errorMessage } from '@/services/workbench'
import {
  getAgentRun, getAgentRounds, getAgentSessionPage,
  type AgentCiEvidence, type AgentRun, type AgentSessionPage
} from '@/services/workbench-agent'
import { getJob, terminalJob, type JobRecord } from '@/services/workbench-platform'

export function usePiWorkspace(specId: Readonly<Ref<string>>, sessionId: Readonly<Ref<string>>) {
  const { sourceState } = useWorkbench()
  const observedSession = computed(() => sourceState.value?.agentSessionVersions?.[specId.value])
  const observedSpec = computed(() => sourceState.value?.specVersions?.[specId.value])
  const observedSource = computed(() => sourceState.value?.sourceVersions?.[specId.value])
  const observedVerification = computed(() => sourceState.value?.verification)
  const context = shallowRef<AgentSessionPage>()
  const contextError = shallowRef('')
  const pageNotice = shallowRef('')
  const listBusy = shallowRef(false)
  const search = shallowRef('')
  const attentionOnly = shallowRef(false)
  const query = shallowRef('')
  const publishSearch = useDebounceFn((value: string) => { query.value = value }, 220)
  watch(search, (value) => {
    const trimmed = value.trim()
    if (trimmed) void publishSearch(trimmed)
    else {
      publishSearch.cancel()
      query.value = ''
    }
  })

  const run = shallowRef<AgentRun>()
  const rounds = shallowRef<JobRecord[]>([])
  const snapshotJobs = shallowRef(new Set<string>())
  const roundCiJobs = shallowRef<Record<string, AgentCiEvidence>>({})
  const runError = shallowRef('')
  const launchError = shallowRef('')
  const accepted = shallowRef<{ spec: string; session: string; job: JobRecord }>()
  const summary = computed(() => {
    const page = context.value
    const id = sessionId.value
    if (page?.spec.id !== specId.value || !id) return undefined
    return page.selectedSession?.id === id ? page.selectedSession : page.sessions.find((item) => item.id === id)
  })
  const latest = computed(() => rounds.value.at(-1))
  const reading = shallowRef(false)
  const listKey = computed(() => JSON.stringify([specId.value, sessionId.value, query.value, attentionOnly.value]))
  const currentListKey = shallowRef('')
  const listCurrent = computed(() => currentListKey.value === listKey.value)
  let contextRequest: AbortController | undefined
  let contextOwner = ''
  let contextFlight: Promise<void> | undefined
  let refreshAfterPage = false
  let roundRequest: AbortController | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  let disposed = false

  function refreshContext(append = false): Promise<void> {
    if (disposed) return Promise.resolve()
    const key = listKey.value
    if (contextFlight && contextOwner === key) {
      if (!append) refreshAfterPage = true
      return contextFlight
    }
    const spec = specId.value
    const previous = listCurrent.value ? context.value : undefined
    const cursor = append ? previous?.nextCursor : undefined
    if (append && !cursor) return Promise.resolve()
    contextRequest?.abort()
    const request = new AbortController()
    const projection = [observedSpec.value, observedSource.value, observedVerification.value].join('|')
    contextRequest = request
    contextOwner = key
    refreshAfterPage = false
    listBusy.value = true
    const filters = { search: query.value, attention: attentionOnly.value, runId: sessionId.value || undefined }
    const stillCurrent = () => !request.signal.aborted && listKey.value === key
    contextFlight = (async () => {
      try {
        let next: AgentSessionPage
        let join = !!cursor
        try {
          next = await getAgentSessionPage(spec, { ...filters, cursor: cursor ?? undefined }, request.signal)
          if (!stillCurrent()) return
          if (cursor && next.version !== previous?.version) throw new ApiError('会话列表已更新', 409)
        } catch (cause) {
          if (!cursor || !stillCurrent() || !(cause instanceof ApiError) || cause.status !== 409) throw cause
          next = await getAgentSessionPage(spec, filters, request.signal)
          join = false
          if (stillCurrent()) pageNotice.value = '会话列表已更新，已重新读取。'
        }
        if (!stillCurrent()) return
        const ids = new Set(previous?.sessions.map((item) => item.id))
        context.value = join && previous
          ? { ...next, sessions: [...previous.sessions, ...next.sessions.filter((item) => !ids.has(item.id))] }
          : next
        currentListKey.value = key
        contextError.value = ''
      } catch (cause) {
        if (stillCurrent()) contextError.value = errorMessage(cause)
      } finally {
        if (contextRequest === request) {
          contextFlight = undefined
          contextRequest = undefined
          listBusy.value = false
          if (refreshAfterPage && stillCurrent()) {
            refreshAfterPage = false
            if (observedSession.value !== context.value?.version ||
              projection !== [observedSpec.value, observedSource.value, observedVerification.value].join('|'))
              void refreshContext()
          }
        }
      }
    })()
    return contextFlight
  }

  async function refreshRounds() {
    const id = sessionId.value
    const spec = specId.value
    if (!id || disposed) return
    roundRequest?.abort()
    const request = new AbortController()
    roundRequest = request
    const tracked = accepted.value
    const pending = tracked?.session === id && tracked.spec === spec ? tracked.job : undefined
    const knownSummary = summary.value
    const jobId = pending?.id ?? knownSummary?.jobId
    const stillCurrent = () => !request.signal.aborted && id === sessionId.value && spec === specId.value
    reading.value = true
    try {
      const [nextRun, list] = await Promise.all([
        getAgentRun(id, request.signal),
        getAgentRounds(id, request.signal)
      ])
      if (!stillCurrent()) return
      if (nextRun.host !== 'pi' || nextRun.specId !== spec) {
        rounds.value = []
        snapshotJobs.value = new Set()
        roundCiJobs.value = {}
        run.value = undefined
        throw new Error('此 Pi 会话不属于当前 Spec')
      }
      run.value = nextRun
      rounds.value = list.rounds
      snapshotJobs.value = new Set(list.snapshotJobIds)
      roundCiJobs.value = list.roundCiJobs ?? (nextRun.ciJob ? { [nextRun.jobId]: nextRun.ciJob } : {})
      if (jobId && nextRun.jobId !== jobId) {
        const job = list.rounds.find((item) => item.id === jobId) ?? await getJob(jobId, request.signal)
        if (!stillCurrent()) return
        if (!list.rounds.some((item) => item.id === job.id)) rounds.value = [...list.rounds, job]
        launchError.value = terminalJob(job.status) ? launchFailure(job) : ''
      } else {
        if (accepted.value === tracked) accepted.value = undefined
        launchError.value = ''
      }
      runError.value = ''
    } catch (cause) {
      if (!stillCurrent()) return
      if (cause instanceof ApiError && cause.status === 404 && jobId) {
        try {
          const job = await getJob(jobId, request.signal)
          if (!stillCurrent()) return
          rounds.value = [job]
          launchError.value = terminalJob(job.status) ? launchFailure(job) : ''
          runError.value = ''
        } catch (error) {
          if (stillCurrent()) runError.value = errorMessage(error)
        }
      } else if (!(cause instanceof ApiError && cause.status === 404 && !context.value && !contextError.value)) {
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
    if (disposed || timer) return
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
    contextFlight = undefined
    context.value = {
      ...context.value,
      selectedSession: context.value.selectedSession?.id === id ? undefined : context.value.selectedSession,
      sessions: context.value.sessions.filter((session) => session.id !== id)
    }
  }

  watch(specId, () => {
    context.value = undefined
    contextError.value = ''
    pageNotice.value = ''
    search.value = ''
    attentionOnly.value = false
    publishSearch.cancel()
    query.value = ''
  }, { flush: 'sync' })
  watch(listKey, () => {
    pageNotice.value = ''
    void refreshContext()
  }, { immediate: true })
  watch(observedSession, (version) => {
    if (version !== undefined && version !== context.value?.version) void refreshContext()
  })
  watch([observedSpec, observedSource, observedVerification], () => void refreshContext())
  watch([specId, sessionId], ([spec, id]) => {
    roundRequest?.abort()
    run.value = undefined
    runError.value = ''
    launchError.value = ''
    reading.value = false
    snapshotJobs.value = new Set()
    roundCiJobs.value = {}
    rounds.value = id && accepted.value?.session === id && accepted.value.spec === spec ? [accepted.value.job] : []
    void refreshRounds()
  }, { immediate: true })
  watch([() => summary.value?.jobId, () => summary.value?.status, () => !!context.value], () => {
    if (sessionId.value) void refreshRounds()
  })
  useEventListener(window, 'focus', scheduleRefresh)
  onScopeDispose(() => {
    disposed = true
    publishSearch.cancel()
    contextRequest?.abort()
    roundRequest?.abort()
    clearTimeout(timer)
  })

  return {
    context, contextError, pageNotice, listBusy, listCurrent, search, attentionOnly,
    run, rounds, latest, summary, snapshotJobs, roundCiJobs, runError, launchError, reading,
    refresh, scheduleRefresh, recordAccepted, forgetSession, loadMore: () => refreshContext(true)
  }
}

function launchFailure(job: JobRecord) {
  return job.error?.message || (['canceled', 'cancelled'].includes(job.status)
    ? '已取消准备。' : '本轮准备未进入 Pi 会话，请查看执行记录。')
}
