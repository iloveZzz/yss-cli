<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter, type LocationQueryRaw } from 'vue-router'
import { FileClock, History } from '@lucide/vue'
import { Alert } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardHeader } from '@/components/ui/card'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import RegisteredSpecs from '@/components/RegisteredSpecs.vue'
import WorkItemRows from '@/components/WorkItemRows.vue'
import ContentAttention from './components/ContentAttention.vue'
import ActiveJobs from './components/ActiveJobs.vue'
import { registerWorkbenchRouteReads, useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'
import { dateLabel, stages, type ChangeEvent } from '@/services/workbench'
import { eventDescription } from '@/services/workbench-history'
import type { WorkbenchOverview } from '@/services/workbench-read'
import { useWorkbenchOverview } from './hooks/useWorkbenchOverview'

const { overview, loading, error, verification, refresh } = useWorkbenchOverview()
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
  { id: 'attention', label: '待确认与核实', query: { attention: ['awaitingConfirmation', 'unverified', 'needsSync', 'needsReview', 'conflict'], progressBasis: 'effective', showClosed: 'true' } },
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
    <header class="page-heading home-heading">
      <h1>工作台</h1>
      <div class="inline-actions">
        <Button as-child variant="outline"><RouterLink to="/projects">工程管理</RouterLink></Button>
        <Button as-child variant="ghost"><RouterLink to="/specs">全部 Spec →</RouterLink></Button>
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
    <p v-else-if="verification === 'stale'" class="muted" role="status">正在核对变化，当前显示上次核实结果。</p>
    <LoadingSkeleton v-if="showPlaceholder" variant="cards" :rows="3" label="正在读取工作台概览" />
    <RegisteredSpecs v-if="!ready && catalog && registered.length" :projects="catalog.projects" :specs="registered"
      :failed="!!error || verification === 'failed'" />
    <Empty v-if="overview?.projectCount === 0" class="panel home-welcome">
      <EmptyHeader>
        <EmptyTitle role="heading" aria-level="2">把本机 Spec 汇集到这里</EmptyTitle>
        <EmptyDescription>先登记工程，再从 Markdown 文件中选择要跟踪的 Spec。</EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button as-child><RouterLink to="/projects">登记第一个工程 →</RouterLink></Button>
      </EmptyContent>
    </Empty>
    <div class="home-layout">
      <div class="home-priority">
        <ContentAttention v-if="ready" :reminders="overview?.reminders ?? {}" />
        <ActiveJobs />
        <section v-if="ready" class="work-section progress-section" aria-labelledby="progress-heading">
          <div class="section-heading">
            <h2 id="progress-heading">Spec 推进</h2>
            <RouterLink :to="{ path: '/specs', query: focus.query }">查看筛选列表 →</RouterLink>
          </div>
          <Tabs class="panel home-tab-panel" :model-value="focus.id" activation-mode="manual" @update:model-value="selectFocus">
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
                  已显示 {{ progressItems.length }} / {{ focus.total }} 项
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
      </div>
      <div v-if="ready && (recent.length || activity.length)" class="recent-panels">
        <Card v-if="recent.length" class="panel recent-panel gap-0 py-0 shadow-none" role="region" aria-labelledby="recent-heading">
          <CardHeader class="recent-heading">
            <FileClock :size="15" aria-hidden="true" />
            <h2 id="recent-heading">最近更新的规格</h2>
          </CardHeader>
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
        </Card>
        <Card v-if="activity.length" class="panel recent-panel gap-0 py-0 shadow-none" role="region" aria-labelledby="activity-heading">
          <CardHeader class="recent-heading">
            <History :size="15" aria-hidden="true" />
            <h2 id="activity-heading">最近操作</h2>
          </CardHeader>
          <ol class="compact-records activity-records">
            <li v-for="event in activity" :key="event.id">
              <div>
                <RouterLink
                  :to="{
                    path: event.specId ? `/specs/${event.specId}` : `/projects/${event.projectId}`,
                    query: { return: route.fullPath }
                  }"
                >
                  {{ target(event) }}
                </RouterLink>
                <p>{{ eventDescription(event) }}</p>
              </div>
              <time :datetime="event.at">{{ dateLabel(event.at) }}</time>
            </li>
          </ol>
        </Card>
      </div>
    </div>
  </div>
</template>
<style scoped>
.workbench-home {
  container: workbench-home / inline-size;
  max-inline-size: 1440px;
  margin-inline: auto;
}
.home-heading {
  margin-block-end: 12px;
  min-block-size: var(--yss-control-min-height);
  flex-wrap: nowrap;
}
.home-heading .inline-actions {
  margin-block-start: 0;
  gap: 6px;
}
.home-welcome {
  gap: 16px;
  padding: 28px 16px;
  margin-block-end: 16px;
}
.home-layout,
.home-priority,
.recent-panels {
  display: grid;
  min-inline-size: 0;
  gap: 16px;
}
.home-layout { align-items: start; }
.home-priority { grid-template-columns: minmax(0, 1fr); }
.home-priority > :deep(section) {
  min-inline-size: 0;
  margin-block: 0;
}
.home-priority :deep(.section-heading) {
  margin-block-end: 6px;
  min-block-size: 25px;
}
.home-priority :deep(.section-heading > a) {
  flex: none;
  padding-block: 3px;
}
.home-priority :deep(.home-tabs) {
  display: flex;
  justify-content: flex-start;
  inline-size: 100%;
  block-size: auto;
  gap: 3px;
  padding: 4px 6px;
  border-block-end: 1px solid var(--yss-colors-border);
  border-radius: 0;
  background: transparent;
  overflow-x: auto;
  overscroll-behavior-x: contain;
  scrollbar-width: thin;
}
.home-priority :deep(.home-tabs [data-slot='tabs-trigger']) {
  flex: 0 0 auto;
  min-block-size: 30px;
  padding-inline: 8px;
}
.home-priority :deep(.home-tabs [aria-selected='true']) {
  color: #315dcf;
  background-color: var(--yss-colors-primary-bg);
  box-shadow: none;
}
.home-priority :deep(.home-tabs [aria-selected='true'] .tab-count) {
  color: inherit;
  background-color: var(--brand-color-bg-container);
}
.home-priority :deep(.home-tab-panel > [data-slot='tabs-content']:focus-visible) {
  outline-offset: -2px;
}
.home-priority :deep(.tab-count) {
  min-inline-size: 20px;
  font-variant-numeric: tabular-nums;
}
.home-priority :deep(.spec-table thead th) {
  height: 30px;
  padding-inline: 12px;
}
.home-priority :deep(.spec-table tbody :is(th[scope='row'], td)) {
  height: 44px;
  padding-block: 8px;
  padding-inline: 12px;
}
.quiet-empty {
  border: 0;
  padding: 14px 10px;
}
.recent-panels {
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr));
  align-items: start;
  gap: 12px;
}
.recent-panel { min-width: 0; }
.recent-heading {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 10px 12px 8px;
}
.recent-heading svg { color: var(--brand-color-text-secondary); }
.recent-heading h2 { font-size: 13px; }
.compact-records {
  list-style: none;
  margin: 0;
  padding-inline: 12px;
}
.compact-records li {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: 3px 8px;
  padding-block: 7px;
  border-block-start: 1px solid var(--yss-colors-border);
}
.compact-records li > :first-child {
  flex: 1 1 8ch;
  min-inline-size: 0;
}
.compact-records a {
  overflow-wrap: anywhere;
  font-size: 12px;
}
.compact-records time {
  flex: none;
  color: var(--brand-color-text-secondary);
  font-size: 11px;
  white-space: nowrap;
}
.activity-records p {
  margin-block-start: 2px;
  color: var(--brand-color-text-secondary);
  font-size: 11px;
  overflow-wrap: anywhere;
}
@container workbench-home (inline-size >= 1160px) {
  .home-layout:has(.recent-panels) {
    grid-template-columns: minmax(0, 1fr) 264px;
  }
  .recent-panels {
    margin-block-start: 31px;
  }
}
@container workbench-home (inline-size < 840px) {
  .home-priority :deep(.work-items) { min-width: 660px; }
  .home-priority :deep(.work-items .item-title) { min-width: 132px; max-width: 172px; white-space: normal; }
  .home-priority :deep(.work-items .item-progress) { min-width: 88px; }
  .home-priority :deep(.work-items .item-content) { min-width: 126px; }
  .home-priority :deep(.work-items .item-next) { min-width: 176px; }
  .home-priority :deep(.work-items .item-updated) { min-width: 138px; }
}
@container workbench-home (inline-size < 476px) {
  .home-priority :deep(.attention-filters) {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
  .home-priority :deep(.home-tabs [data-slot='tabs-trigger']) {
    min-inline-size: 0;
    block-size: auto;
    padding-inline: 5px;
    gap: 4px;
    white-space: normal;
    overflow-wrap: anywhere;
  }
}
@container workbench-home (inline-size < 440px) {
  .home-priority :deep(.progress-filters) {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (width < 720px) {
  .home-heading { flex-wrap: wrap; }
  .home-priority :deep(.home-tabs [data-slot='tabs-trigger']) {
    min-block-size: var(--yss-control-min-height);
  }
  .home-priority :deep(.spec-table thead th),
  .home-priority :deep(.spec-table tbody :is(th[scope='row'], td)) {
    padding-inline: 8px;
  }
}
@media (forced-colors: active) {
  .home-priority :deep(.home-tabs [aria-selected='true']) {
    border-color: Highlight;
  }
}
</style>
