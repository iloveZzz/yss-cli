<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
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
const { result, loading, error, refresh } = useWorkItems(query)
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
        <RouterLink to="/projects" class="muted">← 项目</RouterLink>
        <h1>{{ project.record.name }}</h1>
        <p class="path-text">{{ project.record.rootPath }}</p>
      </div>
      <Button variant="outline" @click="edit = true">编辑项目</Button>
    </div>
    <div v-if="!project.available || !project.record.active" class="notice warning">
      {{
        !project.record.active
          ? '项目已停用，记录保留。可通过“编辑项目”恢复。'
          : `目录不可访问：${project.message}。可重新定位项目根。`
      }}
    </div>
    <nav class="detail-navigation" aria-label="工程详情分组">
      <RouterLink
        v-for="item in sections"
        :key="item.value"
        :to="{ query: { ...route.query, section: item.value } }"
        :aria-current="section === item.value ? 'page' : undefined"
        >{{ item.label }}</RouterLink
      >
    </nav>
    <section v-show="section === 'specs'" aria-label="工程 Spec">
      <div class="section-heading">
        <h2>已纳管 Spec</h2>
        <RouterLink :to="allLink">查看项目全部记录 →</RouterLink>
      </div>
      <div class="toolbar">
        <span>
          共 {{ totals.total }} 项 · 启用且已关闭 {{ totals.closed }} · 停用 {{ totals.inactive }}
        </span>
        <label class="check-label">
          <input v-model="showInactive" type="checkbox" />
          显示停用记录
        </label>
        <label class="check-label">
          <input v-model="showClosed" type="checkbox" />
          显示已完成／已取消
        </label>
      </div>
      <p v-if="error" class="notice error" role="alert">
        {{ error }}
        <Button variant="outline" @click="refresh">重试</Button>
      </p>
      <p v-if="loading" class="muted">正在更新项目 Spec…</p>
      <section v-if="result" class="panel">
        <WorkItemRows :items="result.items.slice(0, 50)" />
        <p v-if="result.total > 50" class="empty">
          当前显示前 50 项。
          <RouterLink :to="allLink">查看全部 {{ result.total }} 项 →</RouterLink>
        </p>
      </section>
      <p class="view-note muted">
        仅覆盖手动关联的 Spec。尚未选择的文件、其他目录和外部平台不计入。
      </p>
      <details v-if="project.available && project.record.active" class="discover-disclosure">
        <summary>关联更多 Spec 文件</summary>
        <DiscoverSpecs
          :key="project.record.id"
          :project-id="project.record.id"
          :directories="project.sourceDirectories"
        />
      </details>
    </section>
    <section v-show="section === 'maintenance'" aria-label="工程维护">
      <EngineeringPanel :root-path="project.record.rootPath" />
    </section>
    <section v-show="section === 'external'" aria-label="研发关联">
      <ExternalLinks :project-id="project.record.id" />
    </section>
    <ProjectForm v-model="edit" :project="project.record" />
  </template>
  <p v-else class="empty">
    项目不存在。
    <RouterLink to="/projects">返回项目列表</RouterLink>
  </p>
</template>

<style scoped>
.detail-navigation {
  display: flex;
  flex-wrap: wrap;
  gap: 24px;
  border-bottom: 1px solid var(--yss-colors-border);
  margin-bottom: 22px;
}
.detail-navigation a {
  padding: 10px 0;
  border-bottom: 2px solid transparent;
  color: var(--brand-color-text-secondary);
}
.detail-navigation a[aria-current='page'] {
  color: var(--yss-color-primary-control);
  border-bottom-color: var(--yss-color-primary-control);
  font-weight: 600;
}
.discover-disclosure {
  margin-top: 22px;
  border-top: 1px solid var(--yss-colors-border);
}
.discover-disclosure > summary {
  padding: 16px 0;
  cursor: pointer;
  font-weight: 550;
}
.discover-disclosure :deep(.discover-panel) {
  margin-top: 0;
}
</style>
