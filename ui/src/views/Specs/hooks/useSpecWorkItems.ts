import { computed, onScopeDispose, shallowRef, watch, type Ref } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'
import { useVerifiedWorkbenchRefresh } from '@/hooks/useWorkbenchVerification'
import { ApiError, errorMessage, type WorkQuery } from '@/services/workbench'
import { loadCompactWorkPage, type CompactWorkPage } from '@/services/workbench-read'

const listPageSize = 50
interface View {
  key: string
  query: WorkQuery
}
interface Settled {
  key: string
  page: CompactWorkPage
  scope?: string
}
const cache = new Map<string, Settled>()
const depths = new Map<string, number>()

export function useSpecWorkItems(
  query: Readonly<Ref<WorkQuery>>,
  enabled?: Readonly<Ref<boolean>>
) {
  const { expired, sourceState } = useWorkbench()
  const { catalog } = useWorkbenchCatalog()
  const view = computed<View>(() => ({
    key: JSON.stringify(query.value),
    query: query.value
  }))
  const settled = shallowRef<Settled>()
  const result = computed(() => settled.value?.page)
  const resultsCurrent = computed(() => settled.value?.key === view.value.key)
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
  const readError = shallowRef('')
  const { refresh, refreshing, refreshError } = useVerifiedWorkbenchRefresh(
    () => read('replace', true), () => enabled?.value !== false
  )
  const loading = computed(() => refreshing.value || !!pending.value)
  const loadingMore = computed(() => pending.value === 'append')
  const error = computed(() => refreshError.value || readError.value)
  const observedProjection = computed(() => sourceState.value?.projectionVersion)
  const verification = computed(() => {
    const observed = sourceState.value
    const page = result.value
    if (!observed?.verification || !page ||
      (observed.changeSequence ?? 0) <= (page.changeSequence ?? 0))
      return page?.verification ?? observed?.verification
    if (observed.verification === 'current') {
      if (!page.projectionVersion) return 'pending'
      if (page.verification === 'stale' && page.projectionVersion === observedProjection.value) return 'stale'
      if (page.projectionVersion !== observedProjection.value &&
        (scope.value === undefined || settled.value?.scope !== scope.value)) return 'stale'
    }
    return observed.verification
  })
  const verificationError = computed(() => sourceState.value?.verificationError ?? result.value?.verificationError)
  const writable = computed(() =>
    resultsCurrent.value && !!result.value?.projectionVersion && verification.value === 'current' &&
    !expired.value && !error.value && !refreshing.value && pending.value !== 'replace'
  )
  let controller: AbortController | undefined
  let ownerKey: string | undefined
  let interruptedKey: string | undefined
  let invalidatedWhileReading = false
  let disposed = false

  function rememberDepth(key: string, count: number) {
    depths.set(key, Math.max(depths.get(key) ?? listPageSize, count))
    if (depths.size > 20) depths.delete(depths.keys().next().value!)
  }

  function save(current: View, page: CompactWorkPage) {
    const record = { key: current.key, page, scope: page.projectionVersion === observedProjection.value ? scope.value : undefined }
    settled.value = record
    if (!page.projectionVersion) return
    cache.delete(current.key)
    cache.set(current.key, record)
    if (cache.size > 12) cache.delete(cache.keys().next().value!)
  }

  async function replacement(current: View, signal: AbortSignal): Promise<CompactWorkPage> {
    const shown = depths.get(current.key) ?? listPageSize
    const first = await loadCompactWorkPage(current.query, {
      limit: Math.min(200, shown)
    }, signal)
    const version = first.projectionVersion
    if (!version) return first

    const target = Math.min(shown, first.total)
    const items = [...first.items]
    while (items.length < target && !signal.aborted) {
      const next = await loadCompactWorkPage(current.query, {
        offset: items.length,
        limit: Math.min(200, target - items.length),
        expectedProjectionVersion: version,
        expectedQueryVersion: first.queryVersion ?? undefined
      }, signal)
      if (next.projectionVersion !== version || next.queryVersion !== first.queryVersion)
        throw new ApiError('查询期间进度已更新，请重试。', 409)
      if (!next.items.length) break
      items.push(...next.items)
    }
    return { ...first, items }
  }

  async function read(mode: 'replace' | 'append', replacePending = false) {
    if (expired.value || disposed || enabled?.value === false) return
    const current = view.value
    if (pending.value && mode === 'replace' && ownerKey === current.key && !replacePending) {
      invalidatedWhileReading = true
      return
    }
    const previous = resultsCurrent.value ? settled.value?.page : undefined
    if (mode === 'append' && (pending.value || !previous?.projectionVersion || !writable.value)) return
    controller?.abort()
    invalidatedWhileReading = false
    const request = new AbortController()
    controller = request
    ownerKey = current.key
    pending.value = mode
    readError.value = ''
    try {
      if (mode === 'append' && previous?.projectionVersion) {
        const offset = previous.items.length
        rememberDepth(current.key, offset + listPageSize)
        try {
          const next = await loadCompactWorkPage(current.query, {
            offset, limit: listPageSize,
            expectedProjectionVersion: previous.projectionVersion,
            expectedQueryVersion: previous.queryVersion ?? undefined
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
      if (!request.signal.aborted) readError.value = errorMessage(cause)
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
    if (ownerKey && (ownerKey !== key || enabled?.value === false)) {
      if (ownerKey === key) interruptedKey = key
      controller?.abort()
      controller = undefined
      ownerKey = undefined
      pending.value = undefined
      invalidatedWhileReading = false
    }
    if (enabled?.value === false) return
    const interrupted = interruptedKey === key
    interruptedKey = undefined
    if (settled.value?.key !== key && cache.has(key)) settled.value = cache.get(key)
    if (interrupted || settled.value?.key !== key) {
      void read('replace')
      return
    }
    if (sourceState.value?.verification !== 'current') return
    if (settled.value.page.projectionVersion === observedProjection.value) {
      const page = settled.value.page
      if (page.verification === 'stale' &&
        (page.changeSequence ?? 0) < (sourceState.value.changeSequence ?? 0)) {
        void read('replace')
        return
      }
      settled.value.scope = scope.value
      return
    }
    if (scope.value === undefined || settled.value.scope !== scope.value) void read('replace')
  }

  function reload() { return read('replace') }

  watch([() => view.value.key, expired, () => enabled?.value], reconcile, { immediate: true })
  watch([observedProjection, scope, () => sourceState.value?.verification, () => sourceState.value?.projectionObservationId, refreshing], () => {
    if (!refreshing.value) reconcile()
  })
  onScopeDispose(() => {
    disposed = true
    controller?.abort()
  })
  return {
    result, resultsCurrent, loading, loadingMore, error, verification, verificationError,
    writable, refresh, reload, loadMore: () => read('append')
  }
}
