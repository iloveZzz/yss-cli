<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { Button } from '@/components/ui/button'
import { useWorkbench } from '@/hooks/useWorkbench'
import ProjectForm from './components/ProjectForm.vue'
import HomeManager from './components/HomeManager.vue'
import type { ProjectRecord } from '@/services/workbench'
const { snapshot } = useWorkbench()
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
      <RouterLink to="/settings?operation=init" class="text-link">初始化工程</RouterLink>
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
        <label class="inline-field"
          >查找工程<input
            v-model="search"
            name="projectSearch"
            type="search"
            placeholder="工程名或本机路径"
        /></label>
      </div>
      <label class="check-label"
        ><input v-model="showInactive" name="showInactive" type="checkbox" />显示停用工程</label
      >
    </div>
    <div
      v-if="projects.length"
      class="panel table-scroll project-table-wrap"
      tabindex="0"
      role="region"
      aria-labelledby="local-projects-heading"
    >
      <table class="spec-table project-table">
        <caption class="sr-only">
          本机工程、关联 Spec 与目录状态
        </caption>
        <thead>
          <tr>
            <th scope="col">工程</th>
            <th scope="col">本机目录</th>
            <th scope="col">关联 Spec</th>
            <th scope="col">状态</th>
            <th scope="col">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="project in projects" :key="project.record.id">
            <th scope="row">
              <RouterLink :to="`/projects/${project.record.id}`" class="project-name">{{
                project.record.name
              }}</RouterLink>
            </th>
            <td class="project-path">
              <span class="path-text">{{ project.record.rootPath }}</span><span
                v-if="!project.available"
                class="row-sub warning-text"
                >{{ project.message }}</span
              >
            </td>
            <td class="numeric">{{ specCounts.get(project.record.id) ?? 0 }}</td>
            <td>
              <span v-if="!project.record.active" class="muted">已停用</span
              ><span v-else :class="project.available ? 'muted' : 'warning-text'">{{
                project.available ? '可访问' : '目录不可访问'
              }}</span>
            </td>
            <td>
              <Button
                type="button"
                variant="ghost"
                :aria-label="`编辑 ${project.record.name}`"
                @click="edit(project.record)"
                >编辑</Button
              >
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-else class="empty panel">
      <template v-if="search"
        ><p>没有匹配“{{ search }}”的工程</p>
        <Button type="button" variant="outline" @click="search = ''">清除搜索</Button></template
      >
      <template v-else-if="snapshot?.projects.length"
        ><h2>当前没有启用工程</h2>
        <p>已停用工程的个人记录仍保留。</p>
        <Button type="button" variant="outline" @click="showInactive = true"
          >显示停用工程</Button
        ></template
      >
      <template v-else
        ><h2>登记第一个本机工程</h2>
        <p>选择工程目录后，可关联 Spec、维护技能或参与 Codex 运行。</p>
        <Button @click="edit()">登记本机工程</Button></template
      >
    </div>
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
.project-search label {
  width: 100%;
}
.project-table {
  min-width: 680px;
}
.project-table th[scope='row'] {
  position: sticky;
  left: 0;
  background: var(--brand-color-bg-container, #fff);
  font-weight: 550;
  text-align: left;
  min-width: 150px;
}
.project-table thead {
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
</style>
