import { computed, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'
import {
  defaultWorkQuery,
  type WorkQuery,
  type Layout,
  type GroupBy,
  type SavedView
} from '@/services/workbench'
import { decodeWorkQuery, encodeWorkQuery } from '../schemas/filter'
export function useSpecFilters() {
  const route = useRoute()
  const router = useRouter()
  const state = shallowRef(decodeWorkQuery(route.query))
  const { catalog } = useWorkbenchCatalog()
  let navigation = 0
  let pending = false
  const filters = computed(() => state.value.query)
  const layout = computed(() => state.value.layout)
  const groupBy = computed(() => state.value.groupBy)
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
      .replace({ query: encodeWorkQuery(next.query, next.layout, next.groupBy, next.viewId) })
      .finally(() => {
        if (own !== navigation) return
        pending = false
        state.value = decodeWorkQuery(route.query)
      })
  }
  function update(patch: Partial<WorkQuery>, display?: { layout?: Layout; groupBy?: GroupBy }) {
    const next: WorkQuery = { ...filters.value, ...patch, entity: 'spec' }
    if (patch.includeClosed === false)
      next.statuses = next.statuses.filter((s) => s !== 'done' && s !== 'canceled')
    if (next.statuses.some((s) => s === 'done' || s === 'canceled')) next.includeClosed = true
    navigate({ ...state.value, query: next, ...display })
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
      navigate({
        query: {
          ...defaultWorkQuery(),
          ...view.query,
          progressBasis: view.query.progressBasis ?? 'personal',
          attention: view.query.attention ?? []
        },
        layout: view.layout,
        groupBy: view.groupBy,
        viewId: view.id
      })
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
      ...state.value,
      query: { ...defaultWorkQuery(), sort: filters.value.sort, progressBasis: filters.value.progressBasis },
      viewId: undefined
    })
  }
  function detachView() {
    navigate({ ...state.value, viewId: undefined })
  }
  return { filters, layout, groupBy, viewId, update, toggle, apply, clear, detachView }
}
