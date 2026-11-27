import { computed, onScopeDispose, shallowRef, watch, type Ref } from 'vue'
import {
  getMergeRequestDetail,
  getMergeRequestPage,
  type MergeRequestDetail,
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
  requestKey,
  sortTimestamp,
  transportIssue,
  type MergeRequestRow,
  type MergeRequestTab
} from '../schemas/requests'

const pageSize = 8
const concurrentReads = 3
const readsPerAction = 18

interface VerifiedRead {
  identity: string
  items: Map<number, MergeRequestSummary>
  readAt: string
}

interface RefreshRead {
  tab: MergeRequestTab
  query: string
  order: MergeRequestOrder
  filterKey: string
  page: number
  scope: string
  previousReads: Map<string, VerifiedRead>
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

interface DetailRead {
  repository: MergeRequestRepository
  number: number
  result?: MergeRequestDetail
  issue?: MergeRequestIssue
}

export function useMergeRequestBrowser(
  repositories: Readonly<Ref<MergeRequestRepository[]>>,
  tab: Readonly<Ref<MergeRequestTab>>,
  query: Readonly<Ref<string>>,
  order: Readonly<Ref<MergeRequestOrder>>,
  filterKey: Readonly<Ref<string>>
) {
  const reads = shallowRef<RepositoryRead[]>([])
  const busy = shallowRef(false)
  const page = shallowRef(1)
  const detail = shallowRef<DetailRead>()
  let detailRequest: AbortController | undefined
  let revision = 0
  let startTimer: number | undefined
  let refreshRead: RefreshRead | undefined

  function publishReads() {
    reads.value = [...reads.value]
  }

  function resetRead(state: RepositoryRead) {
    state.request?.abort()
    state.request = undefined
    state.items = new Map()
    state.nextPage = 1
    state.frontier = Infinity
    state.identity = undefined
    state.issue = undefined
    state.readAt = undefined
    state.previous = undefined
  }

  function clearDetail() {
    detailRequest?.abort()
    detailRequest = undefined
    detail.value = undefined
  }

  function invalidateDetail(repositoryId: string, issue: MergeRequestIssue) {
    const target = detail.value
    if (target?.repository.id !== repositoryId) return
    detailRequest?.abort()
    detailRequest = undefined
    detail.value = { repository: target.repository, number: target.number, issue }
  }

  const numberQuery = computed(() => /^[#!]?\d+$/u.test(query.value))
  const pending = computed(() => reads.value.filter((read) => !read.issue && read.nextPage !== null))
  const issues = computed(() => reads.value.flatMap(({ repository, issue, readAt }) => issue ? [{ repository, issue, readAt }] : []))
  const readAt = computed(() => reads.value.reduce<string | undefined>((latest, state) =>
    state.readAt && (!latest || state.readAt > latest) ? state.readAt : latest, undefined))
  const complete = computed(() => !busy.value && !pending.value.length && !issues.value.length)
  const matching = computed(() => reads.value.flatMap(({ repository, items }) =>
    [...items.values()].map((request) => ({ repository, request }))
      .filter((row) => matchesQuery(row, query.value))))
  const counts = computed(() => {
    if (!reads.value.length || !complete.value) return undefined
    const result = { all: 0, open: 0, merged: 0, closed: 0 }
    let unknown = false
    for (const { request } of matching.value) {
      result.all++
      if (request.status === 'unknown') unknown = true
      else result[request.status]++
    }
    return unknown ? { all: result.all } : result as Partial<Record<MergeRequestTab, number>>
  })
  const tabMatches = computed(() => matching.value.filter(({ request }) =>
    tab.value === 'all' || request.status === tab.value))
  const stableRows = computed(() => {
    // Time ties are held until all unfinished sources have advanced past that time.
    // Otherwise a tied record on a remote page could shift an already shown local page.
    const frontier = pending.value.reduce((max, state) => Math.max(max, state.frontier), -Infinity)
    return tabMatches.value.filter(({ request }) =>
      !pending.value.length || sortTimestamp(request, order.value) > frontier)
      .sort((left, right) => compareRows(left, right, order.value))
  })
  const total = computed(() => stableRows.value.length)
  const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
  const rows = computed(() => stableRows.value.slice((page.value - 1) * pageSize, page.value * pageSize))

  function verifiedRead(state: RepositoryRead): VerifiedRead | undefined {
    if (state.identity && state.readAt) {
      return { identity: state.identity, items: new Map(state.items), readAt: state.readAt }
    }
  }

  function applyIssue(state: RepositoryRead, issue: MergeRequestIssue, identity?: string) {
    const previous = state.previous
    const canKeepCurrent = Boolean(identity && identity === state.identity && !losesAccess(issue))
    const canKeepPrevious = isTemporary(issue) && identity && identity === previous?.identity &&
      (!state.identity || canKeepCurrent)
    // Keep the broader verified snapshot intact when refresh fails partway through it.
    if (canKeepPrevious && previous && (!canKeepCurrent || previous.items.size > state.items.size)) {
      state.identity = previous.identity
      state.items = previous.items
      state.readAt = previous.readAt
    } else if (!canKeepCurrent) {
      if (state.identity || previous) page.value = 1
      resetRead(state)
      invalidateDetail(state.repository.id, issue)
    }
    state.previous = undefined
    state.issue = issue
    publishReads()
  }

  function restartWithNewIdentity(state: RepositoryRead) {
    resetRead(state)
    page.value = 1
    invalidateDetail(state.repository.id, { code: 'identityChanged', message: '已从第一页重新核对仓库。' })
    publishReads()
  }

  async function readPage(state: RepositoryRead, browserRevision: number) {
    const pageNumber = state.nextPage
    if (pageNumber === null || state.request) return
    const request = new AbortController()
    const readOrder = order.value
    state.request = request
    try {
      const result = await getMergeRequestPage(state.repository.id, pageNumber, readOrder, request.signal)
      if (browserRevision !== revision || state.request !== request || request.signal.aborted) return
      if ('issue' in result) {
        applyIssue(state, result.issue, result.identity)
        return
      }
      if (!result.identity) {
        applyIssue(state, { code: 'unavailable', message: '无法核对仓库的连接身份，可以重试。' })
        return
      }
      if (state.identity && state.identity !== result.identity) {
        restartWithNewIdentity(state)
        return
      }
      if (state.previous && state.previous.identity !== result.identity) {
        state.previous = undefined
        page.value = 1
      }
      state.identity = result.identity
      for (const item of result.items) {
        if (!state.items.has(item.number)) state.items.set(item.number, item)
        const stamp = sortTimestamp(item, readOrder)
        if (Number.isFinite(stamp)) state.frontier = Math.min(state.frontier, stamp)
      }
      state.readAt = result.readAt ?? state.readAt
      const exactMatch = numberQuery.value && result.items.some((item) =>
        matchesQuery({ repository: state.repository, request: item }, query.value))
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
      if (browserRevision === revision && state.request === request && !request.signal.aborted)
        applyIssue(state, transportIssue(cause))
    } finally {
      if (state.request === request) state.request = undefined
    }
  }

  async function readThrough(target: number) {
    const browserRevision = revision
    let remaining = readsPerAction
    busy.value = true
    try {
      while (browserRevision === revision && remaining > 0) {
        const available = pending.value.filter((state) => !state.request)
        if (!available.length || total.value >= target) break
        available.sort((left, right) => {
          if (left.frontier === right.frontier) return (left.nextPage ?? 0) - (right.nextPage ?? 0)
          return left.frontier > right.frontier ? -1 : 1
        })
        const batch = available.slice(0, Math.min(concurrentReads, remaining))
        remaining -= batch.length
        await Promise.all(batch.map((state) => readPage(state, browserRevision)))
      }
    } finally {
      if (browserRevision === revision) {
        page.value = Math.min(page.value, pages.value)
        busy.value = false
      }
    }
  }

  function cancelReads() {
    revision++
    window.clearTimeout(startTimer)
    startTimer = undefined
    for (const state of reads.value) state.request?.abort()
    clearDetail()
  }

  function repositoryScopeKey(scope: MergeRequestRepository[]) {
    return JSON.stringify(scope.map(({ id, url }) => [id, url]).sort())
  }

  function prepareRefresh() {
    refreshRead = {
      tab: tab.value,
      query: query.value,
      order: order.value,
      filterKey: filterKey.value,
      page: page.value,
      scope: repositoryScopeKey(repositories.value),
      previousReads: new Map(reads.value.flatMap((state) => {
        const previous = verifiedRead(state)
        return previous ? [[state.repository.id, previous] as const] : []
      }))
    }
  }

  function clearPreparedRefresh() {
    refreshRead = undefined
  }

  watch([repositories, tab, query, order, filterKey], ([scope, nextTab, search, nextOrder, nextFilters], previous) => {
    cancelReads()
    if (refreshRead && (refreshRead.tab !== nextTab || refreshRead.query !== search ||
      refreshRead.order !== nextOrder || refreshRead.filterKey !== nextFilters)) clearPreparedRefresh()
    const verifiedScope = refreshRead?.scope === repositoryScopeKey(scope) ? refreshRead : undefined
    page.value = verifiedScope?.page ?? 1
    reads.value = scope.map((repository) => ({
      repository, items: new Map(), nextPage: 1, frontier: Infinity,
      previous: verifiedScope?.previousReads.get(repository.id)
    }))
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
    if (!busy.value && pending.value.length) {
      // Even a partly filled page may be extended without moving its existing rows.
      await readThrough(total.value + pageSize)
    }
  }

  async function retry(repositoryId: string) {
    const state = reads.value.find((item) => item.repository.id === repositoryId)
    if (busy.value || !state?.issue) return
    const previous = verifiedRead(state)
    resetRead(state)
    state.previous = previous
    publishReads()
    await readThrough(page.value * pageSize)
  }

  function changePage(offset: number) {
    const destination = page.value + offset
    if (destination > 0 && destination <= pages.value) page.value = destination
  }

  async function openDetail(row: MergeRequestRow) {
    const state = reads.value.find((entry) => entry.repository.id === row.repository.id)
    const { repository, request } = row
    if (!state?.identity || !rows.value.some((item) => requestKey(item) === requestKey(row))) return
    detailRequest?.abort()
    const controller = new AbortController()
    detailRequest = controller
    const target = { repository, number: request.number }
    const browserRevision = revision
    const stillCurrent = () => !controller.signal.aborted &&
      detailRequest === controller && browserRevision === revision
    detail.value = target
    try {
      const result = await getMergeRequestDetail(repository.id, request.number, controller.signal)
      if (!stillCurrent()) return
      if ('issue' in result) {
        detail.value = { ...target, issue: result.issue }
        if (losesAccess(result.issue) || result.identity !== state.identity)
          applyIssue(state, result.issue, result.identity)
      } else if (!result.identity) {
        applyIssue(state, { code: 'unavailable', message: '无法核对仓库的连接身份，可以重试。' })
      } else if (result.identity !== state.identity) {
        restartWithNewIdentity(state)
        if (!busy.value) void readThrough(pageSize)
      } else if (result.number === target.number) {
        detail.value = { ...target, result }
      } else {
        detail.value = { ...target, issue: { code: 'unavailable', message: '平台返回的编号与所选请求不一致。' } }
      }
    } catch (cause) {
      if (stillCurrent()) {
        const issue = transportIssue(cause)
        applyIssue(state, issue)
      }
    } finally {
      if (detailRequest === controller) detailRequest = undefined
    }
  }

  const repositoryReads = computed(() => new Map(reads.value.map((state) => [state.repository.id, {
    issue: state.issue, readAt: state.readAt, pending: state.nextPage !== null && !state.issue
  }])))

  return {
    busy, page, pages, rows, total, counts, complete, issues, readAt, repositoryReads,
    hasMore: computed(() => pending.value.length > 0),
    hasKnownMatches: computed(() => tabMatches.value.length > 0),
    loaded: computed(() => reads.value.some((state) => Boolean(state.readAt))),
    prepareRefresh, clearPreparedRefresh, continueReading, changePage, retry, detail, openDetail, clearDetail
  }
}
