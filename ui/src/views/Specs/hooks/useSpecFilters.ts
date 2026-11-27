import { computed, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'
import {
  defaultWorkQuery,
  type WorkQuery,
  type SavedView
} from '@/services/workbench'
import { decodeWorkQuery, encodeWorkQuery, savedSpecQuery } from '../schemas/filter'
export function useSpecFilters() {
  const route = useRoute()
  const router = useRouter()
  const state = shallowRef(decodeWorkQuery(route.query))
  const { catalog } = useWorkbenchCatalog()
  let navigation = 0
  let pending = false
  const filters = computed(() => state.value.query)
  const viewId = computed(() => state.value.viewId)
  watch(
    () => route.query,
    (query) => {
      if (!pending) state.value = decodeWorkQuery(query)
    }
  )
  function navigate(next: typeof state.value) {
    // 连续操作合并到即时状态，不等待上一轮路由写入。
    state.value = next
    pending = true
    const own = ++navigation
    void router
      .replace({ query: encodeWorkQuery(next.query, next.viewId) })
      .finally(() => {
        if (own !== navigation) return
        pending = false
        state.value = decodeWorkQuery(route.query)
      })
  }
  function update(patch: Partial<WorkQuery>) {
    const next: WorkQuery = { ...filters.value, ...patch, entity: 'spec', progressBasis: 'effective' }
    if (patch.includeClosed === false)
      next.statuses = next.statuses.filter((s) => s !== 'done' && s !== 'canceled')
    if (next.statuses.some((s) => s === 'done' || s === 'canceled')) next.includeClosed = true
    navigate({ ...state.value, query: next })
  }
  watch(
    [viewId, catalog],
    ([id, current]) => {
      if (current?.savedViews.some((view) => view.id === id && view.query.entity !== 'spec'))
        detachView()
    },
    { immediate: true }
  )
  function apply(view: SavedView) {
    if (view.query.entity === 'spec')
      navigate({ query: savedSpecQuery(view), viewId: view.id })
  }
  function toggle(
    key: 'projectIds' | 'stages' | 'statuses' | 'contentStatuses' | 'reminderKinds' | 'attention',
    value: string,
    checked: boolean
  ) {
    const selected = filters.value[key]
    update({
      [key]: checked
        ? [...new Set([...selected, value])]
        : selected.filter((item) => item !== value)
    })
  }
  function clear() {
    navigate({
      query: { ...defaultWorkQuery(), sort: filters.value.sort }
    })
  }
  function detachView() {
    navigate({ ...state.value, viewId: undefined })
  }
  return { filters, viewId, update, toggle, apply, clear, detachView }
}
