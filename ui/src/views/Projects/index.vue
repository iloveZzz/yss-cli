<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import TableLoadingOverlay from '@/components/TableLoadingOverlay.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import ProjectForm from './components/ProjectForm.vue'
import HomeManager from './components/HomeManager.vue'
import type { ProjectRecord } from '@/services/workbench'
const { snapshot, loading } = useWorkbench()
const open = shallowRef(false)
const selected = shallowRef<ProjectRecord>()
const showInactive = shallowRef(false)
const search = shallowRef('')
const specCounts = computed(() => {
  const counts = new Map<string, number>()
  for (const spec of snapshot.value?.specs ?? [])
    if (spec.record.active)
      counts.set(spec.record.projectId, (counts.get(spec.record.projectId) ?? 0) + 1)
  return counts
})
const projects = computed(() => {
  const needle = search.value.trim().toLocaleLowerCase()
  return (
    snapshot.value?.projects.filter(
      (project) =>
        (showInactive.value || project.record.active) &&
        (!needle ||
          `${project.record.name} ${project.record.rootPath}`.toLocaleLowerCase().includes(needle))
    ) ?? []
  )
})
function edit(project?: ProjectRecord) {
  selected.value = project
  open.value = true
}
</script>
<template>
  <div class="page-heading">
    <h1>产品与工程</h1>
    <div class="inline-actions">
      <Button as-child variant="outline"><RouterLink to="/settings?operation=init">初始化工程</RouterLink></Button>
      <Button @click="edit()">登记本机工程</Button>
    </div>
  </div>
  <HomeManager />
  <section aria-labelledby="local-projects-heading">
    <div class="section-heading">
      <h2 id="local-projects-heading">本机工程</h2>
      <span class="muted">{{ projects.length }} 项</span>
    </div>
    <div class="toolbar project-toolbar">
      <div role="search" aria-label="搜索工程" class="project-search">
        <div class="inline-field">
          <FieldLabel for="project-search" class="shrink-0">查找工程</FieldLabel>
          <Input
            id="project-search"
            v-model="search"
            name="projectSearch"
            type="search"
            placeholder="工程名或本机路径"
          />
        </div>
      </div>
      <FieldLabel class="check-label has-data-[state=checked]:bg-transparent has-data-[state=checked]:border-transparent" for="include-inactive-projects">
        <Checkbox id="include-inactive-projects" v-model="showInactive" name="showInactive" />
        显示停用工程
      </FieldLabel>
    </div>
    <Card
      v-if="projects.length"
      class="panel block gap-0 py-0 shadow-none table-scroll project-table-wrap"
      tabindex="0"
      role="region"
      aria-labelledby="local-projects-heading"
      :aria-busy="loading"
    >
      <Table class="spec-table project-table">
        <TableCaption class="sr-only">
          本机工程、关联 Spec 与目录状态
        </TableCaption>
        <TableHeader>
          <TableRow>
            <TableHead scope="col">工程</TableHead>
            <TableHead scope="col">本机目录</TableHead>
            <TableHead scope="col">关联 Spec</TableHead>
            <TableHead scope="col">状态</TableHead>
            <TableHead scope="col">操作</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="project in projects" :key="project.record.id">
            <TableHead scope="row">
              <RouterLink :to="`/projects/${project.record.id}`" class="project-name">{{
                project.record.name
              }}</RouterLink>
            </TableHead>
            <TableCell class="project-path">
              <span class="path-text">{{ project.record.rootPath }}</span><span
                v-if="!project.available"
                class="row-sub warning-text"
                >{{ project.message }}</span
              >
            </TableCell>
            <TableCell class="numeric">{{ specCounts.get(project.record.id) ?? 0 }}</TableCell>
            <TableCell>
              <Badge v-if="!project.record.active" variant="secondary">已停用</Badge>
              <Badge v-else-if="project.available" variant="outline">可访问</Badge>
              <Badge v-else variant="outline" class="warning-text">目录不可访问</Badge>
            </TableCell>
            <TableCell>
              <Button
                type="button"
                variant="ghost"
                :aria-label="`编辑 ${project.record.name}`"
                @click="edit(project.record)"
                >编辑</Button
              >
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <TableLoadingOverlay :active="loading" label="正在刷新本机工程" />
    </Card>
    <Empty v-else class="panel" data-workbench-reveal>
      <EmptyHeader>
        <EmptyTitle role="heading" aria-level="2" v-if="search.trim()">没有匹配的工程</EmptyTitle>
        <EmptyTitle role="heading" aria-level="2" v-else-if="snapshot?.projects.length">当前没有启用工程</EmptyTitle>
        <EmptyTitle role="heading" aria-level="2" v-else>登记第一个本机工程</EmptyTitle>
        <EmptyDescription v-if="search.trim()" class="break-all">当前搜索词为“{{ search.trim() }}”。</EmptyDescription>
        <EmptyDescription v-else-if="snapshot?.projects.length">已停用工程的个人记录仍保留。</EmptyDescription>
        <EmptyDescription v-else>选择工程目录后，可关联 Spec、维护技能或参与 Pi Agent 运行。</EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button v-if="search.trim()" type="button" variant="outline" @click="search = ''">清除搜索</Button>
        <Button v-else-if="snapshot?.projects.length" type="button" variant="outline" @click="showInactive = true">显示停用工程</Button>
        <Button v-else type="button" @click="edit()">登记本机工程</Button>
      </EmptyContent>
    </Empty>
  </section>
  <ProjectForm v-model="open" :project="selected" />
</template>
<style scoped>
.project-toolbar {
  justify-content: space-between;
}
.project-search {
  min-width: 0;
  flex: 1;
  max-width: 420px;
}
.project-table {
  min-width: 680px;
}
.project-table :deep(th[scope='row']) {
  position: sticky;
  inset-inline-start: 0;
  background: var(--brand-color-bg-container, #fff);
  font-weight: 550;
  text-align: left;
  min-width: 150px;
}
.project-table :deep(thead) {
  position: sticky;
  top: 0;
  z-index: 1;
}
.project-path {
  max-width: 420px;
  white-space: normal;
  overflow-wrap: anywhere;
}
.numeric {
  font-variant-numeric: tabular-nums;
}
.project-table-wrap {
  max-height: 65vh;
}
.project-name {
  color: var(--yss-color-primary-control);
}
@media (width < 720px) {
  .project-search {
    max-width: none;
    flex-basis: 100%;
  }
  .project-table :deep(th[scope='row']) {
    max-width: 170px;
    white-space: normal;
    overflow-wrap: anywhere;
  }
}
</style>
