<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
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
import { contentReminders } from '@/services/workbench-content'
import { eventDescription } from '@/services/workbench-history'
const { activeSpecs, snapshot } = useWorkbench()
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
    needsReconfirmation: spec.content.needsReconfirmation
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
function selectFocus(value: string) {
  void router.replace({ query: { ...route.query, focus: value } })
}
</script>
<template>
  <div class="page-heading">
    <h1>工作台</h1>
    <div class="inline-actions">
      <RouterLink to="/projects" class="text-link">工程管理</RouterLink>
      <RouterLink to="/specs" class="text-link">全部 Spec →</RouterLink>
    </div>
  </div>
  <div v-if="!snapshot?.projects.length" class="empty panel welcome">
    <h2>把本机 Spec 汇集到这里</h2>
    <p>先登记工程，再从 Markdown 文件中选择要跟踪的 Spec。</p>
    <RouterLink to="/projects" class="primary-link">登记第一个工程 →</RouterLink>
  </div>
  <template v-else>
    <ContentAttention :specs="activeSpecs" />
    <ActiveJobs />
    <section class="work-section progress-section" aria-labelledby="progress-heading">
      <div class="section-heading">
        <h2 id="progress-heading">Spec 推进</h2>
        <RouterLink :to="{ path: '/specs', query: focus.query }">查看筛选列表 →</RouterLink>
      </div>
      <div class="progress-filters" role="group" aria-label="Spec 推进范围">
        <button
          v-for="group in groups"
          :key="group.id"
          type="button"
          :aria-pressed="focus.id === group.id"
          @click="selectFocus(group.id)"
        >
          {{ group.label }}
          <span>{{ group.specs.length }}</span>
        </button>
      </div>
      <div v-if="progressItems.length" class="panel">
        <WorkItemRows :items="progressItems" :caption="focus.label + ' Spec'" />
      </div>
      <p v-else class="quiet-empty">
        {{
          focus.id === 'attention'
            ? '当前没有待确认或待核实事项'
            : focus.id === 'blocked'
            ? '当前没有阻塞事项'
            : focus.id === 'doing'
              ? '当前没有进行中的 Spec'
              : '当前来源均可访问'
        }}
      </p>
    </section>
    <div v-if="recent.length || activity.length" class="recent-panels">
      <section v-if="recent.length" class="panel recent-panel" aria-labelledby="recent-heading">
        <div class="recent-heading"><h2 id="recent-heading">最近访问</h2></div>
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
      </section>
      <section v-if="activity.length" class="panel recent-panel" aria-labelledby="activity-heading">
        <div class="recent-heading"><h2 id="activity-heading">最近操作</h2></div>
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
      </section>
    </div>
  </template>
</template>
<style scoped>
.progress-section {
  margin-top: 24px;
}
.progress-filters {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-bottom: 12px;
  border-bottom: 1px solid var(--yss-colors-border);
}
.progress-filters button {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 38px;
  padding: 7px 12px;
  border: 0;
  border-bottom: 2px solid transparent;
  background: none;
  color: #717780;
  font-size: 12px;
}
.progress-filters button[aria-pressed='true'] {
  border-bottom-color: var(--yss-color-primary-control);
  color: var(--yss-color-primary-control);
}
.progress-filters span {
  font-size: 11px;
  color: #858d99;
}
.quiet-empty {
  padding: 14px 0;
  color: #717780;
  font-size: 12px;
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
  color: #939aa5;
  font-size: 10px;
  white-space: nowrap;
}
.activity-records li > div {
  min-width: 0;
}
.activity-records p {
  margin-top: 4px;
  color: #858d99;
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
  .progress-filters button {
    min-height: 44px;
    padding-inline: 9px;
  }
  .compact-records li {
    flex-wrap: wrap;
    gap: 5px;
  }
}
</style>
