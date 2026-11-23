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
import { computed, reactive, shallowRef, watch, nextTick, useId } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { Button } from '@/components/ui/button'
import SingleSelect from '@/components/SingleSelect.vue'
import WorkItemRows from '@/components/WorkItemRows.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { isClosed } from '@/services/workbench'
import { useSpecFilters } from './hooks/useSpecFilters'
import { useWorkItems } from './hooks/useWorkItems'
import SpecBoard from './components/SpecBoard.vue'
import SavedViews from './components/SavedViews.vue'
import WorkFilters from './components/WorkFilters.vue'
const { snapshot } = useWorkbench()
const { filters, layout, groupBy, viewId, update, toggle, apply, clear, detachView } =
  useSpecFilters()
const id = useId()
const { result, loading, error, refresh, loadMore } = useWorkItems(filters, layout)
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
</script>
<template>
  <div class="page-heading specs-heading">
    <h1>全部 Spec</h1>
    <RouterLink to="/projects" class="text-link">关联更多 Spec ↗</RouterLink>
  </div>
  <section class="spec-query" aria-label="Spec 查询与视图">
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
        <div class="segmented" role="group" aria-label="进度依据">
          <button type="button" :aria-pressed="filters.progressBasis === 'effective'" @click="update({ progressBasis: 'effective' })">自动进度</button>
          <button type="button" :aria-pressed="filters.progressBasis === 'personal'" @click="update({ progressBasis: 'personal' })">个人标记</button>
        </div>
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
        <div class="segmented" role="group" aria-label="显示方式">
          <button
            type="button"
            :aria-pressed="layout === 'list'"
            @click="update({}, { layout: 'list' })"
          >
            列表
          </button>
          <button
            type="button"
            :aria-pressed="layout === 'board'"
            @click="update({}, { layout: 'board' })"
          >
            看板
          </button>
        </div>
        <label v-if="layout === 'board'" class="inline-field group-control" :for="id + 'group'">
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
      @update="update"
      @toggle="toggle"
      @clear="clear"
    />
  </section>
  <div class="results-toolbar">
    <span class="result-count" role="status" aria-live="polite">
      <strong>{{ result?.total ?? 0 }}</strong>
      项结果
      <span v-if="result && result.items.length < result.total">
        · 已显示 {{ result.items.length }} 项
      </span>
      <span v-if="loading" class="muted">· 刷新中</span>
    </span>
    <Button type="button" variant="ghost" :disabled="loading" @click="refresh">刷新结果</Button>
  </div>
  <p v-if="missingScope" class="notice warning">
    筛选引用的工程或 Spec 不存在，结果保持为空。请移除上方标记为“已失效”的条件。
  </p>
  <p v-if="error" role="alert" class="notice error">
    {{ error }} {{ result ? '保留上次查询结果。' : '' }}
    <Button type="button" variant="outline" @click="refresh">重试</Button>
  </p>
  <p v-else-if="loading && result" class="muted view-note">正在载入新结果，以下暂为上次查询。</p>
  <p v-if="!result && loading" role="status" class="query-empty">正在读取 Spec…</p>
  <template v-if="result">
    <section v-if="!result.items.length" class="panel query-empty" aria-label="查询结果为空">
      <h2>没有匹配的 Spec</h2>
      <p>可清除筛选，或将已关闭与停用记录纳入当前查询。</p>
      <div class="inline-actions">
        <Button type="button" variant="outline" @click="clear">清除筛选</Button>
        <Button type="button" variant="ghost" @click="all">显示已关闭与停用</Button>
      </div>
    </section>
    <section v-else-if="layout === 'list'" class="panel" :aria-busy="loading">
      <WorkItemRows :items="result.items" :progress-basis="filters.progressBasis" />
      <div v-if="result.total > result.items.length" class="load-more">
        <Button type="button" variant="outline" :disabled="loading" @click="loadMore">
          加载更多（已显示 {{ result.items.length }} / {{ result.total }}）
        </Button>
      </div>
    </section>
    <SpecBoard
      v-else
      v-model:limits="display.columns"
      v-model:scroll-left="display.scrollLeft"
      :items="result.items"
      :group-by="groupBy"
      :revision="result.revision"
      :progress-basis="filters.progressBasis"
    />
    <div v-if="layout === 'board' && result.total > result.items.length" class="load-more">
      <Button type="button" variant="outline" :disabled="loading" @click="loadMore">
        加载更多（{{ result.items.length }} / {{ result.total }}）
      </Button>
    </div>
  </template>
  <details class="coverage-note">
    <summary>纳管范围 · 当前工程范围共 {{ coverage.total }} 项</summary>
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
  </details>
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
  color: #717780;
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
  color: #717780;
  font-size: 12px;
}
.result-count strong {
  color: #303743;
  font-size: 15px;
  font-weight: 600;
}
.results-toolbar > button {
  color: #717780;
  font-size: 12px;
}
.query-empty {
  padding: 28px 20px;
  color: #717780;
  font-size: 12px;
}
.query-empty h2 {
  color: #555d6a;
  font-size: 14px;
}
.query-empty p {
  margin: 8px 0 16px;
}
.coverage-note {
  margin: 16px 0 24px;
  color: #858d99;
  font-size: 11px;
}
.coverage-note summary {
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
  color: #717780;
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
    min-height: 36px;
  }
  .sort-control :deep(.sort-select) {
    width: 126px;
  }
  .group-control {
    flex-basis: 100%;
  }
}
</style>
