<script lang="ts">
// Restore the list even when Vue Router creates a fresh component on return.
const saved = { search: '', filter: 'all', kind: 'all', top: 0, left: 0, windowY: 0 }
</script>
<script setup lang="ts">
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import TableLoadingOverlay from '@/components/TableLoadingOverlay.vue'
import { computed, onBeforeUnmount, onMounted, shallowRef, useTemplateRef, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Empty, EmptyDescription, EmptyHeader } from '@/components/ui/empty'
import { Field, FieldLabel } from '@/components/ui/field'
import { Table, TableBody, TableCaption, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
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
  loading: boolean
  failed: boolean
  projectNames: (job: JobRecord) => string
}>()
const route = useRoute()
const router = useRouter()
const openAll = route.query.scope === 'all'
const search = shallowRef(openAll ? '' : saved.search)
const filter = shallowRef(openAll ? 'all' : saved.filter)
const kind = shallowRef(openAll ? 'all' : saved.kind)
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
onMounted(() => {
  // Consume the home entry scope so browser Back still restores filters used in this list.
  if (openAll) void router.replace({ query: { ...route.query, scope: undefined } })
})
watch(
  () => [props.loaded, scroll.value],
  () => {
    if (!props.loaded || !scroll.value || restored) return
    restored = true
    scroll.value.scrollTo(openAll ? 0 : saved.left, openAll ? 0 : saved.top)
    window.scrollTo({ top: openAll ? 0 : saved.windowY, behavior: 'instant' })
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
  <Card class="jobs-panel gap-0 py-0 shadow-none">
    <div class="jobs-filters">
      <div class="jobs-filter-fields">
        <Field class="job-search gap-1.5">
          <FieldLabel for="job-search">搜索</FieldLabel>
          <Input id="job-search" v-model="search" type="search" placeholder="操作名称或工程" />
        </Field>
        <Field class="gap-1.5">
          <FieldLabel for="job-range">状态</FieldLabel>
          <SingleSelect id="job-range" v-model="filter" :options="filterOptions" />
        </Field>
        <Field class="gap-1.5">
          <FieldLabel for="job-kind">类型</FieldLabel>
          <SingleSelect id="job-kind" v-model="kind" :options="kindOptions" />
        </Field>
      </div>
      <div class="jobs-filter-actions">
        <Button v-if="hasFilters" type="button" variant="ghost" @click="clearFilters">清除筛选</Button>
        <Button type="button" variant="outline" @click="historyOpen = true">
          请求历史{{ requestCount == null ? '' : `（${requestCount}）` }}
        </Button>
      </div>
    </div>
    <p v-if="loaded" class="job-count muted" role="status">{{ visibleJobs.length }} 条执行</p>
    <div v-if="loaded" class="jobs-table-result">
      <div
        ref="scroll"
        class="table-scroll jobs-table-scroll"
        :aria-busy="loading"
        role="region"
        aria-label="执行记录列表"
        tabindex="0"
      >
        <Table class="spec-table jobs-table">
          <TableCaption class="sr-only">
            本机执行记录，按最近更新时间排列
          </TableCaption>
          <TableHeader>
            <TableRow>
              <TableHead scope="col">执行内容</TableHead>
              <TableHead scope="col">状态</TableHead>
              <TableHead scope="col">类型</TableHead>
              <TableHead scope="col">工程</TableHead>
              <TableHead scope="col">最近更新</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="item in visibleJobs" :key="item.id">
              <TableHead scope="row">
                <RouterLink :to="`/jobs/${item.id}`">{{ jobTitle(item) }}</RouterLink>
              </TableHead>
              <TableCell>
                <JobStatus :status="item.status" />
              </TableCell>
              <TableCell>{{ jobKindLabels[item.kind] ?? item.kind }}</TableCell>
              <TableCell>
                {{ projectNames(item) || '本机工作台' }}
              </TableCell>
              <TableCell class="job-time">{{ dateLabel(item.updatedAt) }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <TableLoadingOverlay :active="loading && !!visibleJobs.length" label="正在刷新执行记录" />
      </div>
    </div>
    <LoadingSkeleton v-if="!loaded && loading" variant="table" :rows="4" label="正在读取执行记录" />
    <Empty v-else-if="(loaded || !failed) && !visibleJobs.length" class="gap-3 p-6 md:p-8" data-workbench-reveal>
      <EmptyHeader>
        <EmptyDescription>{{ hasFilters ? '没有符合筛选条件的执行记录' : '暂无执行记录' }}</EmptyDescription>
      </EmptyHeader>
      <Button v-if="hasFilters" type="button" variant="outline" @click="clearFilters">
        查看全部记录
      </Button>
    </Empty>
  </Card>
</template>

<style scoped>
.jobs-panel {
  min-width: 0;
  overflow: hidden;
}
.jobs-filters {
  display: flex;
  justify-content: space-between;
  align-items: end;
  flex-wrap: wrap;
  gap: 12px 24px;
  padding: 16px;
}
.jobs-filter-fields {
  flex: 1 1 360px;
  max-width: 620px;
  display: grid;
  grid-template-columns: minmax(150px, 1.6fr) repeat(2, minmax(118px, 1fr));
  gap: 12px;
}
.jobs-filter-actions {
  display: flex;
  gap: 8px;
  margin-inline-start: auto;
  flex-wrap: wrap;
}
.job-count {
  margin: 0;
  padding: 0 16px 10px;
  font-size: 12px;
}
.jobs-table-result {
  position: relative;
}
.jobs-table-scroll {
  max-height: 70vh;
  container-type: inline-size;
}
.jobs-table {
  border-collapse: separate;
  border-spacing: 0;
  min-width: 640px;
}
.jobs-table thead {
  position: sticky;
  top: 0;
  z-index: 2;
}
.jobs-table thead th:first-child {
  position: sticky;
  inset-inline-start: 0;
  z-index: 1;
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
  min-width: 180px;
  max-width: 420px;
  line-height: 1.5;
  overflow-wrap: anywhere;
}
.jobs-table tbody th a:hover {
  color: var(--yss-color-primary-control);
  text-decoration: underline;
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
@media (width < 580px) {
  .jobs-filter-fields {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .job-search {
    grid-column: 1 / -1;
  }
  .jobs-table tbody th {
    min-width: 148px;
    max-width: min(50cqw, 220px);
  }
}
</style>
