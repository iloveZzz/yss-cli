<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, shallowRef, useId } from 'vue'
import { useIntervalFn } from '@vueuse/core'
import { Button } from '@/components/ui/button'
import { dateLabel, errorMessage } from '@/services/workbench'
import {
  getJobs,
  terminalJob,
  jobStatusLabels,
  type JobRecord
} from '@/services/workbench-platform'
const jobs = shallowRef<JobRecord[]>([])
const error = shallowRef('')
const loading = shallowRef(false)
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
let generation = 0
let mounted = true
async function refresh() {
  const own = ++generation
  loading.value = true
  try {
    const value = await getJobs()
    if (mounted && own === generation) {
      jobs.value = value
      error.value = ''
    }
  } catch (cause) {
    if (mounted && own === generation) error.value = errorMessage(cause)
  } finally {
    if (mounted && own === generation) loading.value = false
  }
}
onMounted(refresh)
onBeforeUnmount(() => {
  mounted = false
  generation++
})
useIntervalFn(() => {
  if (!document.hidden && !loading.value) void refresh()
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
  <section v-if="active.length || error" class="work-section" :aria-labelledby="id">
    <div class="section-heading">
      <h2 :id="id">执行与待处理</h2>
      <RouterLink to="/jobs">全部执行记录 →</RouterLink>
    </div>
    <p v-if="error" role="alert" class="notice warning">
      执行记录读取失败。{{ error }}
      <Button type="button" variant="outline" :disabled="loading" @click="refresh">重试</Button>
    </p>
    <div
      v-if="active.length"
      class="panel table-scroll active-jobs"
      tabindex="0"
      role="region"
      :aria-labelledby="id"
    >
      <table class="spec-table">
        <caption class="sr-only">执行与待处理记录</caption>
        <thead>
          <tr>
            <th scope="col">执行</th>
            <th scope="col">状态</th>
            <th scope="col">最近更新</th>
            <th scope="col">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="job in active" :key="job.id">
            <th scope="row">
              <RouterLink :to="`/jobs/${job.id}`">{{ job.title }}</RouterLink>
              <span
                v-if="job.roots.length"
                class="row-sub job-roots"
                :title="job.roots.join(' · ')"
              >
                {{ job.roots.join(' · ') }}
              </span>
            </th>
            <td
              :class="
                job.status === 'failed' || job.status === 'recoveryRequired'
                  ? 'warning-text'
                  : 'muted'
              "
            >
              {{ jobStatusLabels[job.status] ?? job.status }}
            </td>
            <td class="job-time">
              <time :datetime="job.updatedAt">{{ dateLabel(job.updatedAt) }}</time>
            </td>
            <td>
              <RouterLink :to="`/jobs/${job.id}`" class="job-action">
                {{ action(job) }} →
              </RouterLink>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
<style scoped>
.active-jobs table {
  min-width: 620px;
  width: 100%;
  border-collapse: separate;
  border-spacing: 0;
}
.active-jobs tbody th {
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
.active-jobs td {
  font-size: 12px;
  white-space: nowrap;
}
.job-time {
  color: #858d99;
  font-size: 11px !important;
}
.job-action {
  color: #315dcf;
}
</style>
