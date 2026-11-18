<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useIntervalFn } from '@vueuse/core'
import { Button } from '@/components/ui/button'
import AgentCommandPermission from '@/components/AgentCommandPermission.vue'
import { TabsRoot, TabsList, TabsTrigger, TabsContent } from 'reka-ui'
import { errorMessage, dateLabel, ApiError } from '@/services/workbench'
import {
  getJobs,
  getJob,
  cancelJob,
  answerJob,
  terminalJob,
  objectValue,
  readableValue,
  type JobRecord
} from '@/services/workbench-platform'
import { getAgentRun, startAgent, type AgentRun } from '@/services/workbench-agent'
import { useWorkbench } from '@/hooks/useWorkbench'
import { useJobEvents } from './hooks/useJobEvents'
import { isPiQuestion } from './schemas/conversation'
import { jobTitle, resultFacts } from './schemas/job'
import JobInputForm from './components/JobInputForm.vue'
import AgentDiffPanel from './components/AgentDiffPanel.vue'
import JobActivity from './components/JobActivity.vue'
import JobConversation from './components/JobConversation.vue'
import JobList from './components/JobList.vue'
import JobStatus from './components/JobStatus.vue'
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
const jobsLoaded = shallowRef(false)
const selectedTab = shallowRef<string>()
const tab = computed({
  get: () => selectedTab.value ?? (isPi.value ? 'conversation' : 'log'),
  set: (value: string) => { selectedTab.value = value }
})
const filesVisited = shallowRef(false)
const continuationOpen = shallowRef(false)
const continuation = shallowRef('请根据已有运行结果继续处理。')
const allowCommands = shallowRef(false)
watch(continuationOpen, (open) => {
  if (!open) allowCommands.value = false
})
const stopRequestedFor = shallowRef('')
const jobId = computed(() => String(route.params.jobId ?? ''))
const eventStream = useJobEvents(jobId, scheduleRefresh)
const conversation = eventStream.conversation
const conversationPanel = useTemplateRef<InstanceType<typeof JobConversation>>('conversationPanel')
const isPi = computed(() => eventStream.latest.value?.host === 'pi')
watch(tab, (value) => {
  if (value === 'files') filesVisited.value = true
})
const finished = computed(() => !!eventStream.latest.value?.terminal || terminalJob(job.value?.status ?? ''))
const legacyInputs = computed(() =>
  eventStream.pendingInputs.value.filter((event) => !isPi.value || !isPiQuestion(event))
)
const runId = computed(
  () => eventStream.latest.value?.runId || String(objectValue(job.value?.result).runId ?? '')
)
const currentRun = computed(() => (run.value?.jobId === jobId.value ? run.value : undefined))
const latestRunActive = computed(() => !!run.value && !terminalJob(run.value.status))
const stopping = computed(
  () => stopRequestedFor.value === jobId.value && !!job.value && !terminalJob(job.value.status)
)
const operationBusy = computed(() => busy.value || stopping.value)
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
      if (active && own === refreshGeneration) {
        jobs.value = currentJobs
        jobsLoaded.value = true
      }
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
  if (!active || pendingRefresh) return
  pendingRefresh = setTimeout(() => {
    pendingRefresh = undefined
    void refresh(false)
  }, 150)
}
const outcome = computed(() => (job.value ? resultFacts(job.value) : []))
const latestMessage = computed(
  () => eventStream.latest.value?.latestMessage?.trim().split(/\n\s*\n/, 1)[0]
)
watch(currentRun, (value) => {
  if (!value) {
    if (selectedTab.value === 'artifacts' || selectedTab.value === 'files')
      selectedTab.value = undefined
    filesVisited.value = false
  }
})
watch(
  jobId,
  () => {
    refreshGeneration++
    fetchingFor = undefined
    job.value = undefined
    run.value = undefined
    selectedTab.value = undefined
    filesVisited.value = false
    continuationOpen.value = false
    continuation.value = '请根据已有运行结果继续处理。'
    allowCommands.value = false
    busy.value = false
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
    if (jobId.value === current.id) error.value = errorMessage(cause)
  } finally {
    if (jobId.value === current.id) busy.value = false
  }
}
async function answer(requestId: string, value: unknown) {
  const target = jobId.value
  if (operationBusy.value) return
  busy.value = true
  error.value = ''
  try {
    await answerJob(target, requestId, value)
    if (target !== jobId.value) return
    eventStream.resolved(requestId)
    await refresh(false)
  } catch (cause) {
    if (jobId.value === target) return errorMessage(cause)
  } finally {
    if (jobId.value === target) busy.value = false
  }
}
async function answerLegacy(requestId: string, value: unknown) {
  const target = jobId.value
  const message = await answer(requestId, value)
  if (message && jobId.value === target) error.value = message
}
function openPendingQuestion() {
  const pending = conversation.pending.value
  if (!pending || finished.value) return
  tab.value = 'conversation'
  void conversationPanel.value?.focusQuestion(pending.id)
}
async function resume() {
  const current = run.value
  const target = jobId.value
  if (
    !current?.canResume ||
    current.jobId !== target ||
    latestRunActive.value ||
    busy.value ||
    !continuation.value.trim()
  )
    return
  busy.value = true
  error.value = ''
  try {
    const value = await startAgent({
      specId: current.specId,
      prompt: continuation.value,
      projectIds: current.projectIds,
      carryFiles: {},
      resumeRunId: current.id,
      allowCommands: allowCommands.value
    })
    if (target !== jobId.value) return
    await router.push({ path: `/jobs/${value.id}`, query: { run: current.id } })
  } catch (cause) {
    if (jobId.value === target) error.value = errorMessage(cause)
  } finally {
    if (jobId.value === target) busy.value = false
  }
}
</script>

<template>
  <div class="page-heading jobs-heading">
    <div>
      <RouterLink v-if="jobId" to="/jobs" class="back-link">← 执行记录</RouterLink>
      <h1>{{ jobId ? (job ? jobTitle(job) : '执行详情') : '执行记录' }}</h1>
    </div>
    <Button type="button" variant="outline" :disabled="loading" @click="refresh()">{{
      loading ? '刷新中…' : '刷新'
    }}</Button>
  </div>
  <p v-if="error" role="alert" class="notice error">{{ error }}</p>
  <JobList v-if="!jobId" :jobs="jobs" :loaded="jobsLoaded" :project-names="projectNames" />
  <template v-else-if="job">
    <section class="platform-section job-overview">
      <div class="section-heading">
        <div class="job-overview-title">
          <h2>{{ terminalJob(job.status) ? '执行结果' : '当前进展' }}</h2>
          <JobStatus :status="job.status" :stopping="stopping" role="status" />
          <span class="muted">{{ projectNames(job) || '本机工作台' }}</span>
        </div>
        <Button
          v-if="!terminalJob(job.status)"
          type="button"
          variant="outline"
          :disabled="operationBusy"
          @click="stop"
          >{{ stopping ? '正在停止…' : '停止运行' }}</Button
        >
        <Button
          v-else-if="currentRun?.canResume && !latestRunActive"
          type="button"
          variant="outline"
          :aria-expanded="continuationOpen"
          aria-controls="continuation-form"
          @click="continuationOpen = !continuationOpen"
        >
          继续处理
        </Button>
      </div>
      <p v-if="job.error" role="alert" class="notice error">{{ job.error.message }}</p>
      <p v-if="stopping" class="muted">停止请求已提交，正在等待进程退出并保存本次记录。</p>
      <p v-if="latestMessage && terminalJob(job.status) && tab === 'log'" class="job-final-message">
        {{ latestMessage }}
      </p>
      <p v-else-if="!isPi && terminalJob(job.status) && job.kind === 'agent' && !job.error" class="muted">
        本轮执行已结束。
      </p>
      <dl v-if="outcome.length" class="outcome-facts">
        <div v-for="fact in outcome" :key="fact.label + fact.value">
          <dt>{{ fact.label }}</dt>
          <dd>{{ fact.value }}</dd>
        </div>
      </dl>
      <form
        v-if="continuationOpen && currentRun?.canResume && terminalJob(job.status) && !latestRunActive"
        id="continuation-form"
        class="form-stack continuation-form"
        aria-label="续接 Pi"
        @submit.prevent="resume"
      >
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
        <AgentCommandPermission v-model="allowCommands" :disabled="busy" />
        <div class="form-actions">
          <Button type="submit" :disabled="busy || !continuation.trim()">{{
            busy ? '正在续接…' : '续接 Pi'
          }}</Button>
        </div>
      </form>
      <p v-if="currentRun && terminalJob(job.status) && !currentRun.canResume" class="muted">
        {{ currentRun.host === 'codex' ? '这是 Codex 历史运行。' : '当前会话不可续接。' }}
        <RouterLink :to="'/specs/' + encodeURIComponent(currentRun.specId)" class="underline">
          返回 Spec 发起新运行
        </RouterLink>
      </p>
      <details class="execution-context">
        <summary>工程位置与执行信息</summary>
        <p v-for="root in job.roots" :key="root" class="path-text">{{ root }}</p>
        <p class="muted">
          开始于 {{ dateLabel(job.createdAt) }} · 最近更新 {{ dateLabel(job.updatedAt) }}
        </p>
        <p class="path-text">执行编号 {{ job.id }}</p>
        <details v-if="currentRun?.hostSettings">
          <summary>本次 {{ currentRun.host === 'pi' ? 'Pi' : 'Codex' }} 模型与权限</summary>
          <pre>{{ readableValue(currentRun.hostSettings) }}</pre>
        </details>
        <details v-if="job.result || job.error">
          <summary>完整结果与错误详情</summary>
          <pre>{{ readableValue(job.error ?? job.result) }}</pre>
        </details>
      </details>
    </section>
    <nav v-if="conversation.page.value?.previousJobId || (run && run.jobId !== job.id)" class="job-related" aria-label="相关执行">
      <RouterLink
        v-if="conversation.page.value?.previousJobId"
        :to="{ path: `/jobs/${conversation.page.value.previousJobId}`, query: { run: runId } }"
      >上一轮</RouterLink>
      <RouterLink
        v-if="run && run.jobId !== job.id"
        :to="{ path: `/jobs/${run.jobId}`, query: { run: run.id } }"
      >查看最新执行与成果</RouterLink>
    </nav>
    <Button
      v-if="isPi && tab !== 'conversation' && !finished && conversation.pending.value"
      type="button"
      variant="outline"
      class="job-pending"
      @click="openPendingQuestion"
    >待答复</Button>
    <JobInputForm
      v-for="event in legacyInputs"
      :key="event.id"
      :event="event"
      :busy="operationBusy"
      @answer="answerLegacy"
    />
    <TabsRoot v-model="tab" activation-mode="manual">
      <TabsList class="tabs" aria-label="执行详情内容">
        <TabsTrigger v-if="isPi" value="conversation">会话</TabsTrigger>
        <TabsTrigger value="log">执行日志</TabsTrigger>
        <template v-if="currentRun">
          <TabsTrigger value="artifacts">本次产物</TabsTrigger>
          <TabsTrigger value="files">文件变更</TabsTrigger>
        </template>
      </TabsList>
      <TabsContent v-if="isPi" value="conversation" force-mount v-show="tab === 'conversation'">
        <JobConversation
          ref="conversationPanel"
          :key="jobId"
          :job-id="jobId"
          :page="conversation.page.value"
          :items="conversation.items.value"
          :terminal="finished"
          :status="eventStream.statusLabel.value"
          :error="eventStream.error.value"
          :has-older="conversation.hasOlder.value"
          :loading-history="conversation.loadingHistory.value"
          :history-error="conversation.historyError.value"
          :busy="operationBusy"
          :submit="answer"
          @retry="eventStream.retry"
          @older="conversation.older(jobId)"
        />
      </TabsContent>
      <TabsContent value="log" force-mount v-show="tab === 'log'">
        <JobActivity
          :key="jobId"
          :job-id="jobId"
          :latest="eventStream.latest.value"
          :status="eventStream.statusLabel.value"
          :error="eventStream.error.value"
          :visible="tab === 'log'"
          @retry="eventStream.retry"
        />
      </TabsContent>
      <template v-if="currentRun">
        <TabsContent value="artifacts">
          <RunArtifacts
            v-if="tab === 'artifacts'"
            :run-id="currentRun.id"
            :spec-id="currentRun.specId"
            :running="latestRunActive"
          />
        </TabsContent>
        <TabsContent value="files" force-mount v-show="tab === 'files'">
          <AgentDiffPanel
            v-if="filesVisited"
            :key="currentRun.id"
            :run-id="currentRun.id"
            :running="!terminalJob(job.status) || latestRunActive"
            :visible="tab === 'files'"
            @accepted="refreshWorkbench"
          />
        </TabsContent>
      </template>
    </TabsRoot>
  </template>
  <p v-else-if="loading" class="empty">正在读取执行详情…</p>
</template>

<style scoped>
.job-related {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  margin-bottom: 12px;
  color: var(--brand-color-primary);
}
.job-related a {
  text-decoration: underline;
  text-underline-offset: 0.18em;
}
.job-pending {
  margin-bottom: 12px;
}
.jobs-heading h1 {
  overflow-wrap: anywhere;
}
.job-overview-title {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.job-final-message {
  margin: 10px 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.8;
  max-height: 6em;
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
.continuation-form {
  margin-block: 14px;
}
</style>
