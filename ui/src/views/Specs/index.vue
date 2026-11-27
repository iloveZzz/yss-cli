<script lang="ts">
interface DisplayState {
  anchor?: { id: string; top: number }
  scrollTop: number
}
const displays = new Map<string, DisplayState>()
</script>
<script setup lang="ts">
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Alert } from '@/components/ui/alert'
import { computed, reactive, shallowRef, watch, nextTick, useId } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import RegisteredSpecs from '@/components/RegisteredSpecs.vue'
import SingleSelect from '@/components/SingleSelect.vue'
import WorkItemRows from '@/components/WorkItemRows.vue'
import { registerWorkbenchRouteReads, useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'
import type { WorkQuery } from '@/services/workbench'
import { useSpecFilters } from './hooks/useSpecFilters'
import { useSpecWorkItems } from './hooks/useSpecWorkItems'
import SavedViews from './components/SavedViews.vue'
import WorkFilters from './components/WorkFilters.vue'
const { catalog, catalogError, refreshCatalog } = useWorkbenchCatalog()
const { filters, viewId, update, toggle, apply, clear, detachView } =
  useSpecFilters()
const id = useId()
const {
  result, resultsCurrent, loading, loadingMore, error,
  verification, verificationError, writable, refresh, loadMore
} = useSpecWorkItems(filters)
registerWorkbenchRouteReads({ busy: loading, readAt: computed(() => result.value?.readAt), refresh })
const showPlaceholder = computed(() =>
  !registered.value.length && !error.value && verification.value !== 'failed' &&
  (loading.value || verification.value === 'pending') &&
  (!result.value?.projectionVersion || (!resultsCurrent.value && !result.value.items.length))
)
const displayKey = computed(() => JSON.stringify(filters.value))
const display = computed(() => {
  let value = displays.get(displayKey.value)
  if (!value) {
    value = reactive({ scrollTop: 0 })
    displays.set(displayKey.value, value)
    if (displays.size > 20) displays.delete(displays.keys().next().value!)
  }
  return value
})
const restorePosition = shallowRef(true)
function rows() {
  return Array.from(document.querySelectorAll<HTMLElement>('#main [data-spec-id]'))
}
function rememberPosition(value: DisplayState = display.value) {
  value.scrollTop = window.scrollY
  const row = rows().find((element) => {
    const rect = element.getBoundingClientRect()
    return rect.bottom > 0 && rect.right > 0 && rect.left < window.innerWidth
  })
  value.anchor = row?.dataset.specId
    ? { id: row.dataset.specId, top: row.getBoundingClientRect().top }
    : undefined
}
function updateFromFilters(patch: Partial<WorkQuery>) {
  rememberPosition()
  update(patch)
}
function toggleFromFilters(...args: Parameters<typeof toggle>) {
  rememberPosition()
  toggle(...args)
}
onBeforeRouteLeave(() => rememberPosition())
watch(displayKey, (_next, previous) => {
  const before = displays.get(previous)
  if (before) rememberPosition(before)
  restorePosition.value = true
})
watch(loading, (busy) => {
  if (busy && !loadingMore.value && !restorePosition.value && result.value?.projectionVersion) {
    rememberPosition()
    restorePosition.value = true
  }
})
watch(
  result,
  async (value) => {
    if (!restorePosition.value || !resultsCurrent.value || !value?.projectionVersion) return
    const key = displayKey.value
    await nextTick()
    if (key === displayKey.value) {
      const saved = display.value
      const anchor = saved.anchor
      const row = anchor && rows().find((element) => element.dataset.specId === anchor.id)
      const top = anchor && row ? window.scrollY + row.getBoundingClientRect().top - anchor.top : saved.scrollTop
      window.scrollTo({ top, behavior: 'instant' })
      restorePosition.value = false
    }
  },
  { flush: 'post', immediate: true }
)
const projects = computed(() => catalog.value?.projects ?? [])
const registered = computed(() => {
  if (result.value?.projectionVersion) return []
  const { projectIds, specIds, activity } = filters.value
  const enabled = new Set(projects.value.filter((project) => project.active).map((project) => project.id))
  return catalog.value?.specs.filter((spec) => {
    const active = spec.active && enabled.has(spec.projectId)
    return (!projectIds.length || projectIds.includes(spec.projectId)) &&
      (!specIds.length || specIds.includes(spec.id)) &&
      (activity === 'all' || active === (activity === 'active'))
  }) ?? []
})
const missingScope = computed(
  () => !!catalog.value && (
    filters.value.projectIds.some((id) => !projects.value.some((p) => p.id === id)) ||
    filters.value.specIds.some((id) => !catalog.value?.specs.some((s) => s.id === id))
  )
)
const coverage = computed(() => resultsCurrent.value ? result.value?.coverage : undefined)
function all() {
  update({ activity: 'all', includeClosed: true, statuses: [] })
}
</script>
<template>
  <div class="page-heading specs-heading">
    <h1>全部 Spec</h1>
    <RouterLink to="/projects" class="text-link">关联更多 Spec ↗</RouterLink>
  </div>
  <Card class="spec-query gap-0 p-0 shadow-none" role="region" aria-label="Spec 查询与视图">
    <div class="query-toolbar">
      <SavedViews
        :query="filters"
        :view-id="viewId"
        @apply="apply"
        @removed="detachView"
      />
      <label class="inline-field sort-control" :for="id + 'sort'">
        <span>排序</span>
        <SingleSelect
          :id="id + 'sort'"
          :model-value="filters.sort"
          :options="[
            { value: 'updatedDesc', label: '最近更新' },
            { value: 'titleAsc', label: '标题顺序' }
          ]"
          name="sort"
          class="sort-select"
          @update:model-value="updateFromFilters({ sort: $event as 'updatedDesc' | 'titleAsc' })"
        />
      </label>
    </div>
    <WorkFilters
      :query="filters"
      :projects="projects"
      :specs="catalog?.specs ?? []"
      :catalog-ready="!!catalog"
      embedded
      @update="updateFromFilters"
      @toggle="toggleFromFilters"
      @clear="clear"
    />
  </Card>
  <div class="results-toolbar">
    <span class="result-count" role="status">
      <template v-if="result?.projectionVersion">
        <strong>{{ result.total }}</strong>
        项结果
        <span v-if="result.items.length < result.total">· 已显示 {{ result.items.length }} 项</span>
      </template>
      <span v-if="loading" class="muted">{{ loadingMore ? ' · 正在追加' : result ? ' · 刷新中' : '正在读取 Spec…' }}</span>
    </span>
    <Button type="button" variant="ghost" :disabled="loading" @click="refresh">
      {{ loading && !loadingMore && result ? '刷新中…' : '刷新结果' }}
    </Button>
  </div>
  <Alert v-if="catalogError" class="notice warning" role="status">
    工程与保存的视图读取失败。{{ catalogError }}
    <Button type="button" variant="outline" @click="refreshCatalog()">重试</Button>
  </Alert>
  <Alert v-if="missingScope" class="notice warning" role="status">
    筛选引用的工程或 Spec 不存在，结果保持为空。请移除上方标记为“已失效”的条件。
  </Alert>
  <Alert v-if="error || verification === 'failed'" role="alert" class="notice error">
    {{ error || verificationError || 'Spec 进度核对失败。' }}
    {{ result?.projectionVersion ? '保留上次查询结果。' : '' }}
    <Button type="button" variant="outline" :disabled="loading" @click="refresh">重试</Button>
  </Alert>
  <p v-else-if="verification === 'stale'" class="muted" role="status">正在核对变化，当前显示上次核实结果。</p>
  <p v-else-if="loading && result?.items.length && !resultsCurrent" class="muted view-note">正在载入新结果，以下暂为上次查询。</p>
  <RegisteredSpecs v-if="registered.length" :projects="projects" :specs="registered"
    :failed="!!error || verification === 'failed'" />
  <Card v-if="showPlaceholder" class="query-placeholder gap-0 p-0 shadow-none" aria-busy="true">
    <LoadingSkeleton variant="table" :rows="4" />
  </Card>
  <template v-else-if="result?.projectionVersion">
    <Empty v-if="!result.items.length" class="panel query-empty" :aria-busy="loading" aria-label="查询结果为空" data-workbench-reveal>
      <EmptyHeader>
        <EmptyTitle><h2>{{ resultsCurrent ? '没有匹配的 Spec' : '上次查询没有匹配的 Spec' }}</h2></EmptyTitle>
        <EmptyDescription>可清除筛选，或将已关闭与停用记录纳入当前查询。</EmptyDescription>
      </EmptyHeader>
      <EmptyContent class="query-empty-actions">
        <Button type="button" variant="outline" @click="clear">清除筛选</Button>
        <Button type="button" variant="ghost" @click="all">显示已关闭与停用</Button>
      </EmptyContent>
    </Empty>
    <Card v-else class="panel result-list gap-0 p-0 shadow-none" data-workbench-reveal>
      <WorkItemRows :items="result.items" :refreshing="loading && !loadingMore" />
      <div v-if="result.total > result.items.length" class="load-more">
        <Button type="button" variant="outline" :disabled="loading || !writable" @click="loadMore()">
          <LoadingIndicator :active="loadingMore" />
          {{ loadingMore ? '正在加载更多…' : '加载更多' }}（已显示 {{ result.items.length }} / {{ result.total }}）
        </Button>
      </div>
    </Card>
  </template>
  <Collapsible v-if="coverage" class="coverage-note">
    <CollapsibleTrigger>纳管范围 · 当前工程范围共 {{ coverage.total }} 项</CollapsibleTrigger>
    <CollapsibleContent>
      <div>
        <p>
          启用且已关闭 {{ coverage.closed }} 项，停用
          {{ coverage.inactive }} 项。未登记目录和未接入平台不在当前覆盖范围。
        </p>
        <p v-if="!filters.includeClosed && !filters.needsReconfirmation && !filters.attention.length && !filters.reminderKinds.includes('ready')">
          当前已隐藏已完成与已取消记录。
        </p>
        <Button type="button" variant="ghost" @click="all">显示已关闭与停用</Button>
      </div>
    </CollapsibleContent>
  </Collapsible>
</template>
<style scoped>
.spec-query {
  min-width: 0;
  margin-bottom: 16px;
  background: var(--workbench-glass-pane);
  border: 1px solid var(--workbench-glass-edge);
  border-radius: 10px;
}
.query-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px 20px;
  padding: 12px 16px;
  border-bottom: 1px solid #edf0f4;
  min-width: 0;
}
.sort-control {
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.sort-control :deep(.sort-select) {
  width: 140px;
  min-width: 0;
}
.results-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px 16px;
  margin-bottom: 10px;
}
.result-count {
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.result-count strong {
  color: #303743;
  font-size: 15px;
  font-weight: 600;
}
.results-toolbar > button {
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.results-toolbar > button:disabled { opacity: 1; }
.query-empty {
  padding: 28px 20px;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.query-empty-actions {
  flex-flow: row wrap;
  justify-content: center;
  gap: 8px;
}
.query-placeholder {
  border: 0;
  background: transparent;
}
.result-list {
  min-width: 0;
}
.coverage-note {
  margin: 16px 0 24px;
  color: var(--brand-color-text-secondary);
  font-size: 11px;
}
.coverage-note [data-slot='collapsible-trigger'] {
  cursor: pointer;
  width: fit-content;
}
.coverage-note > div {
  margin-top: 10px;
}
.coverage-note p {
  margin: 4px 0;
}
.coverage-note :deep(button) {
  color: var(--brand-color-text-secondary);
  font-size: 11px;
  margin-top: 4px;
  padding-left: 0;
}
@media (width<720px) {
  .specs-heading {
    flex-wrap: wrap;
    gap: 12px;
  }
  .query-toolbar {
    padding: 12px;
  }
  .query-toolbar :deep(.saved-views) {
    flex-basis: 100%;
  }
  .sort-control :deep(.sort-select) {
    width: 126px;
  }
}
</style>
