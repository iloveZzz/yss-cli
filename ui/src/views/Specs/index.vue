<script lang="ts">
interface DisplayState {
  limit: number
  columns: Record<string, number>
  scrollTop: number
  scrollLeft: number
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
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import SingleSelect from '@/components/SingleSelect.vue'
import WorkItemRows from '@/components/WorkItemRows.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { isClosed, type WorkQuery } from '@/services/workbench'
import { useSpecFilters } from './hooks/useSpecFilters'
import { useWorkItems } from './hooks/useWorkItems'
import SpecBoard from './components/SpecBoard.vue'
import SavedViews from './components/SavedViews.vue'
import WorkFilters from './components/WorkFilters.vue'
const { snapshot } = useWorkbench()
const { filters, layout, groupBy, viewId, update, toggle, apply, clear, detachView } =
  useSpecFilters()
const id = useId()
const { result, resultProgressBasis, resultsCurrent, loading, loadingMore, error, refresh, loadMore } = useWorkItems(filters, layout)
const showPlaceholder = computed(() =>
  loading.value && (!result.value || (!resultsCurrent.value && !result.value.items.length))
)
const displayKey = computed(() => JSON.stringify([filters.value, layout.value, groupBy.value]))
const display = computed(() => {
  let value = displays.get(displayKey.value)
  if (!value) {
    value = reactive({ limit: 50, columns: {}, scrollTop: 0, scrollLeft: 0 })
    displays.set(displayKey.value, value)
  }
  return value
})
const restorePosition = shallowRef(true)
function rememberPosition() {
  display.value.scrollTop = window.scrollY
}
function updateFromFilters(patch: Partial<WorkQuery>) {
  update(patch)
  rememberPosition()
}
function toggleFromFilters(...args: Parameters<typeof toggle>) {
  toggle(...args)
  rememberPosition()
}
onBeforeRouteLeave(rememberPosition)
watch(displayKey, (_next, previous) => {
  const before = displays.get(previous)
  if (before) before.scrollTop = window.scrollY
  restorePosition.value = true
})
watch(
  result,
  async () => {
    if (!restorePosition.value) return
    const key = displayKey.value
    await nextTick()
    if (key === displayKey.value) {
      window.scrollTo({ top: display.value.scrollTop, behavior: 'instant' })
      restorePosition.value = false
    }
  },
  { flush: 'post' }
)
const projects = computed(() => snapshot.value?.projects ?? [])
const missingScope = computed(
  () =>
    filters.value.projectIds.some((id) => !projects.value.some((p) => p.record.id === id)) ||
    filters.value.specIds.some((id) => !snapshot.value?.specs.some((s) => s.record.id === id))
)
const coverage = computed(() => {
  const ss = (snapshot.value?.specs ?? []).filter(
    (s) =>
      (!filters.value.projectIds.length || filters.value.projectIds.includes(s.record.projectId)) &&
      (!filters.value.specIds.length || filters.value.specIds.includes(s.record.id))
  )
  const enabled = (id: string) => {
    const s = ss.find((s) => s.record.id === id)
    return (
      !!s?.record.active &&
      projects.value.some((p) => p.record.id === s.record.projectId && p.record.active)
    )
  }
  const entries = ss.map((s) => ({ active: enabled(s.record.id), status: filters.value.progressBasis === 'effective' ? snapshot.value?.progress?.[s.record.id]?.status ?? 'unmarked' : s.record.status }))
  return {
    total: entries.length,
    inactive: entries.filter((e) => !e.active).length,
    closed: entries.filter((e) => e.active && isClosed(e.status)).length
  }
})
function all() {
  update({ activity: 'all', includeClosed: true, statuses: [] })
}
function selectBasis(value: unknown) {
  if (value === 'effective' || value === 'personal') update({ progressBasis: value })
}
function selectLayout(value: unknown) {
  if (value === 'list' || value === 'board') update({}, { layout: value })
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
        :layout="layout"
        :group-by="groupBy"
        :view-id="viewId"
        @apply="apply"
        @removed="detachView"
      />
      <div class="result-controls">
        <ToggleGroup class="segmented" type="single" aria-label="进度依据" :model-value="filters.progressBasis" @update:model-value="selectBasis">
          <ToggleGroupItem value="effective">自动进度</ToggleGroupItem>
          <ToggleGroupItem value="personal">个人标记</ToggleGroupItem>
        </ToggleGroup>
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
            @update:model-value="update({ sort: $event as 'updatedDesc' | 'titleAsc' })"
          />
        </label>
        <ToggleGroup class="segmented" type="single" aria-label="显示方式" :model-value="layout" @update:model-value="selectLayout">
          <ToggleGroupItem value="list">列表</ToggleGroupItem>
          <ToggleGroupItem value="board">看板</ToggleGroupItem>
        </ToggleGroup>
        <label v-if="layout === 'board'" class="inline-field group-control" :for="id + 'group'" data-workbench-reveal>
          <span>分组</span>
          <SingleSelect
            :id="id + 'group'"
            :model-value="groupBy"
            :options="[
              { value: 'status', label: '按状态' },
              { value: 'stage', label: '按阶段' }
            ]"
            name="groupBy"
            @update:model-value="update({}, { groupBy: $event as 'status' | 'stage' })"
          />
        </label>
      </div>
    </div>
    <WorkFilters
      :query="filters"
      :projects="projects"
      :specs="snapshot?.specs ?? []"
      embedded
      @update="updateFromFilters"
      @toggle="toggleFromFilters"
      @clear="clear"
    />
  </Card>
  <div class="results-toolbar">
    <span class="result-count" role="status">
      <template v-if="result">
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
  <Alert v-if="missingScope" class="notice warning" role="status">
    筛选引用的工程或 Spec 不存在，结果保持为空。请移除上方标记为“已失效”的条件。
  </Alert>
  <Alert v-if="error" role="alert" class="notice error">
    {{ error }} {{ result ? '保留上次查询结果。' : '' }}
    <Button type="button" variant="outline" @click="refresh">重试</Button>
  </Alert>
  <p v-else-if="loading && result?.items.length && !resultsCurrent" class="muted view-note">正在载入新结果，以下暂为上次查询。</p>
  <Card v-if="showPlaceholder" class="query-placeholder gap-0 p-0 shadow-none" aria-busy="true">
    <LoadingSkeleton :variant="layout === 'board' ? 'cards' : 'table'" :rows="layout === 'board' ? 3 : 4" />
  </Card>
  <template v-else-if="result">
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
    <Card v-else-if="layout === 'list'" class="panel result-list gap-0 p-0 shadow-none" data-workbench-reveal>
      <WorkItemRows :items="result.items" :progress-basis="resultProgressBasis" :refreshing="loading && !loadingMore" />
      <div v-if="result.total > result.items.length" class="load-more">
        <Button type="button" variant="outline" :disabled="loading || !resultsCurrent" @click="loadMore">
          <LoadingIndicator :active="loadingMore" />
          {{ loadingMore ? '正在加载更多…' : '加载更多' }}（已显示 {{ result.items.length }} / {{ result.total }}）
        </Button>
      </div>
    </Card>
    <SpecBoard
      v-else
      v-model:limits="display.columns"
      v-model:scroll-left="display.scrollLeft"
      :items="result.items"
      :group-by="groupBy"
      :revision="result.revision"
      :progress-basis="resultProgressBasis"
      :readonly-results="!resultsCurrent || (loading && !loadingMore)"
      :busy="loading"
      :refreshing="loading && !loadingMore"
    />
    <div v-if="layout === 'board' && result.total > result.items.length" class="load-more">
      <Button type="button" variant="outline" :disabled="loading || !resultsCurrent" @click="loadMore">
        <LoadingIndicator :active="loadingMore" />
        {{ loadingMore ? '正在加载更多…' : '加载更多' }}（{{ result.items.length }} / {{ result.total }}）
      </Button>
    </div>
  </template>
  <Collapsible class="coverage-note">
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
  background: #fff;
  border: 1px solid var(--yss-colors-border);
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
.result-controls {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  min-width: 0;
}
.sort-control,
.group-control {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.sort-control :deep(.sort-select) {
  width: 140px;
  min-width: 0;
}
.group-control :deep(button) {
  width: 116px;
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
  .result-controls {
    width: 100%;
    gap: 10px;
  }
  .segmented button {
    min-height: 44px;
  }
  .sort-control :deep(.sort-select) {
    width: 126px;
  }
  .group-control {
    flex-basis: 100%;
  }
}
</style>
