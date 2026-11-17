import { shallowRef, watch, onBeforeUnmount, type Ref } from 'vue'
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
  const result = shallowRef<QueryResult>()
  const loading = shallowRef(false)
  const error = shallowRef('')
  let controller: AbortController | undefined
  let generation = 0
  async function read(append: boolean) {
    controller?.abort()
    const own = new AbortController()
    controller = own
    const current = ++generation
    if (!snapshot.value || expired.value) return
    loading.value = true
    const key = JSON.stringify(query.value)
    try {
      if (layout?.value === 'board') {
        const value = await queryWorkItems(query.value, own.signal)
        if (current === generation && !own.signal.aborted) {
          result.value = value
          error.value = ''
        }
        return
      }
      const previous = append ? result.value : undefined
      const target = append ? (previous?.items.length ?? 0) + 50 : (requestedCounts.get(key) ?? 50)
      let items = previous?.items ?? []
      let page: QueryResult | undefined
      do {
        const value = await queryPage(
          query.value,
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
        result.value = { ...page, items }
        requestedCounts.set(key, items.length)
      }
      error.value = ''
    } catch (cause) {
      if (current === generation && !own.signal.aborted) error.value = errorMessage(cause)
    } finally {
      if (current === generation) loading.value = false
    }
  }
  const refresh = () => read(false)
  const loadMore = () => read(true)
  watch(
    [
      () => JSON.stringify(query.value),
      () => layout?.value,
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
  return { result, loading, error, refresh, loadMore }
}
