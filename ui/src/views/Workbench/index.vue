<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardHeader } from '@/components/ui/card'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import WorkItemRows from '@/components/WorkItemRows.vue'
import ContentAttention from './components/ContentAttention.vue'
import ActiveJobs from './components/ActiveJobs.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  dateLabel,
  hasSourceProblem,
  stages,
  type ChangeEvent,
  type WorkItem
} from '@/services/workbench'
import { contentReminders, materialNeedsReview } from '@/services/workbench-content'
import { eventDescription } from '@/services/workbench-history'
const { activeSpecs, loading, snapshot } = useWorkbench()
const route = useRoute()
const router = useRouter()
const progressFor = (id: string) => snapshot.value?.progress?.[id]
const doing = computed(() => activeSpecs.value.filter((spec) => progressFor(spec.record.id)?.status === 'doing'))
const blocked = computed(() => activeSpecs.value.filter((spec) => progressFor(spec.record.id)?.status === 'blocked'))
const attention = computed(() => activeSpecs.value.filter((spec) => !!progressFor(spec.record.id)?.attention.length))
const problems = computed(() => activeSpecs.value.filter(hasSourceProblem))
const groups = computed(() => [
  { id: 'attention', label: '待确认与核实', specs: attention.value, query: { attention: ['awaitingConfirmation', 'unverified', 'needsSync', 'needsReview', 'conflict'], progressBasis: 'effective' } },
  { id: 'blocked', label: '需要解除阻塞', specs: blocked.value, query: { status: 'blocked', progressBasis: 'effective' } },
  { id: 'doing', label: '进行中', specs: doing.value, query: { status: 'doing', progressBasis: 'effective' } },
  { id: 'source', label: '来源异常', specs: problems.value, query: { sourceProblems: 'true', progressBasis: 'effective' } }
])
const focus = computed(
  () =>
    groups.value.find((group) => group.id === route.query.focus) ??
    groups.value.find((group) => group.specs.length) ??
    groups.value[0]!
)
const progressItems = computed<WorkItem[]>(() =>
  focus.value.specs.map((spec) => ({
    entity: 'spec',
    id: spec.record.id,
    projectId: spec.record.projectId,
    projectName:
      snapshot.value?.projects.find((project) => project.record.id === spec.record.projectId)
        ?.record.name ?? '未知工程',
    specId: spec.record.id,
    specTitle: spec.record.title,
    sourcePath: spec.record.sourcePath,
    title: spec.record.title,
    progress: progressFor(spec.record.id),
    stage: progressFor(spec.record.id)?.primaryStage ?? 'unmarked',
    status: progressFor(spec.record.id)?.status ?? 'unmarked',
    nextAction: progressFor(spec.record.id)?.nextAction ?? '',
    blocker: progressFor(spec.record.id)?.blocker ?? '',
    notes: spec.record.notes,
    active: spec.record.active,
    updatedAt: spec.record.updatedAt,
    hasSourceProblem: hasSourceProblem(spec),
    contentStatus: spec.content.status,
    reminderCounts: Object.fromEntries(
      contentReminders(spec.content).map((reminder) => [reminder.id, reminder.count])
    ),
    needsReconfirmation: spec.content.needsReconfirmation,
    materialReview: materialNeedsReview(spec.content)
  }))
)
const recent = computed(() =>
  activeSpecs.value
    .filter((spec) => spec.record.lastVisitedAt)
    .toSorted((left, right) =>
      (right.record.lastVisitedAt ?? '').localeCompare(left.record.lastVisitedAt ?? '')
    )
    .slice(0, 5)
)
const activity = computed(() =>
  (snapshot.value?.history ?? [])
    .filter((event) => !event.taskId)
    .toReversed()
    .slice(0, 8)
)
function target(event: ChangeEvent) {
  const parent = event.specId
    ? snapshot.value?.specs.find((spec) => spec.record.id === event.specId)?.record.title
    : snapshot.value?.projects.find((project) => project.record.id === event.projectId)?.record.name
  return event.lifecycleStage
    ? `${parent ?? '未知记录'} · ${stages[event.lifecycleStage]}阶段`
    : (parent ?? '未知记录')
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
  <Empty v-if="!snapshot?.projects.length" class="panel welcome">
    <EmptyHeader>
      <EmptyTitle role="heading" aria-level="2">把本机 Spec 汇集到这里</EmptyTitle>
      <EmptyDescription>先登记工程，再从 Markdown 文件中选择要跟踪的 Spec。</EmptyDescription>
    </EmptyHeader>
    <EmptyContent>
      <Button as-child><RouterLink to="/projects">登记第一个工程 →</RouterLink></Button>
    </EmptyContent>
  </Empty>
  <ContentAttention v-else :specs="activeSpecs" />
  <ActiveJobs />
  <template v-if="snapshot?.projects.length">
    <section class="work-section progress-section" aria-labelledby="progress-heading">
      <div class="section-heading">
        <h2 id="progress-heading">Spec 推进</h2>
        <RouterLink :to="{ path: '/specs', query: focus.query }">查看筛选列表 →</RouterLink>
      </div>
      <Tabs :model-value="focus.id" activation-mode="manual" @update:model-value="selectFocus">
        <TabsList class="progress-filters" aria-label="Spec 推进范围" moving-indicator>
          <TabsTrigger v-for="group in groups" :key="group.id" :value="group.id">
            {{ group.label }}
            <Badge variant="secondary" class="tab-count">{{ group.specs.length }}</Badge>
          </TabsTrigger>
        </TabsList>
        <TabsContent v-for="group in groups" :key="group.id" :value="group.id" class="min-w-0">
          <Card v-if="progressItems.length" class="panel block gap-0 py-0 shadow-none">
            <WorkItemRows :items="progressItems" :caption="focus.label + ' Spec'" :refreshing="loading" />
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
          <li v-for="spec in recent" :key="spec.record.id">
            <RouterLink
              :to="{ path: `/specs/${spec.record.id}`, query: { return: route.fullPath } }"
            >
              {{ spec.record.title }}
            </RouterLink>
            <time :datetime="spec.record.lastVisitedAt ?? undefined">
              {{ dateLabel(spec.record.lastVisitedAt) }}
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
