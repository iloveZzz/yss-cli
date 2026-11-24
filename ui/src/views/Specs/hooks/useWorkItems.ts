import { computed, shallowRef, watch, onBeforeUnmount, type Ref } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  errorMessage,
  queryWorkItems,
  type WorkQuery,
  type QueryResult,
  type Layout
} from '@/services/workbench'
import { queryPage } from '@/services/workbench-platform'
const requestedCounts = new Map<string, number>()
export function useWorkItems(query: Readonly<Ref<WorkQuery>>, layout?: Readonly<Ref<Layout>>) {
  const { snapshot, expired } = useWorkbench()
  const settled = shallowRef<{ result: QueryResult; query: WorkQuery; layout?: Layout }>()
  const result = computed(() => settled.value?.result)
  const resultsCurrent = computed(() =>
    !!settled.value &&
    JSON.stringify(settled.value.query) === JSON.stringify(query.value) &&
    settled.value.layout === layout?.value
  )
  const resultProgressBasis = computed(() => settled.value?.query.progressBasis ?? query.value.progressBasis)
  const pending = shallowRef<'replace' | 'append'>()
  const loading = computed(() => !!pending.value)
  const loadingMore = computed(() => pending.value === 'append')
  const error = shallowRef('')
  let controller: AbortController | undefined
  let generation = 0
  async function read(append: boolean) {
    controller?.abort()
    const own = new AbortController()
    controller = own
    const current = ++generation
    if (!snapshot.value || expired.value) {
      pending.value = undefined
      settled.value = undefined
      return
    }
    pending.value = append ? 'append' : 'replace'
    error.value = ''
    const requestedQuery = query.value
    const requestedLayout = layout?.value
    const key = JSON.stringify(requestedQuery)
    const settle = (value: QueryResult) => {
      settled.value = { result: value, query: requestedQuery, layout: requestedLayout }
      error.value = ''
    }
    try {
      if (requestedLayout === 'board') {
        const value = await queryWorkItems(requestedQuery, own.signal)
        if (current === generation && !own.signal.aborted) settle(value)
        return
      }
      const previous = append ? result.value : undefined
      const target = append ? (previous?.items.length ?? 0) + 50 : (requestedCounts.get(key) ?? 50)
      let items = previous?.items ?? []
      let page: QueryResult | undefined
      do {
        const value = await queryPage(
          requestedQuery,
          items.length,
          Math.min(200, target - items.length),
          own.signal
        )
        if (current !== generation || own.signal.aborted) return
        if (
          (page && page.revision !== value.revision) ||
          (previous && previous.revision !== value.revision)
        )
          throw new Error('记录在读取期间发生变化，请刷新列表。')
        page = value
        items = [...items, ...value.items]
        if (!value.items.length || items.length >= value.total) break
      } while (items.length < target)
      if (page) {
        settle({ ...page, items })
        requestedCounts.set(key, items.length)
      }
      error.value = ''
    } catch (cause) {
      if (current === generation && !own.signal.aborted) error.value = errorMessage(cause)
    } finally {
      if (current === generation) pending.value = undefined
    }
  }
  const refresh = () => read(false)
  const loadMore = () => {
    if (resultsCurrent.value && !loading.value) return read(true)
  }
  watch(
    [
      () => JSON.stringify(query.value),
      () => layout?.value,
      expired,
      () => snapshot.value?.revision,
      () =>
        snapshot.value?.specs
          .filter(
            (spec) =>
              (!query.value.projectIds.length ||
                query.value.projectIds.includes(spec.record.projectId)) &&
              (!query.value.specIds.length || query.value.specIds.includes(spec.record.id))
          )
          .map(
            (spec) => `${spec.record.id}:${spec.sourceModifiedAt}:${spec.content.artifactDigest}:${snapshot.value?.progress?.[spec.record.id]?.evidenceDigest}`
          )
          .join('|')
    ],
    () => void refresh(),
    { immediate: true }
  )
  onBeforeUnmount(() => {
    generation++
    controller?.abort()
  })
  return { result, resultProgressBasis, resultsCurrent, loading, loadingMore, error, refresh, loadMore }
}
