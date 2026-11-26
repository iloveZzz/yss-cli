<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter, type LocationQueryRaw } from 'vue-router'
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
  <div class="page-heading">
    <h1>工作台</h1>
    <div class="inline-actions">
      <Button as-child variant="outline"><RouterLink to="/projects">工程管理</RouterLink></Button>
      <Button as-child variant="ghost"><RouterLink to="/specs">全部 Spec →</RouterLink></Button>
    </div>
  </div>
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
  <Empty v-if="overview?.projectCount === 0" class="panel welcome">
    <EmptyHeader>
      <EmptyTitle role="heading" aria-level="2">把本机 Spec 汇集到这里</EmptyTitle>
      <EmptyDescription>先登记工程，再从 Markdown 文件中选择要跟踪的 Spec。</EmptyDescription>
    </EmptyHeader>
    <EmptyContent>
      <Button as-child><RouterLink to="/projects">登记第一个工程 →</RouterLink></Button>
    </EmptyContent>
  </Empty>
  <ContentAttention v-else-if="ready" :reminders="overview?.reminders ?? {}" />
  <ActiveJobs />
  <template v-if="ready">
    <section class="work-section progress-section" aria-labelledby="progress-heading">
      <div class="section-heading">
        <h2 id="progress-heading">Spec 推进</h2>
        <RouterLink :to="{ path: '/specs', query: focus.query }">查看筛选列表 →</RouterLink>
      </div>
      <Tabs :model-value="focus.id" activation-mode="manual" @update:model-value="selectFocus">
        <TabsList class="progress-filters" aria-label="Spec 推进范围" moving-indicator>
          <TabsTrigger v-for="group in groups" :key="group.id" :value="group.id">
            {{ group.label }}
            <Badge variant="secondary" class="tab-count">{{ group.total }}</Badge>
          </TabsTrigger>
        </TabsList>
        <TabsContent v-for="group in groups" :key="group.id" :value="group.id" class="min-w-0">
          <Card v-if="progressItems.length" class="panel block gap-0 py-0 shadow-none">
            <WorkItemRows :items="progressItems" :caption="focus.label + ' Spec'" :refreshing="loading && !!progressItems.length" />
            <p v-if="progressItems.length < focus.total" class="muted px-4 py-2">
              已显示 {{ progressItems.length }} / {{ focus.total }} 项
            </p>
          </Card>
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
    <div v-if="recent.length || activity.length" class="recent-panels">
      <Card v-if="recent.length" class="panel recent-panel gap-0 py-0 shadow-none" role="region" aria-labelledby="recent-heading">
        <CardHeader class="recent-heading"><h2 id="recent-heading">最近访问</h2></CardHeader>
        <ul class="compact-records">
          <li v-for="spec in recent" :key="spec.id">
            <RouterLink
              :to="{ path: `/specs/${spec.id}`, query: { return: route.fullPath } }"
            >
              {{ spec.title }}
            </RouterLink>
            <time :datetime="spec.lastVisitedAt ?? undefined">
              {{ dateLabel(spec.lastVisitedAt) }}
            </time>
          </li>
        </ul>
      </Card>
      <Card v-if="activity.length" class="panel recent-panel gap-0 py-0 shadow-none" role="region" aria-labelledby="activity-heading">
        <CardHeader class="recent-heading"><h2 id="activity-heading">最近操作</h2></CardHeader>
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
  </template>
</template>
<style scoped>
.progress-section {
  margin-top: 24px;
}
.progress-filters {
  display: flex;
  justify-content: flex-start;
  width: 100%;
  height: auto;
  gap: 4px;
  padding: 4px;
  overflow-x: auto;
  overscroll-behavior-x: contain;
}
.progress-filters :deep([data-slot='tabs-trigger']) {
  flex: 0 0 auto;
  min-height: var(--yss-control-min-height);
  padding-inline: 10px;
}
.tab-count {
  min-width: 20px;
  font-variant-numeric: tabular-nums;
}
.quiet-empty {
  border: 0;
  padding: 18px 10px;
}
.recent-panels {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 20px;
  align-items: start;
  margin: 24px 0;
}
.recent-panel {
  min-width: 0;
}
.recent-heading {
  padding: 16px 18px 10px;
}
.compact-records {
  list-style: none;
  margin: 0;
  padding: 0 18px;
}
.compact-records li {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  padding: 11px 0;
  border-top: 1px solid #edf0f4;
}
.compact-records a {
  min-width: 0;
  overflow-wrap: anywhere;
  font-size: 12px;
}
.compact-records time {
  flex: none;
  color: var(--brand-color-text-secondary);
  font-size: 10px;
  white-space: nowrap;
}
.activity-records li > div {
  min-width: 0;
}
.activity-records p {
  margin-top: 4px;
  color: var(--brand-color-text-secondary);
  font-size: 11px;
  overflow-wrap: anywhere;
}
@media (width<900px) {
  .recent-panels {
    grid-template-columns: 1fr;
    gap: 16px;
  }
}
@media (width<720px) {
  .page-heading {
    flex-wrap: wrap;
  }
  .progress-filters {
    flex-wrap: wrap;
  }
  .compact-records li {
    flex-wrap: wrap;
    gap: 5px;
  }
}
</style>
