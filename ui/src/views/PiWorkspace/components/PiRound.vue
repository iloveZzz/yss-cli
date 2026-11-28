<script setup lang="ts">
import type { RouteLocationRaw } from 'vue-router'
import type { AgentHandoff } from '@/services/workbench-agent'
import { computed, nextTick, shallowRef, toRef, useTemplateRef, watch } from 'vue'
import { ArrowUpRight, ChevronRight, Flag, Wrench } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Separator } from '@/components/ui/separator'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { errorMessage, dateLabel } from '@/services/workbench'
import { agentHostLabels, agentPhaseLabel, answerJob, jobStatusLabels, objectValue, piCommandExecutionLabels, piThinkingLabels, terminalJob,
  type AgentHost, type JobActivity, type JobRecord, type PiCommandExecution } from '@/services/workbench-platform'
import { useJobEvents } from '@/views/Jobs/hooks/useJobEvents'
import { activityFailed } from '@/views/Jobs/schemas/job'
import ConversationMessage from '@/views/Jobs/components/ConversationMessage.vue'
import ConversationQuestion from '@/views/Jobs/components/ConversationQuestion.vue'
import ConversationTool from '@/views/Jobs/components/ConversationTool.vue'
import PiRoundPermissions from '@/views/Jobs/components/PiRoundPermissions.vue'
import PiContextUsage from './PiContextUsage.vue'
import PiTokenUsage from './PiTokenUsage.vue'
import PiPlan from './PiPlan.vue'
import PiReasoning from './PiReasoning.vue'
import PiRoundSummary from './PiRoundSummary.vue'
import PiSubmittedCitations from './PiSubmittedCitations.vue'

const props = defineProps<{
  job: JobRecord
  host: AgentHost | 'codex'
  handoff?: AgentHandoff | null
  handoffSubmitted?: string | null
  handoffSourceLink?: RouteLocationRaw
  latest: boolean
  direct: boolean
  readOnly?: boolean | null
  commandExecution?: PiCommandExecution
  focusActivity?: { id: number }
  stopping: boolean
  canReview: boolean
  canReviewLatest: boolean
  reviewTargets?: [string, string][]
  projectNames: Record<string, string>
  trackFirstText?: boolean
  appendAssistantUi?: (jobId: string, text: string) => Promise<boolean>
}>()
const emit = defineEmits<{
  updated: []
  executionStatus: [jobId: string, label: string, terminal: boolean]
  focus: [node: HTMLElement]
  review: [projectId?: string, path?: string]
  reviewLatest: []
}>()
const round = useTemplateRef<HTMLElement>('round')
const jobId = toRef(() => props.job.id)
const events = useJobEvents(jobId, () => { if (props.latest) emit('updated') }, {
  piRound: true, trackFirstText: () => !!props.trackFirstText
})
const { items, page, hasOlder, loadingHistory, historyError, firstText, toolMotion } = events.conversation
const appendFromRound = computed(() => {
  const append = props.appendAssistantUi
  const id = jobId.value
  return append && props.latest ? (text: string) => append(id, text) : undefined
})
const firstTextRendered = computed(() => !!firstText.value && items.value.some((row) => row.id === firstText.value?.rowId))
watch([firstText, firstTextRendered], ([text, rendered], _, onCleanup) => {
  const target = text && rendered ? round.value?.querySelector(`[data-assistant-row="${text.rowId}"]`) : undefined
  if (!text || !target) return
  const renderedTarget = target
  const { receivedAt, ...detail } = text
  let visible = false
  let measured = false
  let frame: number | undefined
  function recordVisible() {
    if (!visible || measured || document.hidden || frame !== undefined) return
    // The second callback runs after a frame has had a chance to paint the committed text.
    frame = requestAnimationFrame(() => {
      frame = requestAnimationFrame(() => {
        frame = undefined
        if (!visible || measured || document.hidden || !renderedTarget.isConnected || !renderedTarget.textContent?.trim()) return
        measured = true
        textObserver.disconnect()
        const mark = performance.mark('yss.pi.first-text.visible', { detail })
        performance.measure('yss.pi.first-text.receive-to-visible', {
          start: receivedAt, end: mark.startTime, detail
        })
      })
    })
  }
  const observer = new IntersectionObserver(([entry]) => {
    visible = entry?.isIntersecting === true
    recordVisible()
  })
  // An initial delta may only contain hidden Markdown syntax or a pending citation.
  const textObserver = new MutationObserver(recordVisible)
  textObserver.observe(target, { childList: true, characterData: true, subtree: true })
  observer.observe(target)
  document.addEventListener('visibilitychange', recordVisible)
  onCleanup(() => {
    observer.disconnect()
    textObserver.disconnect()
    document.removeEventListener('visibilitychange', recordVisible)
    if (frame !== undefined) cancelAnimationFrame(frame)
  })
}, { flush: 'post' })
const settings = computed(() => events.latest.value?.piSettings)
const commandExecution = computed(() => page.value?.commandExecution ?? settings.value?.commandExecution ??
  events.latest.value?.commandExecution ?? props.job.commandExecution ?? props.commandExecution ??
  (page.value?.allowCommands ? 'sandbox' : undefined))
const onlyReads = computed(() => props.host === 'grok' || (settings.value?.readOnly ?? props.readOnly))
const hostLabel = computed(() => agentHostLabels[props.host])
const roundModel = computed(() => {
  const grok = events.latest.value?.grokConfiguration ?? page.value?.grokConfiguration
  if (grok) return { name: grok.model, provider: grok.provider, level: grok.thinkingLevel, verified: true }
  const model = settings.value?.model
  if (model) return { name: model.id, provider: model.provider, level: settings.value?.thinkingLevel, verified: true }
  const selection = props.job.agentSelection
  return { name: selection?.model, provider: selection?.provider, level: selection?.thinkingLevel, verified: false }
})
const plan = computed(() => events.latest.value?.piPlan)
const terminal = computed(() => terminalJob(props.job.status) || !!events.latest.value?.terminal)
const phase = computed(() => events.piPhase.value?.phase ?? props.job.piPhase)
const status = computed(() => {
  const label = jobStatusLabels[props.job.status] || props.job.status
  if (terminalJob(props.job.status)) return props.job.agentDispatchUncertain ? '结果待核实' : label
  if (terminal.value) return '本轮已结束，正在核对结果'
  if (props.stopping) return '正在停止'
  if (events.statusLabel.value === '读取失败') return '执行记录读取失败'
  if (events.statusLabel.value === '正在重连' || events.error.value) return '正在重新连接执行记录'
  if (events.pendingInputs.value.length || props.job.status === 'waitingInput') return '等待答复'
  if (phase.value === 'finalizeArtifacts') return agentPhaseLabel(phase.value, 'pi')
  if (items.value.some((item) => ['command', 'tool', 'files'].includes(item.kind) && ['inProgress', 'running'].includes(item.status)))
    return '正在执行工具'
  if (items.value.some((item) => item.kind === 'message' && item.text.trim())) {
    return items.value.some((item) => item.kind === 'message' && !item.status) ? '正在生成回复' : '等待后续执行结果'
  }
  if (phase.value === 'promptAccepted' || props.job.agentStartedAt ||
      (props.host === 'grok' && props.job.agentDispatchUncertain))
    return '等待 ' + hostLabel.value + ' 首段回复'
  if (phase.value) return agentPhaseLabel(phase.value, props.host === 'grok' ? 'grok' : 'pi')
  return props.job.status === 'queued' || props.job.status === 'preparing' ? '已受理，正在准备运行' : label
})
watch([() => props.latest, status, terminal], ([isLatest, label, isTerminal]) => {
  if (isLatest) emit('executionStatus', jobId.value, label, isTerminal)
}, { immediate: true })
const ready = computed(() => !!page.value || !!events.error.value || terminal.value)
const answering = shallowRef('')
const inputSync = shallowRef('')
const openRows = shallowRef(new Set<number>())
const filesWithDiff = computed(() => new Set(props.reviewTargets?.map((file) => JSON.stringify(file)) ?? []))
function hasFileDiff(row: JobActivity) {
  return row.projectId && row.path && (filesWithDiff.value.has(JSON.stringify([row.projectId, row.path])) ||
    (props.direct && (row.kind === 'files' || row.operation === 'write' || row.operation === 'edit')))
}

const operations: Record<string, string> = {
  read: '读取', ls: '列出', grep: '搜索', write: '写入', edit: '编辑', bash: '执行离线命令', command: '执行命令', files: '改动文件', compact: '压缩上下文'
}
type Entry = { key: number; rows: JobActivity[]; group: boolean }
const displayedRunningTools = new Set<number>()
watch(items, (rows) => {
  for (const row of rows) {
    if ((row.kind === 'tool' || row.kind === 'files') && row.status === 'inProgress')
      displayedRunningTools.add(row.id)
  }
}, { flush: 'sync' })
function ordinary(row: JobActivity) {
  // A visible running tool keeps its placement even when it becomes eligible for grouping.
  return (row.kind === 'tool' || row.kind === 'files') && !displayedRunningTools.has(row.id)
    && !['inProgress', 'canceled', 'cancelled', 'interrupted'].includes(row.status) && !row.error && !activityFailed(row)
}
const entries = computed(() => {
  const result: Entry[] = []
  for (const row of items.value) {
    const group = ordinary(row)
    const previous = result.at(-1)
    if (previous?.group && group) previous.rows.push(row)
    else result.push({ key: row.id, rows: [row], group })
  }
  return result
})
function activitySummary(rows: JobActivity[]) {
  const counts = new Map<string, number>()
  for (const row of rows) {
    const label = operations[row.operation ?? row.kind] ?? '操作'
    counts.set(label, (counts.get(label) ?? 0) + 1)
  }
  return [...counts].map(([label, count]) => label + ' ' + count + ' 次').join(' · ')
}
function expanded(entry: Entry) {
  return entry.rows.some((row) => openRows.value.has(row.id))
}
function toggle(entry: Entry, open: boolean) {
  const ids = new Set(openRows.value)
  for (const row of entry.rows) {
    if (open) ids.add(row.id)
    else ids.delete(row.id)
  }
  openRows.value = ids
}

function animateExecution(target: Element | null | undefined, entering: boolean) {
  if (!target || document.hidden || !window.matchMedia('(prefers-reduced-motion: no-preference)').matches) return
  const styles = getComputedStyle(target)
  const time = styles.getPropertyValue(entering ? '--workbench-motion-panel' : '--workbench-motion-fast').trim()
  const duration = Number.parseFloat(time) * (time.endsWith('ms') ? 1 : 1000)
  if (!duration) return
  target.animate([
    entering ? { opacity: .55, transform: 'translateY(3px)' } : { opacity: .65, transform: 'scale(.96)' },
    { opacity: 1, transform: 'none' }
  ], { duration, easing: styles.getPropertyValue('--workbench-motion-ease').trim() })
}
watch(toolMotion, (motion) => {
  if (!motion || !round.value) return
  for (const [id, phase] of motion) {
    const tool = round.value.querySelector(`[data-pi-tool-activity="${id}"]`)
    if (phase === 'settled') animateExecution(tool?.querySelector('.tool-state'), false)
    else animateExecution(tool ?? round.value.querySelector(`[data-pi-tool-group="${id}"]`), true)
  }
}, { flush: 'post' })
watch(events.latest, (snapshot, previous) => {
  const liveId = events.livePlanEventId.value
  if (!snapshot?.piPlan || !previous || liveId === undefined || liveId <= previous.cursor || liveId > snapshot.cursor) return
  const prior = previous.piPlan
  if (prior?.length === snapshot.piPlan.length && snapshot.piPlan.every((step, index) =>
    step.title === prior[index].title && step.status === prior[index].status)) return
  animateExecution(round.value?.querySelector('[data-pi-plan-summary]'), !prior?.length)
}, { flush: 'post' })

let lastFocused: typeof props.focusActivity
watch([() => props.focusActivity, ready, () => items.value.length, loadingHistory], async ([request, loaded]) => {
  if (!request) { lastFocused = undefined; return }
  if (!loaded || request === lastFocused || loadingHistory.value) return
  await nextTick()
  if (request !== props.focusActivity) return
  const { id } = request
  const target = round.value?.querySelector<HTMLElement>(id ? '#pi-question-' + id : '[data-round-problem]')
  if (target) {
    lastFocused = request
    emit('focus', target)
  } else if (id && hasOlder.value && !historyError.value) {
    void events.conversation.older(jobId.value)
  }
}, { immediate: true, flush: 'post' })

async function submit(requestId: string, value: unknown) {
  if (!props.latest || answering.value || terminal.value || inputSync.value) return
  answering.value = requestId
  try {
    await answerJob(jobId.value, requestId, value)
    events.resolved(requestId)
    emit('updated')
  } catch (cause) {
    // A request may have reached Pi before the browser lost the response.
    inputSync.value = requestId
    events.retry()
    emit('updated')
    return errorMessage(cause)
  } finally {
    answering.value = ''
  }
}
watch(events.statusLabel, (status) => {
  if (!inputSync.value || (status !== '实时连接' && status !== '已读取记录')) return
  const snapshot = events.latest.value
  if (!snapshot) return
  const request = inputSync.value
  if (!snapshot.terminal && !snapshot.pendingInputs.some((entry) => objectValue(entry.payload).requestId === request)) {
    events.resolved(request)
  }
  inputSync.value = ''
})
</script>

<template>
  <article ref="round" class="pi-round" :aria-label="'执行轮次 ' + dateLabel(job.createdAt)" tabindex="-1">
    <header class="pi-round-meta">
      <time :datetime="job.createdAt">{{ dateLabel(job.createdAt) }}</time>
      <span>{{ status }}</span>
      <span>{{ hostLabel }}</span>
      <span v-if="commandExecution">已授权{{ piCommandExecutionLabels[commandExecution] }}</span>
      <span v-if="onlyReads">{{ direct && host !== 'grok' ? '只读原工程' : '只读分析' }}</span>
      <span v-else-if="direct && onlyReads === false">编辑原工程</span>
      <span v-if="roundModel.name" :title="[roundModel.provider, roundModel.name].filter(Boolean).join(' / ')">
        {{ roundModel.name }}<template v-if="!roundModel.verified">（待核对）</template><template v-if="roundModel.level && roundModel.level !== 'off'"> · {{ piThinkingLabels[roundModel.level] ?? roundModel.level }}推理</template>
      </span>
      <PiContextUsage :usage="events.latest.value?.piContext" :limit="settings?.contextWindow" />
      <RouterLink :to="{ path: '/jobs/' + job.id, query: { detail: '1', tab: 'log' } }">本轮日志 <ArrowUpRight :size="12" /></RouterLink>
    </header>
    <PiRoundPermissions v-if="events.latest.value?.projectAccess?.length" :access="events.latest.value.projectAccess"
      :execution="commandExecution" :names="projectNames" />
    <p v-if="!terminal && events.statusLabel.value === '正在重连'" class="pi-round-note">记录连接中断，正在重连。</p>
    <PiTokenUsage v-if="events.latest.value?.piUsage" :usage="events.latest.value.piUsage" />
    <p v-if="direct && onlyReads === false &&
      (stopping || (terminalJob(job.status) && !['completed', 'succeeded'].includes(job.status)))" class="pi-round-note">
      已写入的内容会保留在原工程。
    </p>
    <ConversationMessage v-if="page?.prompt" :text="page.prompt" user />
    <p v-else-if="page || terminal" class="pi-round-note">本轮未留存原始要求</p>
    <div v-if="handoff || handoffSubmitted || handoffSourceLink" class="mb-2 grid gap-1 rounded-md border p-2.5 text-sm">
      <div class="flex flex-wrap items-center gap-2">
        <strong>{{ handoffSubmitted ? '交接已提交' : terminal && job.agentDispatchUncertain ? '交接提交待核实' : '交接正文尚未确认提交' }}</strong>
        <RouterLink v-if="handoffSourceLink" :to="handoffSourceLink" class="underline underline-offset-4">查看来源轮次</RouterLink>
      </div>
      <details v-if="handoffSubmitted || handoff?.context">
        <summary class="cursor-pointer">{{ handoffSubmitted ? '查看本轮交接内容' : '查看确认的交接草稿' }}</summary>
        <pre class="mt-1 max-h-48 overflow-auto whitespace-pre-wrap break-words">{{ handoffSubmitted || handoff?.context }}</pre>
      </details>
    </div>
    <PiSubmittedCitations v-if="page?.citations?.length" :excerpts="page.citations" :project-names="projectNames" />
    <PiRoundSummary :job-id="jobId" :status="status" :started-at="job.agentStartedAt"
      :terminal="terminal" :summary="events.latest.value?.piRoundSummary" :plan="plan"
      :can-review="terminal && canReview" @review="emit('review')" />
    <div v-if="hasOlder">
      <Button type="button" variant="ghost" size="sm" :aria-disabled="loadingHistory"
        @click="!loadingHistory && events.conversation.older(jobId)">
        <LoadingIndicator :active="loadingHistory" /> 加载本轮更早活动
      </Button>
    </div>
    <div v-if="events.error.value || historyError" class="pi-round-note pi-inline-error" role="status">
      {{ events.error.value || historyError }}
      <Button type="button" variant="ghost" size="sm" @click="events.error.value ? events.retry() : events.conversation.older(jobId)">
        重试读取
      </Button>
    </div>
    <LoadingSkeleton v-if="!page && !terminal && !events.error.value" :rows="2" variant="text" :label="'正在读取 ' + hostLabel + ' 对话'" />
    <p v-else-if="page && !items.length && !terminal && !phase" class="pi-round-note">等待 {{ hostLabel }} 开始答复…</p>
    <PiPlan v-if="plan?.length" :steps="plan" :terminal="terminal" />
    <template v-for="entry in entries" :key="entry.key">
      <Collapsible v-if="entry.group" :open="expanded(entry)" class="pi-tool-group" @update:open="toggle(entry, $event)">
        <CollapsibleTrigger class="pi-tool-trigger" :data-pi-tool-group="entry.key">
          <Wrench :size="15" aria-hidden="true" /> <span>{{ activitySummary(entry.rows) }}</span>
          <ChevronRight :size="14" class="pi-tool-chevron" aria-hidden="true" />
        </CollapsibleTrigger>
        <CollapsibleContent class="pi-tool-steps">
          <div v-for="row in entry.rows" :key="row.id" class="pi-tool-row">
            <ConversationTool :job-id="jobId" :row="row" :terminal="terminal" :data-pi-tool-activity="row.id" />
            <Button v-if="terminal && canReview && hasFileDiff(row)" type="button" variant="ghost" size="sm"
              @click="emit('review', row.projectId, row.path)">查看文件差异</Button>
          </div>
        </CollapsibleContent>
      </Collapsible>
      <template v-else>
        <template v-for="row in entry.rows" :key="row.id">
          <ConversationMessage v-if="row.kind === 'message' || row.kind === 'user'" :row="row"
            :streaming="row.kind === 'message' && !row.status && !terminal" :job-id="jobId" :project-names="projectNames"
            :assistant-label="hostLabel"
            :citation-streaming="row.kind === 'message' && !row.status" :assistant-ui="host === 'pi' || host === 'grok'"
            :round-prompt="page?.prompt" :append-assistant-ui="appendFromRound" />
          <PiReasoning v-else-if="row.kind === 'reasoning'" :row="row" :terminal="terminal" />
          <ConversationQuestion v-else-if="row.question" :row="row" :question="row.question" :terminal="terminal"
            :busy="!!answering || !latest || stopping || !!inputSync" :submitting="row.question.requestId === answering" :submit="submit" />
          <p v-else-if="row.kind === 'error'" class="pi-inline-error preserve-lines">{{ row.text }}</p>
          <ConversationTool v-else :job-id="jobId" :row="row" :terminal="terminal" :data-pi-tool-activity="row.id" />
        </template>
      </template>
    </template>
    <div v-if="job.error || ['interrupted', 'recoveryRequired'].includes(job.status)" tabindex="-1" data-round-problem class="pi-round-problem">
      <p>{{ job.error?.message ?? (job.status === 'interrupted' ? '执行已中断，请核对留存内容。' : '请核实事务和工程状态。') }}</p>
      <RouterLink :to="{ path: '/jobs/' + job.id, query: { detail: '1' } }">查看执行详情</RouterLink>
    </div>
    <div v-if="terminal && canReview" class="pi-checkpoint">
      <Separator class="pi-checkpoint-line" decorative />
      <Button type="button" variant="outline" size="sm" @click="emit('review')">
        <Flag :size="14" aria-hidden="true" /> 本轮成果
      </Button>
      <Separator class="pi-checkpoint-line" decorative />
    </div>
    <div v-else-if="terminal && !latest" class="pi-round-note">
      本轮没有留存成果快照。
      <Button v-if="canReviewLatest" type="button" variant="ghost" size="sm" @click="emit('reviewLatest')">查看最新成果</Button>
    </div>
    <p v-if="latest && !terminal && page" class="pi-round-note" role="status">
      <template v-if="events.statusLabel.value === '实时连接'">
        <LoadingIndicator :active="true" />
        <span class="sr-only">正在接收 {{ hostLabel }} 更新</span>
      </template>
      <template v-else>{{ events.statusLabel.value }}</template>
    </p>
  </article>
</template>

<style scoped>
.pi-round { display: grid; gap: 14px; min-width: 0; overflow-wrap: anywhere; }
.pi-round-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 5px 12px; color: var(--quiet); font-size: 11px; }
.pi-round:focus-visible { outline: none; }
.pi-round:focus-visible > .pi-round-meta time {
  text-decoration: underline 2px;
  text-underline-offset: 4px;
}
.pi-round-meta a, .pi-round-meta button { display: inline-flex; align-items: center; gap: 4px; color: var(--quiet); }
.pi-round-meta button { height: auto; min-height: 26px; padding-inline: 4px; }
.pi-round-meta a:hover, .pi-round-problem a:hover { text-decoration: underline; }
.pi-round-note { color: var(--quiet); font-size: 12px; }
.pi-inline-error { color: #9a432b; }
.pi-round-problem { display: grid; gap: 6px; padding: 10px 12px; border-inline-start: 2px solid #cdbdb8; font-size: 13px; }
.pi-round-problem a { width: fit-content; }
.pi-tool-trigger { display: flex; align-items: center; gap: 8px; min-height: 30px; padding-inline: 0 8px; color: var(--quiet); font-size: 13px; text-align: start; }
.pi-tool-trigger:hover { color: var(--foreground); }
.pi-tool-trigger[data-state='open'] .pi-tool-chevron { transform: rotate(90deg); }
.pi-tool-steps { display: grid; gap: 4px; margin: 2px 0 2px 7px; padding-inline-start: 13px; border-inline-start: 1px solid var(--border); }
.pi-tool-row { display: grid; justify-items: start; }
.pi-tool-row button { justify-self: start; color: var(--quiet); }
.pi-checkpoint { display: flex; align-items: center; gap: 9px; }
.pi-checkpoint-line { flex: 1; min-width: 0; }
.pi-checkpoint button { flex: none; color: var(--quiet); }
@media (pointer: coarse) {
  .pi-round-meta button, .pi-tool-trigger, .pi-checkpoint button { min-height: 44px; }
}
</style>
