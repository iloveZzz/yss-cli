import { computed, onScopeDispose, shallowRef, watch, type Ref } from 'vue'
import {
  getMergeRequestPage,
  type MergeRequestIssue,
  type MergeRequestOrder,
  type MergeRequestRepository,
  type MergeRequestSummary
} from '@/services/workbench-merge-requests'
import {
  compareRows,
  isTemporary,
  losesAccess,
  matchesQuery,
  requestNumberQuery,
  sortTimestamp,
  transportIssue,
  type MergeRequestRow,
  type MergeRequestTab
} from '../schemas/requests'

const pageSize = 8
const concurrentReads = 3
const readsPerAction = 18
const changedIdentity = { code: 'identityChanged', message: '平台连接身份已变化，正在重新核对该实例的仓库。' }

interface VerifiedRead {
  identity: string
  items: Map<number, MergeRequestSummary>
  readAt: string
}

interface RepositoryRead {
  repository: MergeRequestRepository
  items: Map<number, MergeRequestSummary>
  nextPage: number | null
  frontier: number
  identity?: string
  readAt?: string
  issue?: MergeRequestIssue
  previous?: VerifiedRead
  request?: AbortController
}

interface RetainedPage {
  rows: MergeRequestRow[]
  identities: Map<string, string>
}

function initialRead(repository: MergeRequestRepository, previous?: VerifiedRead): RepositoryRead {
  return { repository, items: new Map(), nextPage: 1, frontier: Infinity, previous }
}

function instanceKey({ platform, origin }: MergeRequestRepository) {
  return `${platform}\n${origin}`
}

function connectionUnavailable(issue: MergeRequestIssue) {
  const code = issue.code.replace(/[-_]/g, '').toLowerCase()
  return ['authentication', 'unauthorized', 'missingtool', 'toolmissing'].includes(code)
}

function verifiedRead(state: RepositoryRead): VerifiedRead | undefined {
  const previous = state.previous
  if (!state.identity || !state.readAt) return previous
  if (previous?.identity === state.identity && previous.items.size > state.items.size) return previous
  return { identity: state.identity, items: new Map(state.items), readAt: state.readAt }
}

export function useMergeRequestBrowser(
  repositories: Readonly<Ref<MergeRequestRepository[]>>,
  tab: Readonly<Ref<MergeRequestTab>>,
  query: Readonly<Ref<string>>,
  order: Readonly<Ref<MergeRequestOrder>>,
  filterKey: Readonly<Ref<string>>,
  callbacks: {
    contextChanged: () => void
    repositoryInvalidated: (repositoryId: string, issue: MergeRequestIssue) => void
  }
) {
  const reads = shallowRef<RepositoryRead[]>([])
  const busy = shallowRef(false)
  const page = shallowRef(1)
  const contextVersion = shallowRef(0)
  const retained = shallowRef<RetainedPage>()
  const instanceIdentities = new Map<string, string>()
  let revision = 0
  let startTimer: number | undefined
  let contextKey: string | undefined
  let refreshPrepared = false
  let scopedReads: Set<string> | undefined

  const exact = computed(() => requestNumberQuery(query.value))
  const pending = computed(() => reads.value.filter((state) => !state.issue && state.nextPage !== null))
  const issues = computed(() => reads.value.flatMap(({ repository, issue, readAt }) => issue ? [{ repository, issue, readAt }] : []))
  const readAt = computed(() => reads.value.reduce<string | undefined>((latest, state) => {
    const stamp = state.readAt ?? state.previous?.readAt
    return stamp && (!latest || stamp > latest) ? stamp : latest
  }, undefined))
  const complete = computed(() => !busy.value && !pending.value.length && !issues.value.length)
  const matching = computed(() => reads.value.flatMap(({ repository, items }) =>
    [...items.values()].map((request) => ({ repository, request }))
      .filter((row) => matchesQuery(row, query.value, exact.value))))
  const unknownStatus = computed(() => matching.value.some(({ request }) => request.status === 'unknown'))
  const counts = computed<Partial<Record<MergeRequestTab, number>> | undefined>(() => {
    if (!reads.value.length || !complete.value) return undefined
    if (tab.value !== 'all') return unknownStatus.value ? undefined : { [tab.value]: sortedMatches.value.length }

    if (unknownStatus.value) return { all: matching.value.length }
    const result = { all: matching.value.length, open: 0, merged: 0, closed: 0 }
    for (const { request } of matching.value) {
      if (request.status !== 'unknown') result[request.status]++
    }
    return result
  })

  const sortedMatches = computed(() => matching.value.filter(({ request }) =>
    tab.value === 'all' || request.status === tab.value)
    .sort((left, right) => compareRows(left, right, order.value)))
  const stableRows = computed(() => {
    if (!pending.value.length) return sortedMatches.value
    // A pending source can still supply a record with the same timestamp as its last page.
    const frontier = pending.value.reduce((max, state) => Math.max(max, state.frontier), -Infinity)
    return sortedMatches.value.filter(({ request }) => sortTimestamp(request, order.value) > frontier)
  })

  const byRepository = computed(() => new Map(reads.value.map((state) => [state.repository.id, state])))
  function identityFor(repositoryId: string) {
    const state = byRepository.value.get(repositoryId)
    return state?.identity ?? state?.previous?.identity
  }
  const displayed = computed(() => {
    const snapshot = retained.value
    if (!snapshot) return stableRows.value
    return snapshot.rows.filter(({ repository, request }) => {
      const state = byRepository.value.get(repository.id)
      const current = state?.items.get(request.number)
      const settled = state?.nextPage === null || state?.issue
      return !!state && identityFor(repository.id) === snapshot.identities.get(repository.id) &&
        (!settled || current !== undefined) &&
        (!current || ((tab.value === 'all' || current.status === tab.value) &&
          matchesQuery({ repository, request: current }, query.value, exact.value)))
    })
  })
  const total = computed(() => displayed.value.length)
  const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
  const rows = computed(() => displayed.value.slice((page.value - 1) * pageSize, page.value * pageSize))

  function retainDisplayed() {
    const identities = new Map<string, string>()
    for (const state of reads.value) {
      const identity = identityFor(state.repository.id)
      if (identity) identities.set(state.repository.id, identity)
    }
    retained.value = total.value ? { rows: displayed.value, identities } : undefined
  }

  function publishReads() {
    reads.value = [...reads.value]
    const snapshot = retained.value
    const retainedTarget = snapshot && Math.min(snapshot.rows.length, page.value * pageSize)
    if (retainedTarget && (stableRows.value.length >= retainedTarget || !pending.value.length)) retained.value = undefined
    if (!busy.value || !pending.value.length) page.value = Math.min(page.value, pages.value)
  }

  function resetRepositoryRead(state: RepositoryRead, issue?: MergeRequestIssue, keepRequest = false) {
    if (!keepRequest) state.request?.abort()
    Object.assign(state, {
      ...initialRead(state.repository),
      nextPage: issue ? null : 1,
      request: keepRequest ? state.request : undefined,
      identity: undefined,
      issue,
      readAt: undefined
    })
  }

  function dropRepositoryRead(state: RepositoryRead, issue: MergeRequestIssue) {
    resetRepositoryRead(state, issue)
    publishReads()
    page.value = Math.min(page.value, pages.value)
    callbacks.repositoryInvalidated(state.repository.id, issue)
  }

  function observeIdentity(state: RepositoryRead, identity: string) {
    const key = instanceKey(state.repository)
    const previous = instanceIdentities.get(key)
    instanceIdentities.set(key, identity)
    return previous !== undefined && previous !== identity
  }

  function resetInstanceReads(
    repository: MergeRequestRepository,
    issue: MergeRequestIssue,
    restart: boolean,
    current?: RepositoryRead
  ) {
    const key = instanceKey(repository)
    const affected = reads.value.filter((state) => instanceKey(state.repository) === key)
    // The positions of rows from other instances were already verified in the outgoing result.
    if (restart && !retained.value) retainDisplayed()
    for (const state of affected) resetRepositoryRead(state, restart ? undefined : issue, state === current)
    const snapshot = retained.value
    if (snapshot) {
      const remaining = snapshot.rows.filter((row) => instanceKey(row.repository) !== key)
      retained.value = remaining.length ? { ...snapshot, rows: remaining } : undefined
    }
    publishReads()
    page.value = Math.min(page.value, pages.value)
    for (const state of affected) callbacks.repositoryInvalidated(state.repository.id, issue)

    if (!restart) return
    const ids = affected.map(({ repository }) => repository.id)
    if (busy.value) {
      for (const id of ids) scopedReads?.add(id)
    } else {
      void readThrough(page.value * pageSize, new Set(ids))
    }
  }

  function applyInstanceFailure(state: RepositoryRead, issue: MergeRequestIssue, identity?: string) {
    if (identity) {
      // An authentication failure with a verified identity reports a change during the server read.
      const firstObservation = !instanceIdentities.has(instanceKey(state.repository))
      if (observeIdentity(state, identity) || (firstObservation && issue.code === 'authentication')) {
        resetInstanceReads(state.repository, changedIdentity, true)
        return true
      }
    }
    if (!identity && connectionUnavailable(issue)) {
      resetInstanceReads(state.repository, issue, false)
      return true
    }
    return false
  }

  function invalidateRepository(repositoryId: string, issue: MergeRequestIssue, identity?: string) {
    const state = byRepository.value.get(repositoryId)
    if (state && !applyInstanceFailure(state, issue, identity)) dropRepositoryRead(state, issue)
  }

  function applyIssue(state: RepositoryRead, issue: MergeRequestIssue, identity?: string) {
    if (applyInstanceFailure(state, issue, identity)) return
    const knownIdentity = identityFor(state.repository.id)
    if (!identity || identity !== knownIdentity || losesAccess(issue)) {
      dropRepositoryRead(state, issue)
      return
    }
    const previous = state.previous
    state.identity = identity
    if (isTemporary(issue) && previous?.identity === identity && previous.items.size >= state.items.size) {
      state.items = previous.items
      state.readAt = previous.readAt
    }
    state.previous = undefined
    state.issue = issue
    publishReads()
  }

  async function readPage(state: RepositoryRead, browserRevision: number) {
    const pageNumber = state.nextPage
    if (pageNumber === null || state.request) return
    const request = new AbortController()
    const readOrder = order.value
    const readNumber = exact.value?.number
    state.request = request
    const current = () => browserRevision === revision && state.request === request && !request.signal.aborted
    try {
      const result = await getMergeRequestPage(state.repository.id, pageNumber, readOrder, request.signal, {
        status: tab.value, number: readNumber
      })
      if (!current()) return
      if ('issue' in result) {
        applyIssue(state, result.issue, result.identity)
        return
      }
      if (!result.identity) {
        applyIssue(state, { code: 'unavailable', message: '无法核对仓库的连接身份，可以重试。' })
        return
      }
      if (observeIdentity(state, result.identity)) {
        resetInstanceReads(state.repository, changedIdentity, true, pageNumber === 1 ? state : undefined)
        if (pageNumber !== 1) return
      }
      state.identity = result.identity
      for (const item of result.items) {
        if (!state.items.has(item.number)) state.items.set(item.number, item)
        const stamp = sortTimestamp(item, readOrder)
        if (Number.isFinite(stamp)) state.frontier = Math.min(state.frontier, stamp)
      }
      state.readAt = result.readAt ?? state.readAt
      const exactMatch = readNumber !== undefined && state.items.has(readNumber)
      state.nextPage = exactMatch ? null : result.nextPage
      if (!exactMatch && result.continuationIssue) {
        applyIssue(state, result.continuationIssue, result.identity)
        return
      }
      if (state.nextPage !== null && (!Number.isSafeInteger(state.nextPage) || state.nextPage <= pageNumber)) {
        applyIssue(state, { code: 'limit', message: '平台未返回有效的后续页。' }, result.identity)
        return
      }
      if (state.nextPage === null) state.previous = undefined
      publishReads()
    } catch (cause) {
      if (current()) applyIssue(state, transportIssue(cause))
    } finally {
      if (state.request === request) state.request = undefined
    }
  }

  function nextRead(target: number, only?: ReadonlySet<string>) {
    const cutoffRow = sortedMatches.value[target - 1]
    const cutoff = cutoffRow ? sortTimestamp(cutoffRow.request, order.value) : -Infinity
    let next: RepositoryRead | undefined
    for (const state of pending.value) {
      if (state.request || (only && !only.has(state.repository.id)) || state.frontier < cutoff) continue
      if (!next || state.frontier > next.frontier ||
        (state.frontier === next.frontier && (state.nextPage ?? 0) < (next.nextPage ?? 0))) next = state
    }
    return next
  }

  async function readThrough(target: number, only?: Set<string>) {
    const browserRevision = revision
    const running = new Set<Promise<void>>()
    let remaining = readsPerAction
    scopedReads = only
    busy.value = true
    try {
      while (browserRevision === revision) {
        while (remaining > 0 && running.size < concurrentReads) {
          const next = nextRead(target, only)
          if (!next) break
          remaining--
          const request = readPage(next, browserRevision).finally(() => running.delete(request))
          running.add(request)
        }
        if (!running.size) break
        await Promise.race(running)
      }
    } finally {
      if (browserRevision === revision) {
        scopedReads = undefined
        busy.value = false
        publishReads()
      }
    }
  }

  function cancelReads() {
    revision++
    scopedReads = undefined
    window.clearTimeout(startTimer)
    startTimer = undefined
    for (const state of reads.value) state.request?.abort()
  }

  function prepareRefresh() {
    refreshPrepared = true
  }

  function clearPreparedRefresh() {
    refreshPrepared = false
  }

  watch([repositories, tab, query, order, filterKey], ([scope, nextTab, search, nextOrder, nextFilters], previous) => {
    const sourceKeys = scope.map(({ id, url, platform, origin }) => [id, url, platform, origin]).sort()
    const nextKey = JSON.stringify([nextTab, search, nextOrder, nextFilters, sourceKeys])
    const changed = contextKey !== nextKey
    if (!changed && !refreshPrepared) return
    const keepSnapshot = !changed && refreshPrepared
    clearPreparedRefresh()
    cancelReads()

    if (keepSnapshot) retainDisplayed()
    else {
      if (contextKey !== undefined) callbacks.contextChanged()
      contextVersion.value++
      retained.value = undefined
      page.value = 1
    }
    contextKey = nextKey
    const previousReads = byRepository.value
    reads.value = scope.map((repository) => {
      const oldRead = previousReads.get(repository.id)
      return initialRead(repository, keepSnapshot && oldRead ? verifiedRead(oldRead) : undefined)
    })
    busy.value = scope.length > 0
    if (scope.length) {
      const target = page.value * pageSize
      const delay = previous?.[2] !== undefined && previous[2] !== search ? 220 : 0
      startTimer = window.setTimeout(() => {
        startTimer = undefined
        void readThrough(target)
      }, delay)
    }
  }, { immediate: true, flush: 'sync' })

  onScopeDispose(cancelReads)

  async function continueReading() {
    if (!busy.value && pending.value.length) await readThrough(Math.max(total.value, page.value * pageSize) + pageSize)
  }

  async function retry(repositoryId: string) {
    const state = byRepository.value.get(repositoryId)
    if (busy.value || !state?.issue) return
    retainDisplayed()
    const previous = verifiedRead(state)
    const target = page.value * pageSize
    const next = initialRead(state.repository, previous)
    next.identity = state.identity
    reads.value = reads.value.map((item) => item === state ? next : item)
    await readThrough(target, new Set([repositoryId]))
  }

  function changePage(offset: number) {
    const destination = page.value + offset
    if (destination > 0 && destination <= pages.value) page.value = destination
  }

  const repositoryReads = computed(() => new Map(reads.value.map((state) => [state.repository.id, {
    issue: state.issue, readAt: state.readAt ?? state.previous?.readAt, pending: state.nextPage !== null && !state.issue
  }])))

  return {
    busy, page, pages, rows, total, counts, complete, issues, readAt, repositoryReads, unknownStatus, contextVersion,
    checkedSources: computed(() => reads.value.filter((state) => Boolean(state.readAt) && !state.issue).length),
    showingPrevious: computed(() => !!retained.value),
    hasMore: computed(() => pending.value.length > 0),
    hasKnownMatches: computed(() => Boolean(sortedMatches.value.length)),
    loaded: computed(() => Boolean(readAt.value)),
    prepareRefresh, clearPreparedRefresh, continueReading, changePage, retry, identityFor, invalidateRepository
  }
}
