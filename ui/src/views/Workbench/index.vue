<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter, type LocationQueryRaw } from 'vue-router'
import { ArrowRight, FileClock, History, RefreshCw } from '@lucide/vue'
import { Alert } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import RegisteredSpecs from '@/components/RegisteredSpecs.vue'
import WorkItemRows from '@/components/WorkItemRows.vue'
import ContentAttention from './components/ContentAttention.vue'
import ActiveJobs from './components/ActiveJobs.vue'
import { registerWorkbenchRouteReads, useWorkbenchCatalog, useWorkbenchRefreshAction } from '@/hooks/useWorkbenchReads'
import { dateLabel, stages, type ChangeEvent } from '@/services/workbench'
import { eventDescription } from '@/services/workbench-history'
import type { WorkbenchOverview } from '@/services/workbench-read'
import { useWorkbenchOverview } from './hooks/useWorkbenchOverview'

const { overview, loading, error, verification, refresh } = useWorkbenchOverview()
const { busy: refreshBusy, disabled: refreshDisabled, refresh: refreshPage } = useWorkbenchRefreshAction()
const { catalog, catalogError, refreshCatalog } = useWorkbenchCatalog()
const route = useRoute()
const router = useRouter()
const ready = computed(() => !!overview.value?.projectionVersion && !!overview.value.projectCount)
const registered = computed(() => {
  const projects = new Set(catalog.value?.projects.filter((project) => project.active).map((project) => project.id))
  return catalog.value?.specs.filter((spec) => spec.active && projects.has(spec.projectId)) ?? []
})
const showPlaceholder = computed(() =>
  !registered.value.length && !error.value && (!overview.value ? loading.value :
    !!overview.value.projectCount && !overview.value.projectionVersion && verification.value === 'pending')
)
registerWorkbenchRouteReads({ busy: loading, readAt: computed(() => overview.value?.readAt), refresh })
const definitions: { id: keyof WorkbenchOverview['groups']; label: string; query: LocationQueryRaw }[] = [
  { id: 'attention', label: '待确认与核实', query: { attention: 'any', progressBasis: 'effective', showClosed: 'true' } },
  { id: 'blocked', label: '需要解除阻塞', query: { status: 'blocked', progressBasis: 'effective' } },
  { id: 'doing', label: '进行中', query: { status: 'doing', progressBasis: 'effective' } },
  { id: 'source', label: '来源异常', query: { sourceProblems: 'true', progressBasis: 'effective', showClosed: 'true' } }
]
const groups = computed(() => definitions.map(({ id, label, query }) => ({
  id, label, query,
  total: overview.value?.groups[id]?.total ?? 0,
  items: overview.value?.groups[id]?.items ?? []
})))
const focus = computed(() =>
  groups.value.find((group) => group.id === route.query.focus) ??
  groups.value.find((group) => group.total > 0) ??
  groups.value[0]!
)
const progressItems = computed(() => focus.value.items)
const recent = computed(() => overview.value?.recent ?? [])
const recentSpecList = { path: '/specs', query: { progressBasis: 'effective', showClosed: 'true', sort: 'updatedDesc' } }
const activity = computed(() => overview.value?.activity ?? [])
function target(event: ChangeEvent) {
  const title = event.specId
    ? catalog.value?.specs.find((spec) => spec.id === event.specId)?.title
    : catalog.value?.projects.find((project) => project.id === event.projectId)?.name
  return event.lifecycleStage
    ? `${title ?? '未知记录'} · ${stages[event.lifecycleStage]}阶段`
    : (title ?? '未知记录')
}
function selectFocus(value: string | number) {
  if (typeof value === 'string' && value !== route.query.focus)
    void router.replace({ query: { ...route.query, focus: value } })
}
</script>
<template>
  <div class="workbench-home">
    <header class="home-heading">
      <div class="home-identity">
        <span class="home-brand-mark" aria-hidden="true">Y</span>
        <div class="home-heading-copy">
          <h1>工作台</h1>
          <p class="home-meta">
            <span>全部工程</span>
            <template v-if="overview?.readAt">
              <span aria-hidden="true">·</span>
              <span>最近读取 <time :datetime="overview.readAt">{{ dateLabel(overview.readAt) }}</time></span>
            </template>
          </p>
        </div>
      </div>
      <div class="inline-actions home-actions">
        <Button type="button" variant="outline" :disabled="refreshDisabled" @click="refreshPage">
          <RefreshCw v-if="!refreshBusy" aria-hidden="true" />
          <LoadingIndicator :active="refreshBusy" />
          {{ refreshBusy ? '刷新中…' : '刷新' }}
        </Button>
        <Button as-child variant="ghost"><RouterLink to="/projects">工程管理</RouterLink></Button>
        <Button as-child><RouterLink to="/specs">全部 Spec <ArrowRight aria-hidden="true" /></RouterLink></Button>
      </div>
    </header>
    <Alert v-if="catalogError" class="notice warning" role="status">
      工程目录读取失败。{{ catalogError }}
      <Button type="button" variant="outline" @click="refreshCatalog()">重试</Button>
    </Alert>
    <Alert v-if="error || verification === 'failed'" role="alert" class="notice error">
      {{ error || overview?.verificationError || '工作台进度核对失败。' }}
      <span v-if="overview?.projectionVersion">当前显示上次核实结果。</span>
      <Button type="button" variant="outline" :disabled="loading" @click="refresh">重试</Button>
    </Alert>
    <p v-else-if="verification === 'stale'" class="home-verification muted" role="status">正在核对变化，当前显示上次核实结果。</p>
    <LoadingSkeleton v-if="showPlaceholder" variant="cards" :rows="3" label="正在读取工作台概览" />
    <RegisteredSpecs v-if="!ready && catalog && registered.length" :projects="catalog.projects" :specs="registered" :limit="5"
      :failed="!!error || verification === 'failed'" />
    <Empty v-if="overview?.projectCount === 0" class="panel home-welcome">
      <EmptyHeader>
        <EmptyTitle role="heading" aria-level="2">把本机 Spec 汇集到这里</EmptyTitle>
        <EmptyDescription>先登记工程，再从 Markdown 文件中选择要跟踪的 Spec。</EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button as-child><RouterLink to="/projects">登记第一个工程 <ArrowRight aria-hidden="true" /></RouterLink></Button>
      </EmptyContent>
    </Empty>
    <div class="home-inbox">
      <ContentAttention v-if="ready" :reminders="overview?.reminders ?? {}" />
      <ActiveJobs />
    </div>
    <div v-if="ready" class="home-layout">
      <section class="panel progress-section" aria-labelledby="progress-heading">
        <div class="home-panel-heading">
          <h2 id="progress-heading">Spec 推进</h2>
          <RouterLink class="home-panel-link" :to="{ path: '/specs', query: focus.query }" :aria-label="`查看筛选列表 · ${focus.label}`">
            查看筛选列表 <ArrowRight :size="14" aria-hidden="true" />
          </RouterLink>
        </div>
        <Tabs class="home-tab-panel" :model-value="focus.id" activation-mode="manual" @update:model-value="selectFocus">
          <TabsList class="progress-filters home-tabs" aria-label="Spec 推进范围">
            <TabsTrigger v-for="group in groups" :key="group.id" :value="group.id">
              {{ group.label }}
              <Badge variant="secondary" class="tab-count">{{ group.total }}</Badge>
            </TabsTrigger>
          </TabsList>
          <TabsContent v-for="group in groups" :key="group.id" :value="group.id" class="min-w-0">
            <div v-if="progressItems.length">
              <WorkItemRows :items="progressItems" :caption="focus.label + ' Spec'" :refreshing="loading && !!progressItems.length" />
              <p v-if="progressItems.length < focus.total" class="muted px-4 py-2">
                已显示 {{ progressItems.length }} / {{ focus.total }} 个 Spec
              </p>
            </div>
            <Empty v-else class="quiet-empty">
              <EmptyDescription>
                {{
                  focus.id === 'attention'
                    ? '当前没有待确认或待核实事项'
                    : focus.id === 'blocked'
                      ? '当前没有阻塞事项'
                      : focus.id === 'doing'
                        ? '当前没有进行中的 Spec'
                        : '当前来源均可访问'
                }}
              </EmptyDescription>
            </Empty>
          </TabsContent>
        </Tabs>
      </section>
      <aside v-if="recent.length || activity.length" class="panel recent-panels" aria-label="近期动态">
        <section v-if="recent.length" class="recent-panel" aria-labelledby="recent-heading">
          <header class="recent-heading">
            <FileClock :size="15" aria-hidden="true" />
            <h2 id="recent-heading">最近更新的规格</h2>
          </header>
          <ul class="compact-records">
            <li v-for="spec in recent" :key="spec.id">
              <RouterLink
                :to="{ path: `/specs/${spec.id}`, query: { return: route.fullPath } }"
              >
                {{ spec.title }}
              </RouterLink>
              <time :datetime="spec.updatedAt">
                {{ dateLabel(spec.updatedAt) }}
              </time>
            </li>
          </ul>
          <RouterLink v-if="overview && recent.length < overview.activeSpecCount" :to="recentSpecList" class="home-panel-link recent-list-link">
            查看完整规格列表 <ArrowRight :size="14" aria-hidden="true" />
          </RouterLink>
        </section>
        <section v-if="activity.length" class="recent-panel" aria-labelledby="activity-heading">
          <header class="recent-heading">
            <History :size="15" aria-hidden="true" />
            <h2 id="activity-heading">最近操作</h2>
          </header>
          <ol class="compact-records activity-records">
            <li v-for="event in activity" :key="event.id">
              <RouterLink
                :to="{
                  path: event.specId ? `/specs/${event.specId}` : `/projects/${event.projectId}`,
                  query: { return: route.fullPath }
                }"
              >
                {{ target(event) }}
              </RouterLink>
              <div class="record-meta">
                <span>{{ eventDescription(event) }}</span>
                <time :datetime="event.at">{{ dateLabel(event.at) }}</time>
              </div>
            </li>
          </ol>
        </section>
      </aside>
    </div>
  </div>
</template>
<style scoped>
.workbench-home {
  container: workbench-home / inline-size;
  max-inline-size: 1320px;
  margin-inline: auto;
  padding-block: 6px 24px;
}
.home-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px 20px;
  margin-block-end: 18px;
}
.home-identity {
  display: flex;
  align-items: center;
  gap: 12px;
  min-inline-size: 0;
}
.home-heading-copy { min-inline-size: 0; }
.home-brand-mark {
  display: grid;
  flex: none;
  inline-size: 36px;
  block-size: 36px;
  place-items: center;
  border-radius: 9px;
  background: var(--yss-colors-text-title);
  color: var(--brand-color-bg-container);
  font-size: 16px;
  font-weight: 700;
}
.home-heading h1 { line-height: 1.2; }
.home-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 7px;
  margin-block-start: 4px;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.home-actions { gap: 6px; }
.home-actions [data-slot='button'] { padding-inline: 10px; }
.home-verification {
  margin-block: 0 12px;
  font-size: 12px;
}
.home-welcome {
  gap: 16px;
  padding: 28px 16px;
  margin-block-end: 16px;
}
.home-inbox,
.home-layout {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.home-inbox { margin-block-end: 16px; }
.home-inbox > :deep(section) {
  min-inline-size: 0;
  margin-block: 0;
}
.home-inbox:has(> .home-status + .home-status) {
  gap: 0;
  border: 1px solid var(--workbench-glass-edge);
  border-radius: var(--brand-border-radius-lg);
  background: var(--workbench-glass-pane);
}
.home-inbox :deep(.home-status-row) {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 14px;
  min-block-size: 56px;
}
.home-inbox :deep(.home-status + .home-status) { border-block-start: 1px solid var(--yss-colors-border); }
.home-inbox :deep(.home-status-icon) {
  flex: none;
  inline-size: 18px;
  block-size: 18px;
  color: var(--brand-color-text-secondary);
}
.home-inbox :deep(.home-status-copy) {
  flex: 1;
  min-inline-size: 0;
}
.home-inbox :deep(.home-status-copy h2) { font-size: 13px; }
.home-inbox :deep(.home-status-copy p) {
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.home-panel-link,
.home-inbox :deep(.home-status-link) {
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: 4px;
  min-block-size: 32px;
  color: var(--yss-color-primary-control-active);
  font-size: 12px;
}
.home-panel-link:hover,
.home-inbox :deep(.home-status-link:hover) { text-decoration: underline; }
.home-inbox :deep(.home-status-link svg) {
  inline-size: 14px;
  block-size: 14px;
}
.home-inbox :deep(.section-heading) {
  margin-block-end: 6px;
  min-block-size: 25px;
}
.home-inbox :deep(.section-heading > a) {
  flex: none;
  padding-block: 3px;
}
.progress-section {
  container: home-progress / inline-size;
  min-inline-size: 0;
}
.home-panel-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 7px 14px 2px;
}
.home-panel-heading h2 { color: var(--yss-colors-text-title); }
.workbench-home :deep(.home-tab-panel) { gap: 0; }
.workbench-home :deep(.home-tabs) {
  display: flex;
  justify-content: flex-start;
  inline-size: 100%;
  block-size: auto;
  gap: 4px;
  padding: 4px 8px 6px;
  border-block-end: 1px solid var(--yss-colors-border);
  border-radius: 0;
  background: transparent;
  overflow-x: auto;
  overscroll-behavior-x: contain;
  scrollbar-width: thin;
}
.workbench-home :deep(.home-tabs [data-slot='tabs-trigger']) {
  flex: 0 0 auto;
  min-block-size: 30px;
  padding-inline: 8px;
}
.workbench-home :deep(.home-tabs [aria-selected='true']) {
  color: var(--yss-color-primary-control-active);
  background-color: var(--yss-colors-primary-bg);
  box-shadow: none;
}
.workbench-home :deep(.home-tabs [aria-selected='true'] .tab-count) {
  color: inherit;
  background-color: var(--brand-color-bg-container);
}
.workbench-home :deep(.home-tab-panel > [data-slot='tabs-content']:focus-visible) { outline-offset: -2px; }
.workbench-home :deep(.tab-count) {
  min-inline-size: 20px;
  font-variant-numeric: tabular-nums;
}
.workbench-home :deep(.spec-table thead th) {
  height: 30px;
  padding-inline: 12px;
}
.workbench-home :deep(.spec-table tbody :is(th[scope='row'], td)) {
  height: 44px;
  padding-block: 8px;
  padding-inline: 12px;
}
.progress-section :deep(.work-items tbody tr),
.progress-section :deep(.work-items tbody th) { background: var(--brand-color-bg-elevated); }
.progress-section :deep(.work-items tbody tr:last-child th) { border-block-end: 0; }
.progress-section :deep(.work-items tbody tr:hover) { background: var(--brand-color-bg-layout); }
.progress-section :deep(.work-items tbody tr:hover th),
.progress-section :deep(.work-items tbody tr:hover td) { background: var(--brand-color-bg-layout); }
.quiet-empty {
  border: 0;
  padding: 20px 12px;
}
.recent-panels,
.recent-panel { min-inline-size: 0; }
.recent-panel {
  padding: 2px 14px 8px;
}
.recent-panel + .recent-panel { border-block-start: 1px solid var(--yss-colors-border); }
.recent-heading {
  display: flex;
  align-items: center;
  gap: 7px;
  padding-block: 12px 6px;
}
.recent-heading svg { flex: none; color: var(--brand-color-text-secondary); }
.recent-heading h2 { font-size: 13px; }
.recent-list-link { margin-block-start: 2px; }
.compact-records {
  list-style: none;
  margin: 0;
  padding: 0;
}
.compact-records li {
  display: grid;
  gap: 2px;
  padding-block: 7px;
}
.compact-records li + li { border-block-start: 1px solid var(--yss-colors-border); }
.compact-records a {
  justify-self: start;
  overflow-wrap: anywhere;
  font-size: 12px;
  font-weight: 550;
}
.compact-records a:hover { text-decoration: underline; }
.compact-records time,
.record-meta {
  color: var(--brand-color-text-secondary);
  font-size: 11px;
}
.record-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 8px;
}
@container workbench-home (inline-size >= 680px) {
  .home-inbox:has(> .home-status + .home-status),
  .recent-panels:has(> .recent-panel + .recent-panel) {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .home-inbox :deep(.home-status + .home-status),
  .recent-panel + .recent-panel {
    border-block-start: none;
    border-inline-start: 1px solid var(--yss-colors-border);
  }
}
@container workbench-home (inline-size >= 1160px) {
  .home-layout:has(.recent-panels) {
    display: grid;
    align-items: start;
    grid-template-columns: minmax(0, 1fr) clamp(280px, 24%, 320px);
  }
  .recent-panels:has(> .recent-panel + .recent-panel) { grid-template-columns: minmax(0, 1fr); }
  .recent-panel + .recent-panel {
    border-inline-start: none;
    border-block-start: 1px solid var(--yss-colors-border);
  }
}
@container home-progress (inline-size < 840px) {
  .progress-section :deep(.work-items) { min-width: 660px; }
  .progress-section :deep(.work-items .item-title) { min-width: 132px; max-width: 172px; white-space: normal; }
  .progress-section :deep(.work-items .item-progress) { min-width: 88px; }
  .progress-section :deep(.work-items .item-content) { min-width: 126px; }
  .progress-section :deep(.work-items .item-next) { min-width: 176px; }
  .progress-section :deep(.work-items .item-updated) { min-width: 138px; }
}
@container workbench-home (inline-size < 476px) {
  .workbench-home :deep(.attention-filters) {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
  .workbench-home :deep(.home-tabs [data-slot='tabs-trigger']) {
    min-inline-size: 0;
    block-size: auto;
    padding-inline: 5px;
    gap: 4px;
    white-space: normal;
    overflow-wrap: anywhere;
  }
}
@container home-progress (inline-size < 440px) {
  .progress-section :deep(.progress-filters) {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (width < 720px) {
  .workbench-home :deep(.home-tabs [data-slot='tabs-trigger']),
  .home-panel-link,
  .home-inbox :deep(.home-status-link) {
    min-block-size: var(--yss-control-min-height);
  }
  .workbench-home :deep(.spec-table thead th),
  .workbench-home :deep(.spec-table tbody :is(th[scope='row'], td)) {
    padding-inline: 8px;
  }
}
@media (forced-colors: active) {
  .workbench-home :deep(.home-tabs [aria-selected='true']) { border-color: Highlight; }
}
</style>
