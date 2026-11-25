<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Alert } from '@/components/ui/alert'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import { computed, onBeforeUnmount, onMounted, shallowRef, useId } from 'vue'
import { useIntervalFn } from '@vueuse/core'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { dateLabel, errorMessage } from '@/services/workbench'
import {
  getJobs,
  terminalJob,
  jobStatusLabels,
  type JobRecord
} from '@/services/workbench-platform'
const jobs = shallowRef<JobRecord[]>([])
const error = shallowRef('')
const pending = shallowRef<'initial' | 'retry' | 'poll' | null>('initial')
const loading = computed(() => pending.value !== null)
const loaded = shallowRef(false)
const initialLoading = computed(() => !loaded.value && loading.value && pending.value !== 'poll')
const id = useId()
const priority: Record<string, number> = { waitingInput: 0, recoveryRequired: 1, failed: 3 }
const active = computed(() =>
  jobs.value
    .filter(
      (job) =>
        !terminalJob(job.status) || job.status === 'failed' || job.status === 'recoveryRequired'
    )
    .toSorted(
      (left, right) =>
        (priority[left.status] ?? 2) - (priority[right.status] ?? 2) ||
        right.updatedAt.localeCompare(left.updatedAt)
    )
    .slice(0, 6)
)
let controller: AbortController | undefined
async function refresh(kind: 'initial' | 'retry' | 'poll') {
  controller?.abort()
  const current = new AbortController()
  controller = current
  pending.value = kind
  try {
    const value = await getJobs(current.signal)
    if (!current.signal.aborted) {
      jobs.value = value
      loaded.value = true
      error.value = ''
    }
  } catch (cause) {
    if (!current.signal.aborted) error.value = errorMessage(cause)
  } finally {
    if (controller === current) pending.value = null
  }
}
onMounted(() => refresh('initial'))
onBeforeUnmount(() => controller?.abort())
useIntervalFn(() => {
  if (!document.hidden && !loading.value) void refresh('poll')
}, 5000)
function action(job: JobRecord) {
  return job.status === 'waitingInput'
    ? '处理输入'
    : job.status === 'recoveryRequired'
      ? '核对恢复'
      : job.status === 'failed'
        ? '查看原因'
        : '查看运行'
}
</script>
<template>
  <section class="work-section" :aria-labelledby="id">
    <div class="section-heading">
      <h2 :id="id">执行与待处理</h2>
      <RouterLink to="/jobs">全部执行记录 →</RouterLink>
    </div>
    <Alert v-if="error" role="alert" class="notice warning">
      执行记录读取失败。{{ error }}
      <Button type="button" variant="outline" :disabled="loading" @click="refresh('retry')">
        <LoadingIndicator :active="pending === 'retry' && loaded" />
        {{ pending === 'retry' ? '重试中…' : '重试' }}
      </Button>
    </Alert>
    <Card
      v-if="active.length"
      class="panel block gap-0 py-0 shadow-none table-scroll active-jobs"
      :aria-busy="pending === 'retry'"
      tabindex="0"
      role="region"
      :aria-labelledby="id"
    >
      <Table class="spec-table">
        <TableCaption class="sr-only">执行与待处理记录</TableCaption>
        <TableHeader>
          <TableRow>
            <TableHead scope="col">执行</TableHead>
            <TableHead scope="col">状态</TableHead>
            <TableHead scope="col">最近更新</TableHead>
            <TableHead scope="col">操作</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="job in active" :key="job.id">
            <TableHead scope="row">
              <RouterLink :to="`/jobs/${job.id}`">{{ job.title }}</RouterLink>
              <span
                v-if="job.roots.length"
                class="row-sub job-roots"
                :title="job.roots.join(' · ')"
              >
                {{ job.roots.join(' · ') }}
              </span>
            </TableHead>
            <TableCell>
              <Badge
                v-if="job.status === 'failed' || job.status === 'recoveryRequired'"
                variant="outline"
                class="warning-text"
              >
                {{ jobStatusLabels[job.status] ?? job.status }}
              </Badge>
              <Badge v-else variant="secondary">{{ jobStatusLabels[job.status] ?? job.status }}</Badge>
            </TableCell>
            <TableCell class="job-time">
              <time :datetime="job.updatedAt">{{ dateLabel(job.updatedAt) }}</time>
            </TableCell>
            <TableCell>
              <RouterLink :to="`/jobs/${job.id}`" class="job-action">
                {{ action(job) }} →
              </RouterLink>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </Card>
    <LoadingSkeleton
      v-else-if="initialLoading"
      variant="table"
      :rows="3"
      label="正在读取执行记录"
    />
  </section>
</template>
<style scoped>
.active-jobs :deep(table) {
  min-width: 620px;
  width: 100%;
  border-collapse: separate;
  border-spacing: 0;
}
.active-jobs :deep(tbody th) {
  position: sticky;
  inset-inline-start: 0;
  z-index: 1;
  background: #fff;
  min-width: 190px;
  max-width: 340px;
  padding: 12px 16px;
  text-align: left;
  font-size: 12px;
  font-weight: 550;
}
.job-roots {
  max-width: 310px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 400;
}
.active-jobs :deep(td) {
  font-size: 12px;
  white-space: nowrap;
}
.job-time {
  color: var(--brand-color-text-secondary);
  font-size: 11px !important;
}
.job-action {
  color: #315dcf;
}
</style>
