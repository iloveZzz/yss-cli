<script setup lang="ts">
import { computed, nextTick, shallowRef, useTemplateRef } from 'vue'
import { FolderClosed, Search } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import WorkbenchBreadcrumb from '@/components/WorkbenchBreadcrumb.vue'
import { registerWorkbenchRouteReads, useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'
import ProjectForm from './components/ProjectForm.vue'
import HomeManager from './components/HomeManager.vue'
import { useProjectDirectoryChecks } from './useProjectDirectoryChecks'
import { expired, type ProjectRecord } from '@/services/workbench'

const { catalog, catalogLoading, catalogError, refreshCatalog } = useWorkbenchCatalog()
const homeManager = useTemplateRef<InstanceType<typeof HomeManager>>('home-manager')
const localHeading = useTemplateRef<HTMLHeadingElement>('local-projects-heading')
const open = shallowRef(false)
const selected = shallowRef<ProjectRecord>()
const showInactive = shallowRef(false)
const search = shallowRef('')
const specCounts = computed(() => {
  const counts = new Map<string, number>()
  for (const spec of catalog.value?.specs ?? [])
    if (spec.active)
      counts.set(spec.projectId, (counts.get(spec.projectId) ?? 0) + 1)
  return counts
})
const projects = computed(() => {
  const needle = search.value.trim().toLocaleLowerCase()
  return (
    catalog.value?.projects.filter(
      (project) =>
        (showInactive.value || project.active) &&
        (!needle ||
          `${project.name} ${project.rootPath}`.toLocaleLowerCase().includes(needle))
    ) ?? []
  )
})
const { directoryCheck, refreshDirectories, checking } = useProjectDirectoryChecks(() => projects.value)
const projectCards = computed(() => projects.value.map((project) => ({ project, directory: directoryCheck(project) })))
const hasDirectoryErrors = computed(() => projectCards.value.some(({ directory }) => directory?.available === null))

registerWorkbenchRouteReads({
  busy: computed(() => catalogLoading.value || homeManager.value?.refreshing === true),
  readAt: computed(() => catalog.value?.readAt),
  refresh: async () => {
    await Promise.all([homeManager.value?.refresh(), refreshDirectories('all')])
  }
})

function edit(project?: ProjectRecord) {
  if (!catalog.value || expired.value) return
  selected.value = project
  open.value = true
}

async function retryRead(event: MouseEvent, action: () => Promise<unknown>) {
  const keepFocus = document.activeElement === event.currentTarget
  await action()
  await nextTick()
  if (keepFocus && document.activeElement === document.body)
    localHeading.value?.focus({ preventScroll: true })
}
</script>
<template>
  <template v-if="!expired">
    <div class="page-heading">
      <WorkbenchBreadcrumb title="产品与工程" />
      <div class="inline-actions">
        <Button as-child variant="outline"><RouterLink to="/settings?operation=init">初始化工程</RouterLink></Button>
        <Button :disabled="!catalog" @click="edit()">登记本机工程</Button>
      </div>
    </div>
    <HomeManager ref="home-manager" />
    <section class="local-projects" aria-labelledby="local-projects-heading">
      <div class="section-heading project-list-heading">
        <h2 id="local-projects-heading" ref="local-projects-heading" tabindex="-1">本机工程</h2>
        <div v-if="catalog" class="project-list-status">
          <LoadingIndicator :active="catalogLoading" label="正在刷新本机工程" />
          <span class="muted">{{ projects.length }} 项</span>
        </div>
      </div>
      <Alert v-if="catalogError" class="notice warning" role="status">
        <AlertTitle>{{ catalog ? '显示上次读取的工程列表' : '未能读取工程列表' }}</AlertTitle>
        <AlertDescription>{{ catalogError }}</AlertDescription>
        <Button type="button" variant="outline" class="justify-self-start" :disabled="catalogLoading" @click="retryRead($event, refreshCatalog)">重试读取</Button>
      </Alert>
      <div v-if="catalog" class="project-toolbar">
        <div role="search" aria-label="搜索工程" class="project-search">
          <FieldLabel for="project-search" class="sr-only">查找工程</FieldLabel>
          <Search :size="16" class="project-search-icon" aria-hidden="true" />
          <Input
            id="project-search"
            v-model="search"
            name="projectSearch"
            type="search"
            placeholder="搜索工程名或本机路径"
          />
        </div>
        <div class="project-filters">
          <FieldLabel class="check-label has-data-[state=checked]:bg-transparent has-data-[state=checked]:border-transparent" for="include-inactive-projects">
            <Checkbox id="include-inactive-projects" v-model="showInactive" name="showInactive" />
            显示停用工程
          </FieldLabel>
          <Button v-if="hasDirectoryErrors" type="button" variant="ghost" :disabled="checking" @click="retryRead($event, () => refreshDirectories('failed'))">
            重试目录核验
          </Button>
        </div>
      </div>
      <LoadingSkeleton v-if="!catalog && !catalogError" variant="cards" :rows="4" class="project-list-skeleton" label="正在读取工程列表" />
      <ul
        v-else-if="projectCards.length"
        class="project-list-grid"
        role="list"
        aria-labelledby="local-projects-heading"
        :aria-busy="catalogLoading"
      >
        <li v-for="{ project, directory } in projectCards" :key="project.id" class="project-list-item">
          <article class="local-project-card">
            <header class="project-card-header">
              <span class="project-card-icon" aria-hidden="true"><FolderClosed :size="19" /></span>
              <div class="project-card-identity">
                <h3 :title="project.name">
                  <RouterLink :to="`/projects/${project.id}`" class="project-name">{{ project.name }}</RouterLink>
                </h3>
                <Badge v-if="!project.active" variant="secondary">已停用</Badge>
                <Badge v-else-if="directory?.available" variant="outline">可访问</Badge>
                <Badge v-else-if="directory?.available === false" variant="outline" class="warning-text">目录不可访问</Badge>
                <Badge v-else-if="directory" variant="outline">未核验</Badge>
                <Badge v-else variant="outline">待核验</Badge>
              </div>
            </header>
            <div>
              <p class="path-text project-card-path" :title="project.rootPath">{{ project.rootPath }}</p>
              <p v-if="directory?.message" class="project-card-note warning-text">{{ directory.message }}</p>
            </div>
            <footer class="project-card-footer">
              <span class="muted project-spec-count">{{ specCounts.get(project.id) ?? 0 }} 个关联 Spec</span>
              <Button type="button" variant="outline" size="sm" :aria-label="`编辑 ${project.name}`" @click="edit(project)">编辑</Button>
            </footer>
          </article>
        </li>
      </ul>
      <Empty v-else-if="catalog" class="panel" data-workbench-reveal>
        <EmptyHeader>
          <EmptyTitle role="heading" aria-level="2" v-if="search.trim()">没有匹配的工程</EmptyTitle>
          <EmptyTitle role="heading" aria-level="2" v-else-if="catalog.projects.length">当前没有启用工程</EmptyTitle>
          <EmptyTitle role="heading" aria-level="2" v-else>登记第一个本机工程</EmptyTitle>
          <EmptyDescription v-if="search.trim()" class="break-all">当前搜索词为“{{ search.trim() }}”。</EmptyDescription>
          <EmptyDescription v-else-if="catalog.projects.length">已停用工程的个人记录仍保留。</EmptyDescription>
          <EmptyDescription v-else>选择工程目录后，可关联 Spec、维护技能或参与 Pi Agent 运行。</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button v-if="search.trim()" type="button" variant="outline" @click="search = ''">清除搜索</Button>
          <Button v-else-if="catalog.projects.length" type="button" variant="outline" @click="showInactive = true">显示停用工程</Button>
          <Button v-else type="button" @click="edit()">登记本机工程</Button>
        </EmptyContent>
      </Empty>
    </section>
    <ProjectForm v-model="open" :project="selected" :revision="catalog?.revision" :refresh-after="refreshCatalog" />
  </template>
</template>
<style scoped>
.local-projects {
  --project-grid-gap: 14px;
}
.project-list-heading {
  flex-wrap: wrap;
  gap: 6px 12px;
  margin-block-end: 10px;
}
.project-list-status, .project-filters {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.project-list-status {
  font-size: 12px;
}
.project-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 16px;
  margin-block-end: 12px;
}
.project-search {
  position: relative;
  min-inline-size: 0;
  flex: 1 1 18rem;
}
.project-search [data-slot='input'] {
  min-block-size: max(40px, var(--yss-control-min-height));
  padding-inline-start: 36px;
  background: var(--workbench-glass-card);
}
.project-search-icon {
  position: absolute;
  inset-block: 0;
  inset-inline-start: 12px;
  margin-block: auto;
  color: var(--brand-color-text-secondary);
  pointer-events: none;
}
.project-list-grid, .project-list-skeleton :deep(.skeleton-layout) {
  /* Keep a narrow minimum and reserve the empty columns when the list is filtered. */
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, max(248px, calc((100% - 3 * var(--project-grid-gap)) / 4))), 1fr));
  gap: var(--project-grid-gap);
}
.project-list-grid {
  margin: 0;
  padding: 0;
  list-style: none;
}
.project-list-item {
  display: flex;
  min-inline-size: 0;
}
.local-project-card {
  display: grid;
  grid-template-rows: auto 1fr auto;
  gap: 10px;
  inline-size: 100%;
  min-inline-size: 0;
  padding: 14px;
  background: var(--workbench-glass-card);
  border: 1px solid var(--workbench-glass-edge);
  border-radius: var(--brand-border-radius-lg);
}
.project-list-skeleton :deep(.skeleton-card) {
  min-block-size: 144px;
}
.project-card-header, .project-card-footer {
  display: flex;
  align-items: center;
  gap: 10px;
}
.project-card-icon {
  display: grid;
  place-items: center;
  flex: none;
  inline-size: 36px;
  block-size: 36px;
  border-radius: var(--brand-border-radius);
  background: var(--yss-colors-primary-bg);
  color: var(--yss-color-primary-control);
}
.project-card-identity {
  display: grid;
  justify-items: start;
  gap: 4px;
  min-inline-size: 0;
}
.project-card-identity h3 {
  min-inline-size: 0;
  max-inline-size: 100%;
  font-size: 15px;
  font-weight: 650;
  line-height: 1.3;
}
.project-name {
  color: var(--brand-color-text);
}
.project-name:hover, .project-name:focus-visible {
  color: var(--yss-color-primary-control);
  text-decoration: underline;
}
.project-name, .project-card-path {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: clip;
  overflow-wrap: anywhere;
}
.project-card-note {
  margin-block-start: 4px;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.project-card-footer {
  justify-content: space-between;
  flex-wrap: wrap;
}
.project-spec-count {
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
@media (width < 720px) {
  .project-search [data-slot='input'] { font-size: 16px; }
  .project-filters :is(.check-label, [data-slot='button']),
  .project-card-footer [data-slot='button'] { min-block-size: var(--yss-control-min-height); }
}
</style>
