import { computed, onScopeDispose, shallowRef, watch } from 'vue'
import { expired } from '@/services/workbench'
import {
  getIssueDetail, getIssuePage, getIssueSources,
  type IssueDetail, type IssuePage, type IssueProblem, type IssueSources, type IssueState
} from '@/services/workbench-issues'
import { identityChanged, needsSourceRefresh, sourceChanged, transportIssue } from '../schemas/issues'

interface Reader {
  number: number
  result?: IssueDetail
  problem?: IssueProblem
}

export function useIssueBrowser() {
  const sources = shallowRef<IssueSources>()
  const sourceId = shallowRef('')
  const source = computed(() => sources.value?.sources.find(entry => entry.id === sourceId.value))
  const sourcesLoading = shallowRef(false)
  const sourcesProblem = shallowRef<IssueProblem>()
  const state = shallowRef<IssueState>('open')
  const query = shallowRef('')
  const visitedPages = shallowRef([1])
  const page = computed(() => visitedPages.value.at(-1) ?? 1)
  const pageData = shallowRef<IssuePage>()
  const pageProblem = shallowRef<IssueProblem>()
  const pageLoading = shallowRef(false)
  const unavailable = shallowRef<ReadonlySet<number>>(new Set())
  const withheldCount = computed(() => pageData.value?.items.filter(item => unavailable.value.has(item.number)).length ?? 0)
  const rows = computed(() => pageData.value?.items.filter(item => !unavailable.value.has(item.number)) ?? [])
  const reader = shallowRef<Reader>()
  const readAt = shallowRef<string>()
  const busy = computed(() => sourcesLoading.value || pageLoading.value || Boolean(reader.value && !reader.value.result && !reader.value.problem))
  let identity: string | undefined
  let sourcesRequest: AbortController | undefined
  let pageRequest: AbortController | undefined
  let detailRequest: AbortController | undefined

  function rememberIdentity(next?: string) {
    if (!next) return false
    const changed = identity !== undefined && next !== identity
    if (changed) unavailable.value = new Set()
    identity = next
    return changed
  }

  function markUnavailable(number: number, hidden: boolean) {
    const updated = new Set(unavailable.value)
    if (hidden) updated.add(number)
    else updated.delete(number)
    unavailable.value = updated
  }

  function cancelPage() {
    pageRequest?.abort()
    pageRequest = undefined
    pageLoading.value = false
  }

  function cancelDetail() {
    detailRequest?.abort()
    detailRequest = undefined
  }

  function failSource(problem: IssueProblem, preservePageRequest = false) {
    if (!preservePageRequest) cancelPage()
    cancelDetail()
    pageData.value = undefined
    pageProblem.value = problem
    readAt.value = undefined
    if (reader.value) reader.value = { number: reader.value.number, problem }
  }

  async function readPage(recheckUnavailable = false) {
    const id = source.value?.id
    if (!id || sourcesLoading.value || expired.value) return
    cancelPage()
    const request = new AbortController()
    pageRequest = request
    const current = () => pageRequest === request && !request.signal.aborted && !expired.value
    pageLoading.value = true
    pageProblem.value = undefined
    pageData.value = undefined
    try {
      const next = await getIssuePage({ sourceId: id, state: state.value, query: query.value, page: page.value }, request.signal)
      if (!current()) return
      const changed = rememberIdentity(next.identity)
      if ('issue' in next) {
        if (changed || next.issue.scope === 'source') failSource(changed ? identityChanged : next.issue, true)
        else pageProblem.value = next.issue
        return
      }
      if (changed && reader.value) {
        cancelDetail()
        reader.value = { number: reader.value.number, problem: identityChanged }
      }
      if (recheckUnavailable && unavailable.value.size) {
        for (const item of next.items.filter(entry => unavailable.value.has(entry.number))) {
          const checked = await getIssueDetail(id, item.number, request.signal)
          if (!current()) return
          if (rememberIdentity(checked.identity)) {
            failSource(identityChanged, true)
            return
          }
          if ('issue' in checked && checked.issue.scope === 'source') {
            failSource(checked.issue, true)
            return
          }
          if (!('issue' in checked)) markUnavailable(item.number, false)
        }
      }
      pageData.value = next
      readAt.value = new Date().toISOString()
    } catch (cause) {
      if (!current()) return
      const problem = transportIssue(cause)
      if (problem.scope === 'source') failSource(problem, true)
      else pageProblem.value = problem
    } finally {
      if (pageRequest === request) {
        pageRequest = undefined
        pageLoading.value = false
      }
    }
  }

  async function readDetail(number: number) {
    const id = source.value?.id
    if (!id || expired.value) return
    cancelDetail()
    const request = new AbortController()
    detailRequest = request
    const current = () => detailRequest === request && !request.signal.aborted && !expired.value
    reader.value = { number }
    try {
      const next = await getIssueDetail(id, number, request.signal)
      if (!current()) return
      if (rememberIdentity(next.identity)) {
        failSource(identityChanged)
        return
      }
      if ('issue' in next) {
        if (!next.identity || next.issue.scope === 'source') {
          failSource(next.issue)
          return
        }
        if (next.issue.scope === 'item') markUnavailable(number, true)
        reader.value = { number, problem: next.issue }
      } else {
        markUnavailable(number, false)
        reader.value = { number, result: next }
      }
    } catch (cause) {
      if (!current()) return
      failSource({ ...transportIssue(cause), scope: 'source' })
    } finally {
      if (detailRequest === request) detailRequest = undefined
    }
  }

  async function refreshSources() {
    if (expired.value || sourcesLoading.value) return
    const request = new AbortController()
    sourcesRequest = request
    const number = reader.value?.number
    cancelPage()
    cancelDetail()
    if (number !== undefined) reader.value = { number }
    sourcesLoading.value = true
    sourcesProblem.value = undefined
    pageData.value = undefined
    pageProblem.value = undefined
    readAt.value = undefined
    const current = () => sourcesRequest === request && !request.signal.aborted && !expired.value
    try {
      const next = await getIssueSources(request.signal)
      if (!current()) return
      sources.value = next
      if (!sourceId.value) sourceId.value = next.sources[0]?.id ?? ''
    } catch (cause) {
      if (!current()) return
      const problem = transportIssue(cause)
      sources.value = undefined
      sourcesProblem.value = problem
      if (reader.value) reader.value = { number: reader.value.number, problem }
      return
    } finally {
      if (sourcesRequest === request) {
        sourcesRequest = undefined
        sourcesLoading.value = false
      }
    }
    if (!source.value) {
      // A removed source can have a replacement with different contents. Require an explicit selection.
      if (sourceId.value) failSource(sourceChanged)
      return
    }
    await readPage(true)
    if (number === undefined || reader.value?.number !== number || reader.value.problem) return
    if (pageProblem.value) reader.value = { number, problem: pageProblem.value }
    else await readDetail(number)
  }

  function startCriteria() {
    visitedPages.value = [1]
    closeReader()
    void readPage()
  }

  function selectSource(id: string) {
    if (sourcesLoading.value || id === sourceId.value || !sources.value?.sources.some(entry => entry.id === id)) return
    sourceId.value = id
    identity = undefined
    unavailable.value = new Set()
    readAt.value = undefined
    startCriteria()
  }

  function selectState(value: IssueState) {
    if (value === state.value) return
    state.value = value
    startCriteria()
  }

  function search(value: string) {
    query.value = value.trim()
    startCriteria()
  }

  function previousPage() {
    if (visitedPages.value.length < 2 || pageLoading.value) return
    visitedPages.value = visitedPages.value.slice(0, -1)
    void readPage()
  }

  function nextPage() {
    const next = pageData.value?.nextPage
    if (next == null || next <= page.value || pageLoading.value) return
    visitedPages.value = [...visitedPages.value, next]
    void readPage()
  }

  function closeReader() {
    cancelDetail()
    reader.value = undefined
  }

  async function retryPage() {
    if (!source.value || (pageProblem.value && needsSourceRefresh(pageProblem.value))) await refreshSources()
    else await readPage(true)
  }

  async function retryDetail() {
    const target = reader.value
    if (!target?.problem) return
    if (!source.value || needsSourceRefresh(target.problem)) {
      await refreshSources()
    } else if (target.problem.scope === 'source') {
      reader.value = { number: target.number }
      await readPage()
      if (reader.value?.number !== target.number || reader.value.problem) return
      if (pageProblem.value) reader.value = { number: target.number, problem: pageProblem.value }
      else await readDetail(target.number)
    } else {
      await readDetail(target.number)
    }
  }

  watch(expired, value => {
    if (!value) {
      void refreshSources()
      return
    }
    sourcesRequest?.abort()
    sourcesRequest = undefined
    sourcesLoading.value = false
    cancelPage()
    closeReader()
    sources.value = undefined
    pageData.value = undefined
    identity = undefined
    unavailable.value = new Set()
  }, { immediate: true, flush: 'sync' })

  onScopeDispose(() => {
    sourcesRequest?.abort()
    cancelPage()
    cancelDetail()
  })

  return {
    sources, sourceId, source, sourcesLoading, sourcesProblem, state, query, page, visitedPages,
    pageData, pageProblem, pageLoading, rows, withheldCount, reader, busy, readAt, refreshSources,
    selectSource, selectState, search, previousPage, nextPage, openReader: readDetail, closeReader, retryPage, retryDetail
  }
}
