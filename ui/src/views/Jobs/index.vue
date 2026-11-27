<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { computed, onBeforeUnmount, shallowRef, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useIntervalFn } from '@vueuse/core'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Field, FieldLabel } from '@/components/ui/field'
import { Separator } from '@/components/ui/separator'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import AgentRunPermissions from '@/components/AgentRunPermissions.vue'
import PiReadiness from '@/components/PiReadiness.vue'
import { errorMessage, dateLabel, ApiError } from '@/services/workbench'
import {
  getJobs,
  getJob,
  cancelJob,
  answerJob,
  terminalJob,
  objectValue,
  readableValue,
  type JobRecord,
  type PiModel
} from '@/services/workbench-platform'
import { getAgentRun, startAgent, type AgentRun } from '@/services/workbench-agent'
import { useWorkbench } from '@/hooks/useWorkbench'
import { registerWorkbenchRouteReads } from '@/hooks/useWorkbenchReads'
import { useJobEvents } from './hooks/useJobEvents'
import { isPiQuestion } from './schemas/conversation'
import { jobTitle, resultFacts } from './schemas/job'
import JobInputForm from './components/JobInputForm.vue'
import AgentDiffPanel from './components/AgentDiffPanel.vue'
import JobActivity from './components/JobActivity.vue'
import JobConversation from './components/JobConversation.vue'
import JobList from './components/JobList.vue'
import JobDisclosure from './components/JobDisclosure.vue'
import JobStatus from './components/JobStatus.vue'
import RunArtifacts from './components/RunArtifacts.vue'

const route = useRoute()
const router = useRouter()
const { snapshot, refresh: refreshWorkbench } = useWorkbench()
const jobs = shallowRef<JobRecord[]>([])
const job = shallowRef<JobRecord>()
const run = shallowRef<AgentRun>()
const error = shallowRef('')
type JobOperation = { kind: 'stop' | 'resume' } | { kind: 'answer'; requestId: string }
const operation = shallowRef<JobOperation>()
const busy = computed(() => !!operation.value)
const answeringRequest = computed(() => operation.value?.kind === 'answer' ? operation.value.requestId : undefined)
const loading = shallowRef(false)
const readAt = shallowRef<string>()
const jobsLoaded = shallowRef(false)
const selectedTab = shallowRef<string>()
const tab = computed({
  get: () => selectedTab.value ?? (isPi.value ? 'conversation' : 'log'),
  set: (value: string | number) => { selectedTab.value = String(value) }
})
const filesVisited = shallowRef(false)
const continuationOpen = shallowRef(false)
const defaultContinuation = '请根据已有运行结果继续处理。'
const continuation = shallowRef(defaultContinuation)
const readOnly = shallowRef(true)
const allowCommands = shallowRef(false)
const readyModel = shallowRef<PiModel>()
watch(continuationOpen, (open) => {
  if (!open) {
    readOnly.value = true
    allowCommands.value = false
    readyModel.value = undefined
  }
})
const stopRequestedFor = shallowRef('')
const jobId = computed(() => String(route.params.jobId ?? ''))
const activityTarget = computed(() => {
  const value = route.query.activity
  if (value === undefined) return undefined
  if (typeof value !== 'string') return null
  const id = Number(value)
  return Number.isSafeInteger(id) && id > 0 && String(id) === value ? id : null
})
const showExecutionDetail = computed(() => route.query.detail === '1' || activityTarget.value !== undefined)
watch(
  [jobId, () => route.query.tab, () => route.query.activity],
  ([id, requestedTab, activity], [previousJobId, previousTab]) => {
    if (activity !== undefined || requestedTab === 'log') selectedTab.value = 'log'
    else if (id !== previousJobId || requestedTab !== previousTab) selectedTab.value = undefined
  },
  { immediate: true }
)
function clearActivityTarget() {
  if (route.query.activity === undefined) return
  const query = { ...route.query }
  delete query.activity
  void router.replace({ query: { ...query, detail: '1', tab: 'log' } })
}
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
const piRoute = computed(() => isPi.value && run.value && job.value && {
  name: 'pi-workspace',
  params: { specId: run.value.specId },
  query: { session: run.value.id, round: job.value.id }
})
watch(piRoute, (target) => {
  if (target && !showExecutionDetail.value) void router.replace(target)
})
const currentRun = computed(() => (run.value?.jobId === jobId.value ? run.value : undefined))
const artifactRun = computed(() => currentRun.value ?? (run.value?.workspaceMode === 'direct' ? run.value : undefined))
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
let pendingRead: { target: string; controller: AbortController; promise: Promise<void> } | undefined
function refresh(showLoading = true) {
  const target = jobId.value
  if (showLoading) loading.value = true
  if (pendingRead?.target === target) return pendingRead.promise
  pendingRead?.controller.abort()
  const controller = new AbortController()
  const promise = loadJobResults(target, ++refreshGeneration, controller.signal)
  pendingRead = { target, controller, promise }
  return promise
}
registerWorkbenchRouteReads({ busy: loading, readAt, refresh })

async function loadJobResults(target: string, own: number, signal: AbortSignal) {
  try {
    if (target) {
      const value = await getJob(target, signal)
      if (!active || target !== jobId.value || own !== refreshGeneration) return
      job.value = value
      if (runId.value) {
        try {
          const currentRun = await getAgentRun(runId.value, signal)
          if (active && own === refreshGeneration) run.value = currentRun
        } catch (cause) {
          if (!(cause instanceof ApiError && cause.status === 404 && !terminalJob(value.status)))
            throw cause
        }
      }
    } else {
      const currentJobs = await getJobs(signal)
      if (active && own === refreshGeneration) {
        jobs.value = currentJobs
        jobsLoaded.value = true
      }
    }
    if (active && own === refreshGeneration) {
      error.value = ''
      readAt.value = new Date().toISOString()
    }
  } catch (cause) {
    if (active && own === refreshGeneration) error.value = errorMessage(cause)
  } finally {
    if (own === refreshGeneration) {
      pendingRead = undefined
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
watch(artifactRun, (value) => {
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
    pendingRead?.controller.abort()
    pendingRead = undefined
    readAt.value = undefined
    job.value = undefined
    run.value = undefined
    filesVisited.value = false
    continuationOpen.value = false
    continuation.value = defaultContinuation
    readOnly.value = true
    allowCommands.value = false
    operation.value = undefined
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
  pendingRead?.controller.abort()
  clearTimeout(pendingRefresh)
})
function currentOperation(owner: JobOperation) {
  return active && operation.value === owner
}
async function stop() {
  const current = job.value
  if (!current || operationBusy.value) return
  const owner: JobOperation = { kind: 'stop' }
  operation.value = owner
  try {
    const value = await cancelJob(current.id)
    if (!currentOperation(owner)) return
    job.value = value
    stopRequestedFor.value = current.id
    error.value = ''
  } catch (cause) {
    if (currentOperation(owner)) error.value = errorMessage(cause)
  } finally {
    if (currentOperation(owner)) operation.value = undefined
  }
}
async function answer(requestId: string, value: unknown, reportToPage = false) {
  const target = jobId.value
  if (operationBusy.value) return
  const owner: JobOperation = { kind: 'answer', requestId }
  operation.value = owner
  error.value = ''
  try {
    await answerJob(target, requestId, value)
    if (!currentOperation(owner)) return
    eventStream.resolved(requestId)
    await refresh(false)
  } catch (cause) {
    if (!currentOperation(owner)) return
    const message = errorMessage(cause)
    if (reportToPage) error.value = message
    else return message
  } finally {
    if (currentOperation(owner)) operation.value = undefined
  }
}
async function answerLegacy(requestId: string, value: unknown) {
  await answer(requestId, value, true)
}
function openPendingQuestion() {
  const pending = conversation.pending.value
  if (!pending || finished.value) return
  tab.value = 'conversation'
  void conversationPanel.value?.focusQuestion(pending.id)
}
function toggleContinuation() {
  const issue = job.value?.error?.context?.piIssue
  const previousPrompt = currentRun.value?.prompt
  if (
    !continuationOpen.value && continuation.value === defaultContinuation && previousPrompt &&
    (['subscription', 'credentials', 'modelUnavailable', 'modelAccess'].includes(String(issue)) ||
      job.value?.error?.message.startsWith('Pi 模型认证失败'))
  ) {
    continuation.value = previousPrompt
  }
  continuationOpen.value = !continuationOpen.value
}
async function resume() {
  const current = run.value
  const target = jobId.value
  const expectedModel = readyModel.value
  if (
    !current?.canResume || current.projectAccess?.length ||
    current.jobId !== target ||
    latestRunActive.value ||
    busy.value ||
    !expectedModel ||
    !continuation.value.trim()
  )
    return
  const owner: JobOperation = { kind: 'resume' }
  operation.value = owner
  error.value = ''
  try {
    const value = await startAgent({
      specId: current.specId,
      prompt: continuation.value,
      projectIds: current.projectIds,
      resumeRunId: current.id,
      expectedJobId: target,
      expectedModel,
      readOnly: readOnly.value,
      allowCommands: !readOnly.value && allowCommands.value
    })
    if (!currentOperation(owner)) return
    await router.push({ path: `/jobs/${value.id}`, query: { run: current.id } })
  } catch (cause) {
    if (currentOperation(owner)) error.value = errorMessage(cause)
  } finally {
    if (currentOperation(owner)) operation.value = undefined
  }
}
</script>

<template>
  <div class="page-heading jobs-heading">
    <div>
      <RouterLink v-if="jobId" to="/jobs" class="back-link">← 执行记录</RouterLink>
      <RouterLink v-if="piRoute && showExecutionDetail" :to="piRoute" class="back-link ml-3">打开 Pi 会话</RouterLink>
      <h1>{{ jobId ? (job ? jobTitle(job) : '执行详情') : '执行记录' }}</h1>
    </div>
    <Button type="button" variant="outline" :aria-disabled="loading" @click="!loading && refresh()">
      <LoadingIndicator v-if="job" :active="loading" />
      {{ loading ? '刷新中…' : '刷新' }}
    </Button>
  </div>
  <Alert v-if="error" variant="destructive" class="notice error">
    <AlertDescription class="text-current">{{ error }}</AlertDescription>
  </Alert>
  <JobList
    v-if="!jobId"
    :jobs="jobs"
    :loaded="jobsLoaded"
    :loading="loading"
    :failed="!!error"
    :project-names="projectNames"
  />
  <template v-else-if="job">
    <section class="platform-section job-overview" :aria-busy="loading">
      <div class="section-heading">
        <div class="job-overview-title">
          <h2>{{ terminalJob(job.status) ? '执行结果' : '当前进展' }}</h2>
          <JobStatus :status="job.status" :stopping="stopping" role="status" />
          <span class="muted">{{ projectNames(job) || '本机工作台' }}<template v-if="run?.workspaceMode === 'direct'"> · 原工程运行</template></span>
        </div>
        <Button
          v-if="!terminalJob(job.status)"
          type="button"
          variant="outline"
          :disabled="operationBusy"
          @click="stop"
        >
          <LoadingIndicator :active="operation?.kind === 'stop' || stopping" />
          {{ operation?.kind === 'stop' || stopping ? '正在停止…' : '停止运行' }}
        </Button>
        <Button v-else-if="currentRun?.canResume && currentRun.projectAccess?.length && !latestRunActive && piRoute"
          variant="outline" as-child>
          <RouterLink :to="piRoute">在 Pi 工作区继续处理</RouterLink>
        </Button>
        <Button
          v-else-if="currentRun?.canResume && !currentRun.projectAccess?.length && !latestRunActive"
          type="button"
          variant="outline"
          :aria-expanded="continuationOpen"
          :aria-controls="continuationOpen ? 'continuation-form' : undefined"
          @click="toggleContinuation"
        >
          继续处理
        </Button>
      </div>
      <Alert v-if="job.error" variant="destructive" class="notice error">
        <AlertDescription class="text-current">{{ job.error.message }}</AlertDescription>
      </Alert>
      <p v-if="isPi && job.status === 'failed' && currentRun?.canResume" class="muted">
        如需更换模型，可
        <RouterLink :to="{ path: '/specs/' + encodeURIComponent(currentRun.specId), query: { tab: 'run' } }" class="underline">
          返回原 Spec 选择模型并发起新运行
        </RouterLink>。
      </p>
      <p v-if="stopping" class="muted" role="status">停止请求已提交，正在等待进程退出并保存本次记录。</p>
      <p v-if="run?.workspaceMode === 'direct' && (eventStream.latest.value?.piSettings?.readOnly ?? currentRun?.readOnly) === false &&
        (stopping || (terminalJob(job.status) && !['completed', 'succeeded'].includes(job.status)))" class="muted">
        已写入的内容会保留在原工程。
      </p>
      <Alert v-if="currentRun?.workspaceMode === 'direct' && currentRun.snapshotError" class="notice warning" role="status">
        <AlertDescription class="text-current">本轮差异待核实。{{ currentRun.snapshotError }}</AlertDescription>
      </Alert>
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
        :aria-busy="operation?.kind === 'resume'"
        @submit.prevent="resume"
      >
        <PiReadiness v-model:ready-model="readyModel" :resume-run-id="currentRun.id" :busy="busy" />
        <Field class="gap-1.5">
          <FieldLabel for="job-continuation">继续处理的内容</FieldLabel>
          <Textarea
            id="job-continuation"
            v-model="continuation"
            name="continuation"
            rows="3"
            required
            :disabled="busy"
          />
        </Field>
        <AgentRunPermissions v-model:read-only="readOnly" v-model:allow-commands="allowCommands" :disabled="busy" />
        <div class="form-actions">
          <Button type="submit" :disabled="busy || !readyModel || !continuation.trim()">
            <LoadingIndicator :active="operation?.kind === 'resume'" />
            {{ operation?.kind === 'resume' ? '正在续接…' : '续接 Pi' }}
          </Button>
        </div>
      </form>
      <p v-if="currentRun && terminalJob(job.status) && !currentRun.canResume" class="muted">
        {{ currentRun.resumeIssue || (currentRun.workspaceMode !== 'direct' ? '这是历史隔离运行。' : '当前会话不可续接。') }}
        <RouterLink :to="'/specs/' + encodeURIComponent(currentRun.specId)" class="underline">
          返回 Spec 发起新运行
        </RouterLink>
      </p>
      <Separator class="execution-separator" />
      <JobDisclosure>
        <template #label>工程位置与执行信息</template>
        <div class="execution-facts">
          <p v-for="root in job.roots" :key="root" class="path-text">{{ root }}</p>
          <p class="muted">
            开始于 {{ dateLabel(job.createdAt) }} · 最近更新 {{ dateLabel(job.updatedAt) }}
          </p>
          <p class="path-text">执行编号 {{ job.id }}</p>
        </div>
        <JobDisclosure v-if="currentRun?.hostSettings">
          <template #label>本次 {{ currentRun.host === 'pi' ? 'Pi' : 'Codex' }} 模型与权限</template>
          <pre>{{ readableValue(currentRun.hostSettings) }}</pre>
        </JobDisclosure>
        <JobDisclosure v-if="job.result || job.error">
          <template #label>完整结果与错误详情</template>
          <pre>{{ readableValue(job.error ?? job.result) }}</pre>
        </JobDisclosure>
      </JobDisclosure>
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
      :answering-request="answeringRequest"
      @answer="answerLegacy"
    />
    <Tabs v-model="tab" activation-mode="manual" class="job-tabs min-w-0 gap-0">
      <TabsList class="tabs job-tablist w-full justify-start rounded-none bg-transparent p-0"
        aria-label="执行详情内容" moving-indicator>
        <TabsTrigger v-if="isPi" value="conversation">会话</TabsTrigger>
        <TabsTrigger value="log">执行日志</TabsTrigger>
        <template v-if="artifactRun">
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
          :answering-request="answeringRequest"
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
          :activity-target="activityTarget"
          @retry="eventStream.retry"
          @clear-activity-target="clearActivityTarget"
        />
      </TabsContent>
      <template v-if="artifactRun">
        <TabsContent value="artifacts">
          <RunArtifacts
            v-if="tab === 'artifacts'"
            :run-id="artifactRun.id"
            :spec-id="artifactRun.specId"
            :job-id="job.id"
            :direct="artifactRun.workspaceMode === 'direct'"
            :running="!terminalJob(job.status)"
          />
        </TabsContent>
        <TabsContent value="files" force-mount v-show="tab === 'files'">
          <AgentDiffPanel
            v-if="filesVisited"
            :key="job.id"
            :run-id="artifactRun.id"
            :job-id="job.id"
            :direct="artifactRun.workspaceMode === 'direct'"
            :running="!terminalJob(job.status)"
            :visible="tab === 'files'"
            @accepted="refreshWorkbench"
          />
        </TabsContent>
      </template>
    </Tabs>
  </template>
  <LoadingSkeleton v-else-if="loading" variant="detail" label="正在读取执行详情" />
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
.jobs-heading button[aria-disabled='true'] {
  opacity: 0.65;
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
.execution-separator {
  margin-block: 16px 8px;
}
.execution-facts {
  display: grid;
  gap: 6px;
  padding: 4px 12px 8px;
}
.job-tablist > [role='tab'] {
  flex: none;
  height: 100%;
  border-radius: 0;
  box-shadow: none;
}
.job-tablist > [role='tab']:focus-visible {
  outline-offset: -3px;
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
@media (width < 720px) {
  .job-tablist {
    min-height: 44px;
    gap: 18px;
  }
}
</style>
