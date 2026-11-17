<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useIntervalFn } from '@vueuse/core'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import SingleSelect from '@/components/SingleSelect.vue'
import { errorMessage, dateLabel, ApiError } from '@/services/workbench'
import {
  getJobs,
  getJob,
  cancelJob,
  answerJob,
  jobStatusLabels,
  terminalJob,
  objectValue,
  readableValue,
  type JobRecord
} from '@/services/workbench-platform'
import { getAgentRun, startAgent, type AgentRun } from '@/services/workbench-agent'
import { useWorkbench } from '@/hooks/useWorkbench'
import { useJobEvents } from './hooks/useJobEvents'
import { jobActivity, jobKindLabels, resultFacts } from './schemas/job'
import JobInputForm from './components/JobInputForm.vue'
import AgentDiffPanel from './components/AgentDiffPanel.vue'
import JobActivity from './components/JobActivity.vue'
import PendingRequests from './components/PendingRequests.vue'
import RunArtifacts from './components/RunArtifacts.vue'

const route = useRoute()
const router = useRouter()
const { snapshot, refresh: refreshWorkbench } = useWorkbench()
const jobs = shallowRef<JobRecord[]>([])
const job = shallowRef<JobRecord>()
const run = shallowRef<AgentRun>()
const error = shallowRef('')
const busy = shallowRef(false)
const loading = shallowRef(false)
const filter = shallowRef('all')
const kind = shallowRef('all')
const search = shallowRef('')
const continuation = shallowRef('请根据已有运行结果继续处理。')
const stopRequestedFor = shallowRef('')
const jobId = computed(() => String(route.params.jobId ?? ''))
const detectedRunId = shallowRef('')
const runId = computed(() =>
  typeof route.query.run === 'string'
    ? route.query.run
    : detectedRunId.value || String(objectValue(job.value?.result).runId ?? '')
)
const latestRunActive = computed(() => !!run.value && !terminalJob(run.value.status))
const stopping = computed(
  () => stopRequestedFor.value === jobId.value && !!job.value && !terminalJob(job.value.status)
)
const operationBusy = computed(() => busy.value || stopping.value)
const filterOptions = [
  { value: 'all', label: '全部执行' },
  { value: 'active', label: '运行中与待输入' },
  { value: 'attention', label: '需要处理' },
  { value: 'terminal', label: '已结束' }
]
const kindOptions = computed(() => [
  { value: 'all', label: '全部类型' },
  ...[...new Set(jobs.value.map((item) => item.kind))].map((value) => ({
    value,
    label: jobKindLabels[value] ?? value
  }))
])
const visibleJobs = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  return jobs.value
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
          [item.title, jobKindLabels[item.kind] ?? item.kind, ...item.roots, projectNames(item)]
            .join(' ')
            .toLocaleLowerCase()
            .includes(query))
      )
    })
    .sort((left, right) => Date.parse(right.updatedAt) - Date.parse(left.updatedAt))
})
const hasFilters = computed(() => filter.value !== 'all' || kind.value !== 'all' || !!search.value)
function projectNames(item: JobRecord) {
  return item.roots
    .map(
      (root) =>
        snapshot.value?.projects.find((project) => project.record.rootPath === root)?.record.name ??
        root.split(/[\\/]/).filter(Boolean).at(-1) ??
        root
    )
    .join('、')
}
function clearFilters() {
  filter.value = 'all'
  kind.value = 'all'
  search.value = ''
}
let active = true
let refreshGeneration = 0
let pendingRefresh: ReturnType<typeof setTimeout> | undefined
let fetchingFor: string | undefined
async function refresh(showLoading = true) {
  const target = jobId.value
  if (fetchingFor === target) return
  fetchingFor = target
  const own = ++refreshGeneration
  if (showLoading) loading.value = true
  try {
    if (target) {
      const value = await getJob(target)
      if (!active || target !== jobId.value || own !== refreshGeneration) return
      job.value = value
      if (runId.value) {
        try {
          const currentRun = await getAgentRun(runId.value)
          if (active && own === refreshGeneration) run.value = currentRun
        } catch (cause) {
          if (!(cause instanceof ApiError && cause.status === 404 && !terminalJob(value.status)))
            throw cause
        }
      }
    } else {
      const currentJobs = await getJobs()
      if (active && own === refreshGeneration) jobs.value = currentJobs
    }
    if (active && own === refreshGeneration) error.value = ''
  } catch (cause) {
    if (active && own === refreshGeneration) error.value = errorMessage(cause)
  } finally {
    if (own === refreshGeneration) {
      fetchingFor = undefined
      loading.value = false
    }
  }
}
function scheduleRefresh() {
  if (pendingRefresh) return
  pendingRefresh = setTimeout(() => {
    pendingRefresh = undefined
    void refresh(false)
  }, 150)
}
const eventStream = useJobEvents(
  jobId,
  computed(() => job.value?.id === jobId.value && terminalJob(job.value.status)),
  scheduleRefresh
)
const outcome = computed(() => (job.value ? resultFacts(job.value) : []))
const latestMessage = computed(() => {
  if (!job.value || !terminalJob(job.value.status)) return ''
  return (
    jobActivity(eventStream.events.value).findLast((entry) => entry.kind === 'message')?.text ?? ''
  )
})
watch(eventStream.runId, (value) => {
  if (!value) return
  detectedRunId.value = value
  scheduleRefresh()
})
watch(
  jobId,
  () => {
    refreshGeneration++
    fetchingFor = undefined
    job.value = undefined
    run.value = undefined
    detectedRunId.value = ''
    stopRequestedFor.value = ''
    error.value = ''
    void refresh()
  },
  { immediate: true }
)
useIntervalFn(() => {
  if (!document.hidden) void refresh(false)
}, 5000)
onBeforeUnmount(() => {
  active = false
  refreshGeneration++
  clearTimeout(pendingRefresh)
})
async function stop() {
  const current = job.value
  if (!current || operationBusy.value) return
  busy.value = true
  try {
    const value = await cancelJob(current.id)
    if (jobId.value !== current.id) return
    job.value = value
    stopRequestedFor.value = current.id
    error.value = ''
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
async function answer(requestId: string, value: unknown) {
  const target = jobId.value
  busy.value = true
  error.value = ''
  try {
    await answerJob(target, requestId, value)
    if (target !== jobId.value) return
    eventStream.resolved(requestId)
    await refresh(false)
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
async function resume() {
  const current = run.value
  const target = jobId.value
  if (!current || busy.value || !continuation.value.trim()) return
  busy.value = true
  error.value = ''
  try {
    const value = await startAgent({
      specId: current.specId,
      prompt: continuation.value,
      projectIds: current.projectIds,
      carryFiles: {},
      resumeRunId: current.id
    })
    if (target !== jobId.value) return
    await router.push({ path: `/jobs/${value.id}`, query: { run: current.id } })
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="page-heading jobs-heading">
    <div>
      <RouterLink v-if="jobId" to="/jobs" class="back-link">← 执行记录</RouterLink>
      <h1>{{ jobId ? (job?.title ?? '执行详情') : '执行记录' }}</h1>
    </div>
    <Button type="button" variant="outline" :disabled="loading" @click="refresh()">{{
      loading ? '刷新中…' : '刷新'
    }}</Button>
  </div>
  <p v-if="error" role="alert" class="notice error">{{ error }}</p>
  <template v-if="!jobId">
    <PendingRequests />
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
      <Button v-if="hasFilters" type="button" variant="ghost" @click="clearFilters"
        >清除筛选</Button
      >
      <span class="muted job-count">{{ visibleJobs.length }} 条</span>
    </div>
    <section class="panel jobs-panel">
      <div
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
                <RouterLink :to="`/jobs/${item.id}`">{{ item.title }}</RouterLink>
              </th>
              <td>
                <span class="job-state" :data-state="item.status">{{
                  jobStatusLabels[item.status] ?? item.status
                }}</span>
              </td>
              <td>{{ jobKindLabels[item.kind] ?? item.kind }}</td>
              <td>
                <details class="job-target">
                  <summary>{{ projectNames(item) || '本机工作台' }}</summary>
                  <p v-for="root in item.roots" :key="root" class="path-text">{{ root }}</p>
                </details>
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
  <template v-else-if="job">
    <section class="platform-section job-overview">
      <div class="section-heading">
        <div class="job-overview-title">
          <h2>{{ terminalJob(job.status) ? '执行结果' : '当前进展' }}</h2>
          <span class="job-state" :data-state="job.status" role="status">{{
            stopping ? '正在停止…' : (jobStatusLabels[job.status] ?? job.status)
          }}</span>
        </div>
        <Button
          v-if="!terminalJob(job.status)"
          type="button"
          variant="outline"
          :disabled="operationBusy"
          @click="stop"
          >{{ stopping ? '正在停止…' : '停止运行' }}</Button
        >
      </div>
      <p v-if="job.error" role="alert" class="notice error">{{ job.error.message }}</p>
      <p v-if="stopping" class="muted">停止请求已提交，正在等待进程退出并保存本次记录。</p>
      <p v-if="latestMessage" class="job-final-message">{{ latestMessage }}</p>
      <p v-else-if="terminalJob(job.status) && job.kind === 'agent' && !job.error" class="muted">
        本轮运行已结束，可在下方阅读产物并审阅文件差异。
      </p>
      <dl v-if="outcome.length" class="outcome-facts">
        <div v-for="fact in outcome" :key="fact.label + fact.value">
          <dt>{{ fact.label }}</dt>
          <dd>{{ fact.value }}</dd>
        </div>
      </dl>
      <details class="execution-context">
        <summary>工程位置与执行信息</summary>
        <p v-for="root in job.roots" :key="root" class="path-text">{{ root }}</p>
        <p class="muted">
          开始于 {{ dateLabel(job.createdAt) }} · 最近更新 {{ dateLabel(job.updatedAt) }}
        </p>
        <p class="path-text">执行编号 {{ job.id }}</p>
        <details v-if="run?.hostSettings">
          <summary>本次 Codex 模型与权限</summary>
          <pre>{{ readableValue(run.hostSettings) }}</pre>
        </details>
        <details v-if="job.result || job.error">
          <summary>完整结果与错误详情</summary>
          <pre>{{ readableValue(job.error ?? job.result) }}</pre>
        </details>
      </details>
    </section>
    <p v-if="run && run.jobId !== job.id" class="notice">
      这次任务已有后续运行，<RouterLink :to="{ path: `/jobs/${run.jobId}`, query: { run: run.id } }"
        >查看最新执行与成果</RouterLink
      >。
    </p>
    <JobInputForm
      v-for="event in eventStream.pendingInputs.value"
      :key="event.id"
      :event="event"
      :busy="operationBusy"
      @answer="answer"
    />
    <section
      v-if="run && terminalJob(job.status) && !latestRunActive"
      class="platform-section continuation-section"
    >
      <details>
        <summary>继续处理这次任务</summary>
        <form class="form-stack" aria-label="续接 Codex" @submit.prevent="resume">
          <label for="job-continuation"
            >继续处理的内容<textarea
              id="job-continuation"
              v-model="continuation"
              name="continuation"
              rows="3"
              required
              :disabled="busy"
            />
          </label>
          <div class="form-actions">
            <Button :disabled="busy || !continuation.trim()">{{
              busy ? '正在续接…' : '续接 Codex'
            }}</Button>
          </div>
        </form>
      </details>
    </section>
    <template v-if="runId">
      <RunArtifacts
        v-if="run"
        :run-id="runId"
        :spec-id="run.specId"
        :running="!terminalJob(job.status) || latestRunActive"
      />
      <AgentDiffPanel
        :key="runId"
        :run-id="runId"
        :running="!terminalJob(job.status) || latestRunActive"
        @accepted="refreshWorkbench"
      />
    </template>
    <JobActivity
      :key="jobId"
      :job-id="jobId"
      :events="eventStream.events.value"
      :status="eventStream.statusLabel.value"
      :error="eventStream.error.value"
      :complete="eventStream.complete.value"
      :dropped="eventStream.dropped.value"
    />
  </template>
  <p v-else-if="loading" class="empty">正在读取执行详情…</p>
</template>

<style scoped>
.jobs-heading h1 {
  overflow-wrap: anywhere;
}
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
.job-target {
  max-width: 240px;
}
.job-target summary {
  max-width: 240px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
}
.job-target p {
  white-space: normal;
  margin-block: 8px;
}
.job-time {
  white-space: nowrap;
}
.job-state {
  display: inline-flex;
  align-items: center;
  min-height: 24px;
  padding: 2px 8px;
  border-radius: 5px;
  background: var(--brand-color-fill-secondary);
  font-size: 12px;
  white-space: nowrap;
}
.job-state[data-state='running'],
.job-state[data-state='preparing'] {
  color: var(--yss-color-primary-control);
  background: var(--yss-colors-primary-bg);
}
.job-state[data-state='failed'],
.job-state[data-state='recoveryRequired'] {
  color: var(--yss-colors-error-text);
}
.job-state[data-state='waitingInput'],
.job-state[data-state='waitingApproval'] {
  color: #92600a;
  background: #fff6e6;
}
.job-overview-title {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.job-final-message {
  margin: 16px 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.8;
  max-height: 360px;
  overflow: auto;
}
.execution-context {
  margin-top: 16px;
  padding-top: 12px;
  border-top: 1px solid var(--yss-colors-border);
}
.execution-context > p {
  margin-block: 8px;
}
.outcome-facts {
  margin: 14px 0;
}
.outcome-facts > div {
  display: grid;
  grid-template-columns: 90px minmax(0, 1fr);
  gap: 12px;
  padding: 5px 0;
}
.outcome-facts dt {
  color: var(--brand-color-text-secondary);
}
.outcome-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.continuation-section form {
  margin-top: 14px;
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
  .job-final-message {
    max-height: 300px;
  }
}
</style>
