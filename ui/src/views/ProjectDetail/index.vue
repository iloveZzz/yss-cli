<script setup lang="ts">
import { computed, nextTick, shallowRef, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import EngineeringPanel from '@/components/EngineeringPanel.vue'
import ExternalLinks from '@/components/ExternalLinks.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Empty, EmptyContent, EmptyTitle } from '@/components/ui/empty'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { provideProjectDetailWorkbench } from '@/hooks/useProjectDetailWorkbench'
import ProjectForm from '@/views/Projects/components/ProjectForm.vue'
import ProjectSpecs from './components/ProjectSpecs.vue'

const route = useRoute()
const router = useRouter()
const projectId = computed(() =>
  typeof route.params.projectId === 'string' ? route.params.projectId : undefined
)
const heading = useTemplateRef<HTMLElement>('project-heading')
const specPanel = useTemplateRef<InstanceType<typeof ProjectSpecs>>('project-specs')
const {
  project, directoryKnown, registeredCount, loading, error, refresh, reloadProject, refreshing, refreshError
} = provideProjectDetailWorkbench(() => projectId.value, () => specPanel.value?.reloadFailed())
const edit = shallowRef(false)
const sections = [
  { value: 'specs', label: 'Spec 与文件' },
  { value: 'maintenance', label: '工程维护' },
  { value: 'external', label: '研发关联' }
] as const
type Section = typeof sections[number]['value']
const section = computed(() =>
  sections.find((item) => item.value === route.query.section)?.value ?? 'specs'
)
const visited = shallowRef(new Set<Section>())
watch([projectId, section], ([id, value], [previousId]) => {
  if (id !== previousId) {
    edit.value = false
    visited.value = new Set([value])
  } else if (!visited.value.has(value)) {
    visited.value = new Set([...visited.value, value])
  }
}, { immediate: true })

function selectSection(value: string | number) {
  if (typeof value === 'string' && value !== section.value)
    void router.push({ query: { ...route.query, section: value } })
}

async function retryProject(event: MouseEvent) {
  const id = projectId.value
  const focused = document.activeElement === event.currentTarget
  if (await reloadProject() && focused && projectId.value === id) {
    await nextTick()
    if (document.activeElement === document.body) heading.value?.focus({ preventScroll: true })
  }
}
</script>

<template>
  <template v-if="project">
    <div class="page-heading">
      <div>
        <RouterLink to="/projects" class="back-link">← 产品与工程</RouterLink>
        <h1 ref="project-heading" tabindex="-1">{{ project.record.name }}</h1>
        <p class="path-text">{{ project.record.rootPath }}</p>
      </div>
      <Button variant="outline" @click="edit = true">编辑项目</Button>
    </div>
    <Alert v-if="!project.record.active" class="notice warning">
      项目已停用，记录保留。可通过“编辑项目”恢复。
    </Alert>
    <Alert v-else-if="directoryKnown && !project.available" class="notice warning">
      目录不可访问：{{ project.message }}。可重新定位项目根。
    </Alert>
  </template>
  <Alert v-if="error" class="notice error" role="alert" variant="destructive">
    {{ error }}
    <Button type="button" variant="outline" :disabled="loading" @click="retryProject">重试读取工程</Button>
  </Alert>
  <template v-if="project">
    <Alert v-if="refreshError && section !== 'specs'" class="notice error" role="alert" variant="destructive">
      {{ refreshError }}
      <Button type="button" variant="outline" :disabled="refreshing" @click="refresh()">重试刷新</Button>
    </Alert>
    <p v-else-if="refreshing && section !== 'specs'" class="view-note muted" role="status">正在核验工程 Spec。</p>
    <Tabs :key="project.record.id" :model-value="section" :unmount-on-hide="false" activation-mode="manual" @update:model-value="selectSection">
      <TabsList class="detail-navigation" aria-label="工程详情分组" moving-indicator>
        <TabsTrigger v-for="item in sections" :key="item.value" :value="item.value">{{ item.label }}</TabsTrigger>
      </TabsList>
      <TabsContent value="specs" class="min-w-0">
        <ProjectSpecs
          v-if="visited.has('specs')"
          ref="project-specs"
          :project="project"
          :directory-known="directoryKnown"
          :registered-count="registeredCount"
          :active="section === 'specs'"
          :refresh="refresh"
          :refreshing="refreshing"
          :refresh-error="refreshError"
          :reload-project="reloadProject"
        />
      </TabsContent>
      <TabsContent value="maintenance" class="min-w-0">
        <EngineeringPanel v-if="visited.has('maintenance')" :root-path="project.record.rootPath" />
      </TabsContent>
      <TabsContent value="external" class="min-w-0">
        <ExternalLinks v-if="visited.has('external')" :project-id="project.record.id" :active="section === 'external'" />
      </TabsContent>
    </Tabs>
    <ProjectForm :key="project.record.id" v-model="edit" :project="project.record" />
  </template>
  <LoadingSkeleton v-else-if="loading" variant="detail" :rows="4" label="正在读取工程详情" />
  <Empty v-else-if="!error" class="panel">
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
</style>
