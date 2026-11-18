<script lang="ts">
// Restore the list even when Vue Router creates a fresh component on return.
const saved = { search: '', filter: 'all', kind: 'all', top: 0, left: 0, windowY: 0 }
</script>
<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, useTemplateRef, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import SingleSelect from '@/components/SingleSelect.vue'
import { dateLabel } from '@/services/workbench'
import { terminalJob, type JobRecord } from '@/services/workbench-platform'
import { jobKindLabels, jobTitle } from '../schemas/job'
import JobStatus from './JobStatus.vue'
import PendingRequests from './PendingRequests.vue'

const props = defineProps<{
  jobs: JobRecord[]
  loaded: boolean
  projectNames: (job: JobRecord) => string
}>()
const search = shallowRef(saved.search)
const filter = shallowRef(saved.filter)
const kind = shallowRef(saved.kind)
const scroll = useTemplateRef<HTMLElement>('scroll')
const requestCount = shallowRef<number>()
const historyOpen = shallowRef(false)
let restored = false
let leaving = false
function remember() {
  if (!restored || leaving) return
  leaving = true
  Object.assign(saved, {
    search: search.value,
    filter: filter.value,
    kind: kind.value,
    top: scroll.value?.scrollTop ?? 0,
    left: scroll.value?.scrollLeft ?? 0,
    windowY: window.scrollY
  })
}
onBeforeRouteLeave(remember)
onBeforeRouteUpdate((to, from) => {
  if (to.path !== from.path) remember()
})
onBeforeUnmount(remember)
watch(
  () => [props.loaded, scroll.value],
  () => {
    if (!props.loaded || !scroll.value || restored) return
    restored = true
    scroll.value.scrollTo(saved.left, saved.top)
    window.scrollTo({ top: saved.windowY, behavior: 'instant' })
  },
  { flush: 'post', immediate: true }
)
const filterOptions = [
  { value: 'all', label: '全部执行' },
  { value: 'active', label: '运行中与待输入' },
  { value: 'attention', label: '需要处理' },
  { value: 'terminal', label: '已结束' }
]
const kindOptions = computed(() => [
  { value: 'all', label: '全部类型' },
  ...[...new Set(props.jobs.map((item) => item.kind))].map((value) => ({
    value,
    label: jobKindLabels[value] ?? value
  }))
])
const visibleJobs = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  return props.jobs
    .filter((item) => {
      const matchesRange =
        filter.value === 'all' ||
        (filter.value === 'active' && !terminalJob(item.status)) ||
        (filter.value === 'terminal' && terminalJob(item.status)) ||
        (filter.value === 'attention' &&
          ['waitingInput', 'waitingApproval', 'failed', 'recoveryRequired'].includes(item.status))
      return (
        matchesRange &&
        (kind.value === 'all' || kind.value === item.kind) &&
        (!query ||
          [
            item.title,
            jobTitle(item),
            jobKindLabels[item.kind] ?? item.kind,
            ...item.roots,
            props.projectNames(item)
          ]
            .join(' ')
            .toLocaleLowerCase()
            .includes(query))
      )
    })
    .sort((left, right) => Date.parse(right.updatedAt) - Date.parse(left.updatedAt))
})
const hasFilters = computed(() => filter.value !== 'all' || kind.value !== 'all' || !!search.value)
function clearFilters() {
  filter.value = 'all'
  kind.value = 'all'
  search.value = ''
}
</script>
<template>
  <PendingRequests v-model:open="historyOpen" @count="requestCount = $event" />
  <div class="toolbar jobs-filters">
    <label for="job-search" class="inline-field job-search"
      ><span>搜索</span
      ><Input id="job-search" v-model="search" type="search" placeholder="操作名称或工程"
    /></label>
    <label for="job-range" class="inline-field"
      ><span>状态</span><SingleSelect id="job-range" v-model="filter" :options="filterOptions"
    /></label>
    <label for="job-kind" class="inline-field"
      ><span>类型</span><SingleSelect id="job-kind" v-model="kind" :options="kindOptions"
    /></label>
    <Button v-if="hasFilters" type="button" variant="ghost" @click="clearFilters">清除筛选</Button>
    <span class="muted job-count">{{ visibleJobs.length }} 条执行</span>
    <Button type="button" variant="outline" @click="historyOpen = true"
      >请求历史{{ requestCount == null ? '' : `（${requestCount}）` }}</Button
    >
  </div>
  <section class="panel jobs-panel">
    <div
      ref="scroll"
      class="table-scroll jobs-table-scroll"
      role="region"
      aria-label="执行记录列表"
      tabindex="0"
    >
      <table class="spec-table jobs-table">
        <caption class="sr-only">
          本机执行记录，按最近更新时间排列
        </caption>
        <thead>
          <tr>
            <th scope="col">执行内容</th>
            <th scope="col">状态</th>
            <th scope="col">类型</th>
            <th scope="col">工程</th>
            <th scope="col">最近更新</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in visibleJobs" :key="item.id">
            <th scope="row">
              <RouterLink :to="`/jobs/${item.id}`">{{ jobTitle(item) }}</RouterLink>
            </th>
            <td>
              <JobStatus :status="item.status" />
            </td>
            <td>{{ jobKindLabels[item.kind] ?? item.kind }}</td>
            <td>
              {{ projectNames(item) || '本机工作台' }}
            </td>
            <td class="job-time">{{ dateLabel(item.updatedAt) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="!visibleJobs.length" class="empty">
      <p>{{ hasFilters ? '没有符合筛选条件的执行记录' : '暂无执行记录' }}</p>
      <Button v-if="hasFilters" type="button" variant="ghost" @click="clearFilters"
        >查看全部记录</Button
      >
    </div>
  </section>
</template>

<style scoped>
.jobs-filters {
  gap: 12px;
}
.job-search input {
  min-width: 190px;
  width: 230px;
}
.job-count {
  margin-left: auto;
}
.jobs-table-scroll {
  max-height: 70vh;
  container-type: inline-size;
}
.jobs-table {
  border-collapse: separate;
  border-spacing: 0;
  min-width: 760px;
}
.jobs-table thead {
  position: sticky;
  top: 0;
  z-index: 2;
}
.jobs-table tbody th {
  position: sticky;
  inset-inline-start: 0;
  z-index: 1;
  background: var(--brand-color-bg-container);
  font-size: 13px;
  font-weight: 500;
  padding: 10px 16px;
  border-bottom: 1px solid var(--yss-colors-border);
  white-space: normal;
  min-width: 220px;
  max-width: 420px;
  line-height: 1.5;
}
.jobs-table tbody td {
  height: 48px;
  font-size: 12px;
}
.jobs-table tbody tr:hover th {
  background: #fbfcff;
}
.job-time {
  white-space: nowrap;
}
@container (width < 700px) {
  .jobs-table tbody th {
    max-width: 300px;
  }
}
@media (width < 720px) {
  .job-search {
    width: 100%;
  }
  .job-search input {
    flex: 1;
    min-width: 0;
    width: 100%;
  }
  .job-count {
    margin-left: 0;
  }
}
</style>
