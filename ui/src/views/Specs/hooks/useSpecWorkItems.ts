import { computed, onScopeDispose, shallowRef, watch, type Ref } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'
import { ApiError, errorMessage, type GroupBy, type Layout, type WorkQuery } from '@/services/workbench'
import {
  loadCompactWorkPage,
  requestWorkbenchReadRefresh,
  type CompactWorkPage
} from '@/services/workbench-read'

const listPageSize = 50
const boardPageSize = 20
type Display = { list: number; columns: Record<string, number> }
interface View {
  key: string
  query: WorkQuery
  layout: Layout
  groupBy: GroupBy
}
interface Settled extends View {
  page: CompactWorkPage
  scope?: string
}
const cache = new Map<string, Settled>()
const depths = new Map<string, Display>()

export function useSpecWorkItems(
  query: Readonly<Ref<WorkQuery>>,
  layout: Readonly<Ref<Layout>>,
  groupBy: Readonly<Ref<GroupBy>>
) {
  const { expired, sourceState } = useWorkbench()
  const { catalog } = useWorkbenchCatalog()
  const view = computed<View>(() => ({
    key: JSON.stringify([query.value, layout.value, layout.value === 'board' ? groupBy.value : null]),
    query: query.value,
    layout: layout.value,
    groupBy: groupBy.value
  }))
  const settled = shallowRef<Settled>()
  const result = computed(() => settled.value?.layout === layout.value &&
    (layout.value === 'list' || settled.value.groupBy === groupBy.value) ? settled.value.page : undefined)
  const resultsCurrent = computed(() => settled.value?.key === view.value.key)
  const resultProgressBasis = computed(() => settled.value?.query.progressBasis ?? query.value.progressBasis)
  const scope = computed(() => {
    const versions = sourceState.value?.specVersions
    if (!versions) return undefined
    const { projectIds, specIds } = query.value
    const projects = new Map(catalog.value?.specs.map((item) => [item.id, item.projectId]))
    return JSON.stringify(Object.entries(versions).filter(([id]) => {
      const project = projects.get(id)
      return (!specIds.length || specIds.includes(id)) &&
        (!projectIds.length || !project || projectIds.includes(project))
    }))
  })
  const pending = shallowRef<'replace' | 'append'>()
  const loading = computed(() => !!pending.value)
  const loadingMore = computed(() => pending.value === 'append')
  const error = shallowRef('')
  const observedProjection = computed(() => sourceState.value?.projectionVersion)
  const verification = computed(() => {
    const observed = sourceState.value
    const page = result.value
    if (!observed?.verification || !page ||
      (observed.changeSequence ?? 0) < (page.changeSequence ?? 0))
      return page?.verification ?? observed?.verification
    return observed.verification
  })
  const verificationError = computed(() => sourceState.value?.verificationError ?? result.value?.verificationError)
  const writable = computed(() =>
    resultsCurrent.value && !!result.value?.projectionVersion && verification.value === 'current' &&
    !expired.value && !error.value && pending.value !== 'replace'
  )
  let controller: AbortController | undefined
  let ownerKey: string | undefined
  let invalidatedWhileReading = false
  let disposed = false

  function display(key: string) {
    let value = depths.get(key)
    if (!value) {
      value = { list: listPageSize, columns: {} }
      depths.set(key, value)
      if (depths.size > 20) depths.delete(depths.keys().next().value!)
    }
    return value
  }

  function save(current: View, page: CompactWorkPage) {
    const record = { ...current, page, scope: page.projectionVersion === observedProjection.value ? scope.value : undefined }
    settled.value = record
    if (!page.projectionVersion) return
    cache.delete(current.key)
    cache.set(current.key, record)
    if (cache.size > 12) cache.delete(cache.keys().next().value!)
  }

  async function replacement(current: View, signal: AbortSignal): Promise<CompactWorkPage> {
    const shown = display(current.key)
    const board = current.layout === 'board' ? { groupBy: current.groupBy } : undefined
    const first = await loadCompactWorkPage(current.query, {
      limit: board ? boardPageSize : Math.min(200, shown.list), board
    }, signal)
    const version = first.projectionVersion
    if (!version) return first

    async function remainder(target: number, start: number, column?: string) {
      const items: CompactWorkPage['items'] = []
      while (start + items.length < target && !signal.aborted) {
        const next = await loadCompactWorkPage(current.query, {
          offset: start + items.length,
          limit: Math.min(200, target - start - items.length),
          expectedProjectionVersion: version!,
          expectedQueryVersion: first.queryVersion ?? undefined,
          board: board && { ...board, column }
        }, signal)
        if (next.projectionVersion !== version || next.queryVersion !== first.queryVersion)
          throw new ApiError('查询期间进度已更新，请重试。', 409)
        items.push(...next.items)
        if (!next.items.length) break
      }
      return items
    }
    const extra = board
      ? await Promise.all(Object.entries(shown.columns).map(([column, limit]) =>
          remainder(Math.min(limit, first.columnTotals?.[column] ?? 0),
            first.items.filter((item) => item[board.groupBy] === column).length, column)))
      : [await remainder(Math.min(shown.list, first.total), first.items.length)]
    return { ...first, items: [...first.items, ...extra.flat()] }
  }

  async function read(mode: 'replace' | 'append', column?: string) {
    if (expired.value || disposed) return
    const current = view.value
    if (pending.value && mode === 'replace' && ownerKey === current.key) {
      invalidatedWhileReading = true
      return
    }
    const previous = resultsCurrent.value ? settled.value?.page : undefined
    if (mode === 'append' && (pending.value || !previous?.projectionVersion || !writable.value)) return
    controller?.abort()
    const request = new AbortController()
    controller = request
    ownerKey = current.key
    pending.value = mode
    error.value = ''
    try {
      if (mode === 'append' && previous?.projectionVersion) {
        const board = current.layout === 'board' ? { groupBy: current.groupBy, column } : undefined
        const pageSize = board ? boardPageSize : listPageSize
        const offset = board ? previous.items.filter((item) => item[board.groupBy] === column).length : previous.items.length
        const target = display(current.key)
        if (column) target.columns[column] = Math.max(pageSize, target.columns[column] ?? 0, offset + pageSize)
        else target.list = Math.max(pageSize, target.list, offset + pageSize)
        try {
          const next = await loadCompactWorkPage(current.query, {
            offset, limit: pageSize,
            expectedProjectionVersion: previous.projectionVersion,
            expectedQueryVersion: previous.queryVersion ?? undefined,
            board
          }, request.signal)
          if (request.signal.aborted || view.value.key !== current.key) return
          if (next.projectionVersion !== previous.projectionVersion || next.queryVersion !== previous.queryVersion)
            throw new ApiError('查询期间进度已更新，请重试。', 409)
          if (next.verification === 'current') {
            save(current, { ...next, items: [...previous.items, ...next.items] })
            return
          }
        } catch (cause) {
          if (!(cause instanceof ApiError) || cause.status !== 409 || request.signal.aborted) throw cause
        }
        pending.value = 'replace'
      }
      let page: CompactWorkPage
      try {
        page = await replacement(current, request.signal)
      } catch (cause) {
        if (!(cause instanceof ApiError) || cause.status !== 409 || request.signal.aborted) throw cause
        page = await replacement(current, request.signal)
      }
      if (request.signal.aborted || view.value.key !== current.key) return
      // A server still warming up cannot displace a verified result for the current filters.
      if (page.projectionVersion || !resultsCurrent.value || !settled.value?.page.projectionVersion) save(current, page)
    } catch (cause) {
      if (!request.signal.aborted) error.value = errorMessage(cause)
    } finally {
      if (controller === request) {
        pending.value = undefined
        ownerKey = undefined
        if (!request.signal.aborted && invalidatedWhileReading) {
          invalidatedWhileReading = false
          reconcile()
        }
      }
    }
  }

  function reconcile() {
    if (expired.value) return
    const key = view.value.key
    if (ownerKey && ownerKey !== key) {
      controller?.abort()
      controller = undefined
      ownerKey = undefined
      pending.value = undefined
      invalidatedWhileReading = false
    }
    if (settled.value?.key !== key && cache.has(key)) settled.value = cache.get(key)
    if (settled.value?.key !== key) {
      void read('replace')
      return
    }
    if (sourceState.value?.verification !== 'current') return
    if (settled.value.page.projectionVersion === observedProjection.value) {
      settled.value.scope = scope.value
      return
    }
    if (scope.value === undefined || settled.value.scope !== scope.value) void read('replace')
  }

  function reload() { return read('replace') }
  async function refresh() {
    try {
      await requestWorkbenchReadRefresh()
      await reload()
    } catch (cause) {
      error.value = errorMessage(cause)
    }
  }

  watch([() => view.value.key, observedProjection, scope, () => sourceState.value?.verification, expired],
    reconcile, { immediate: true })
  onScopeDispose(() => {
    disposed = true
    controller?.abort()
  })
  return {
    result, resultsCurrent, resultProgressBasis, loading, loadingMore, error, verification, verificationError,
    writable, refresh, reload, loadMore: (column?: string) => read('append', column)
  }
}
