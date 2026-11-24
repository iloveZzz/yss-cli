<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Alert } from '@/components/ui/alert'
import { computed, shallowRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Empty, EmptyContent, EmptyTitle } from '@/components/ui/empty'
import { FieldLabel } from '@/components/ui/field'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import WorkItemRows from '@/components/WorkItemRows.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { defaultWorkQuery, isClosed } from '@/services/workbench'
import { useWorkItems } from '@/views/Specs/hooks/useWorkItems'
import { encodeWorkQuery } from '@/views/Specs/schemas/filter'
import ProjectForm from '@/views/Projects/components/ProjectForm.vue'
import DiscoverSpecs from './components/DiscoverSpecs.vue'
import EngineeringPanel from '@/components/EngineeringPanel.vue'
import ExternalLinks from '@/components/ExternalLinks.vue'
const route = useRoute()
const router = useRouter()
const { snapshot } = useWorkbench()
const edit = shallowRef(false)
const sections = [
  { value: 'specs', label: 'Spec 与文件' },
  { value: 'maintenance', label: '工程维护' },
  { value: 'external', label: '研发关联' }
]
const section = computed(() =>
  sections.some((item) => item.value === route.query.section)
    ? String(route.query.section)
    : 'specs'
)
function selectSection(value: string | number) {
  if (typeof value === 'string' && value !== route.query.section)
    void router.push({ query: { ...route.query, section: value } })
}
const showInactive = computed({
  get: () => route.query.activity === 'all',
  set: (value) => {
    void router.replace({ query: { ...route.query, activity: value ? 'all' : undefined } })
  }
})
const showClosed = computed({
  get: () => route.query.showClosed === 'true',
  set: (value) => {
    void router.replace({ query: { ...route.query, showClosed: value ? 'true' : undefined } })
  }
})
const project = computed(() =>
  snapshot.value?.projects.find((p) => p.record.id === route.params.projectId)
)
const specs = computed(
  () => snapshot.value?.specs.filter((s) => s.record.projectId === project.value?.record.id) ?? []
)
const query = computed(() => ({
  ...defaultWorkQuery(),
  projectIds: [String(route.params.projectId)],
  activity: showInactive.value ? ('all' as const) : ('active' as const),
  includeClosed: showClosed.value
}))
const { result, resultsCurrent, loading, error, refresh } = useWorkItems(query)
const projectResult = computed(() => {
  const previous = result.value
  if (!previous || resultsCurrent.value) return previous
  const projectId = project.value?.record.id
  const sameProject =
    previous.items.length > 0 && previous.items.every((item) => item.projectId === projectId)
  return sameProject ? previous : undefined
})
const totals = computed(() => {
  const entries = specs.value.map((s) => ({
    active: !!project.value?.record.active && s.record.active,
    status: snapshot.value?.progress?.[s.record.id]?.status ?? 'unmarked'
  }))
  return {
    total: entries.length,
    inactive: entries.filter((e) => !e.active).length,
    closed: entries.filter((e) => e.active && isClosed(e.status)).length
  }
})
const allLink = computed(() => ({
  path: '/specs',
  query: encodeWorkQuery({ ...query.value, activity: 'all', includeClosed: true }, 'list', 'status')
}))
</script>
<template>
  <template v-if="project">
    <div class="page-heading">
      <div>
        <RouterLink to="/projects" class="back-link">← 产品与工程</RouterLink>
        <h1>{{ project.record.name }}</h1>
        <p class="path-text">{{ project.record.rootPath }}</p>
      </div>
      <Button variant="outline" @click="edit = true">编辑项目</Button>
    </div>
    <Alert v-if="!project.available || !project.record.active" class="notice warning">
      {{
        !project.record.active
          ? '项目已停用，记录保留。可通过“编辑项目”恢复。'
          : `目录不可访问：${project.message}。可重新定位项目根。`
      }}
    </Alert>
    <Tabs :model-value="section" :unmount-on-hide="false" activation-mode="manual" @update:model-value="selectSection">
      <TabsList class="detail-navigation" aria-label="工程详情分组">
        <TabsTrigger v-for="item in sections" :key="item.value" :value="item.value">{{ item.label }}</TabsTrigger>
      </TabsList>
      <TabsContent value="specs" class="min-w-0">
        <div class="section-heading">
          <h2>已纳管 Spec</h2>
          <RouterLink :to="allLink">查看项目全部记录 →</RouterLink>
        </div>
        <div class="toolbar project-spec-toolbar">
          <span class="project-spec-count">
            共 {{ totals.total }} 项 · 启用且已关闭 {{ totals.closed }} · 停用 {{ totals.inactive }}
          </span>
          <FieldLabel for="project-spec-inactive" class="check-label has-data-[state=checked]:bg-transparent has-data-[state=checked]:border-transparent">
            <Checkbox id="project-spec-inactive" v-model="showInactive" />
            显示停用记录
          </FieldLabel>
          <FieldLabel for="project-spec-closed" class="check-label has-data-[state=checked]:bg-transparent has-data-[state=checked]:border-transparent">
            <Checkbox id="project-spec-closed" v-model="showClosed" />
            显示已完成／已取消
          </FieldLabel>
        </div>
        <Alert v-if="error" class="notice error" role="alert" variant="destructive">
          {{ error }}
          <Button variant="outline" :disabled="loading" @click="refresh">重试</Button>
        </Alert>
        <p v-if="projectResult && !resultsCurrent && !loading" class="project-result-status muted">
          下方显示上次读取的记录，请重试以应用当前筛选。
        </p>
        <div v-if="projectResult" class="relative">
          <Card class="panel block gap-0 py-0 shadow-none" :aria-busy="loading">
            <div :inert="!resultsCurrent">
              <WorkItemRows :items="projectResult.items.slice(0, 50)" />
              <p v-if="projectResult.total > 50" class="empty">
                当前显示前 50 项。
                <RouterLink :to="allLink">查看全部 {{ projectResult.total }} 项 →</RouterLink>
              </p>
            </div>
          </Card>
          <LoadingIndicator
            :active="loading"
            placement="overlay"
            :label="resultsCurrent ? '正在更新项目 Spec' : '正在筛选，暂显示上次结果'"
          />
        </div>
        <LoadingSkeleton v-else-if="loading" variant="table" :rows="4" label="正在读取项目 Spec" />
        <p class="view-note muted">
          包含自动纳管和手动关联的 Spec。其他候选文件可在下方选择关联；不扫描外部平台。
        </p>
        <Collapsible
          v-if="project.available && project.record.active"
          :key="project.record.rootPath"
          :default-open="!specs.length"
          :unmount-on-hide="false"
          class="discover-disclosure"
        >
          <CollapsibleTrigger>自动发现与关联 Spec 文件</CollapsibleTrigger>
          <CollapsibleContent>
            <DiscoverSpecs
              :key="project.record.id"
              :project-id="project.record.id"
              :directories="project.sourceDirectories"
            />
          </CollapsibleContent>
        </Collapsible>
      </TabsContent>
      <TabsContent value="maintenance" class="min-w-0">
        <EngineeringPanel :root-path="project.record.rootPath" />
      </TabsContent>
      <TabsContent value="external" class="min-w-0">
        <ExternalLinks :project-id="project.record.id" />
      </TabsContent>
    </Tabs>
    <ProjectForm v-model="edit" :project="project.record" />
  </template>
  <Empty v-else class="panel">
    <EmptyTitle role="heading" aria-level="2">项目不存在</EmptyTitle>
    <EmptyContent>
      <Button as-child variant="outline"><RouterLink to="/projects">返回工程列表</RouterLink></Button>
    </EmptyContent>
  </Empty>
</template>

<style scoped>
.detail-navigation {
  display: flex;
  justify-content: flex-start;
  width: 100%;
  height: auto;
  min-width: 0;
  padding: 4px;
  gap: 4px;
  overflow-x: auto;
  overscroll-behavior-x: contain;
  margin-bottom: 12px;
}
.detail-navigation :deep([data-slot='tabs-trigger']) {
  flex: 0 0 auto;
  min-height: var(--yss-control-min-height);
  padding-inline: 12px;
}
.project-result-status {
  margin: 0 0 8px;
  font-size: 12px;
}
@media (width < 720px) {
  .project-spec-toolbar {
    column-gap: 12px;
    row-gap: 0;
  }
  .project-spec-count {
    flex-basis: 100%;
  }
}
.discover-disclosure {
  margin-top: 22px;
  border-top: 1px solid var(--yss-colors-border);
}
.discover-disclosure :deep(.discover-panel) {
  margin-top: 0;
}
</style>
