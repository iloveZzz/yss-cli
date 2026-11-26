<script setup lang="ts">
import { computed, nextTick, shallowRef, toRef, useTemplateRef, watch } from 'vue'
import { ArrowUpRight, ChevronRight, Flag, Wrench } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Separator } from '@/components/ui/separator'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { errorMessage, dateLabel } from '@/services/workbench'
import { answerJob, jobStatusLabels, objectValue, piThinkingLabels, terminalJob, type JobActivity, type JobRecord } from '@/services/workbench-platform'
import { useJobEvents } from '@/views/Jobs/hooks/useJobEvents'
import ConversationMessage from '@/views/Jobs/components/ConversationMessage.vue'
import ConversationQuestion from '@/views/Jobs/components/ConversationQuestion.vue'
import ConversationTool from '@/views/Jobs/components/ConversationTool.vue'
import PiContextUsage from './PiContextUsage.vue'
import PiTokenUsage from './PiTokenUsage.vue'
import PiPlan from './PiPlan.vue'
import PiReasoning from './PiReasoning.vue'

const props = defineProps<{
  job: JobRecord
  latest: boolean
  direct: boolean
  readOnly?: boolean | null
  focusActivity?: { id: number }
  stopping: boolean
  canReview: boolean
  canReviewLatest: boolean
  reviewTargets?: [string, string][]
  projectNames: Record<string, string>
  trackFirstText?: boolean
}>()
const emit = defineEmits<{
  updated: []
  focus: [node: HTMLElement]
  review: [projectId?: string, path?: string]
  reviewLatest: []
}>()
const round = useTemplateRef<HTMLElement>('round')
const jobId = toRef(() => props.job.id)
const events = useJobEvents(jobId, () => { if (props.latest) emit('updated') }, () => !!props.trackFirstText)
const { items, page, hasOlder, loadingHistory, historyError, firstText } = events.conversation
const firstTextRendered = computed(() => !!firstText.value && items.value.some((row) => row.id === firstText.value?.rowId))
watch([firstText, firstTextRendered], ([text, rendered], _, onCleanup) => {
  const target = text && rendered ? round.value?.querySelector(`[data-assistant-row="${text.rowId}"]`) : undefined
  if (!text || !target) return
  const renderedTarget = target
  const { jobId: measuredJob, receivedAt } = text
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
        const mark = performance.mark('yss.pi.first-text.visible', { detail: { jobId: measuredJob } })
        performance.measure('yss.pi.first-text.receive-to-visible', {
          start: receivedAt, end: mark.startTime, detail: { jobId: measuredJob }
        })
      })
    })
  }
  const observer = new IntersectionObserver(([entry]) => {
    visible = entry?.isIntersecting === true
    recordVisible()
  })
  observer.observe(target)
  document.addEventListener('visibilitychange', recordVisible)
  onCleanup(() => {
    observer.disconnect()
    document.removeEventListener('visibilitychange', recordVisible)
    if (frame !== undefined) cancelAnimationFrame(frame)
  })
}, { flush: 'post' })
const settings = computed(() => events.latest.value?.piSettings)
const onlyReads = computed(() => settings.value?.readOnly ?? props.readOnly)
const plan = computed(() => events.latest.value?.piPlan)
const terminal = computed(() => terminalJob(props.job.status) || !!events.latest.value?.terminal)
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
function ordinary(row: JobActivity) {
  return ['tool', 'files'].includes(row.kind)
    && row.status !== 'inProgress' && row.status !== 'failed' && row.status !== 'canceled'
    && !row.error && (row.exitCode ?? 0) === 0
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
      <span>{{ stopping && !terminal ? '正在停止' : jobStatusLabels[job.status] || job.status }}</span>
      <span v-if="page?.allowCommands">已授权离线命令</span>
      <span v-if="onlyReads">{{ direct ? '只读原工程' : '只读分析' }}</span>
      <span v-else-if="direct && onlyReads === false">编辑原工程</span>
      <span v-if="settings" :title="settings.model.provider + ' / ' + settings.model.id">
        {{ settings.model.id }}<template v-if="settings.thinkingLevel && settings.thinkingLevel !== 'off'"> · {{ piThinkingLabels[settings.thinkingLevel] }}推理</template>
      </span>
      <PiContextUsage :usage="events.latest.value?.piContext" :limit="settings?.contextWindow" />
      <RouterLink :to="{ path: '/jobs/' + job.id, query: { detail: '1', tab: 'log' } }">本轮日志 <ArrowUpRight :size="12" /></RouterLink>
    </header>
    <PiTokenUsage v-if="events.latest.value?.piUsage" :usage="events.latest.value.piUsage" />
    <p v-if="direct && onlyReads === false &&
      (stopping || (terminalJob(job.status) && !['completed', 'succeeded'].includes(job.status)))" class="pi-round-note">
      已写入的内容会保留在原工程。
    </p>
    <ConversationMessage v-if="page?.prompt" :text="page.prompt" user />
    <p v-else-if="page || terminal" class="pi-round-note">本轮未留存原始要求</p>
    <details v-for="(excerpt, index) in page?.citations" :key="index" class="pi-sent-excerpt">
      <summary>{{ projectNames[excerpt.projectId] ?? excerpt.projectId }} / {{ excerpt.path }} · {{ excerpt.side === 'initial' ? '运行起点' : direct ? '本轮结束' : '隔离结果' }} {{ excerpt.startLine }}–{{ excerpt.endLine }} 行</summary>
      <p>{{ excerpt.note }}</p>
      <pre>{{ excerpt.snippet }}</pre>
      <small>来源轮次 {{ excerpt.jobId }} · 版本 {{ excerpt.reviewToken.slice(0, 10) }}</small>
    </details>
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
    <LoadingSkeleton v-if="!page && !terminal && !events.error.value" :rows="2" variant="text" label="正在读取 Pi 对话" />
    <p v-else-if="page && !items.length && !terminal" class="pi-round-note">等待 Pi 开始答复…</p>
    <PiPlan v-if="plan?.length" :steps="plan" :terminal="terminal" />
    <template v-for="entry in entries" :key="entry.key">
      <Collapsible v-if="entry.group" :open="expanded(entry)" class="pi-tool-group" @update:open="toggle(entry, $event)">
        <CollapsibleTrigger class="pi-tool-trigger">
          <Wrench :size="15" /> <span>{{ activitySummary(entry.rows) }}</span>
          <ChevronRight :size="14" class="pi-tool-chevron" />
        </CollapsibleTrigger>
        <CollapsibleContent class="pi-tool-steps">
          <div v-for="row in entry.rows" :key="row.id" class="pi-tool-row">
            <ConversationTool :job-id="jobId" :row="row" :terminal="terminal" />
            <Button v-if="terminal && canReview && hasFileDiff(row)" type="button" variant="ghost" size="sm"
              @click="emit('review', row.projectId, row.path)">查看文件差异</Button>
          </div>
        </CollapsibleContent>
      </Collapsible>
      <template v-else>
        <template v-for="row in entry.rows" :key="row.id">
          <ConversationMessage v-if="row.kind === 'message' || row.kind === 'user'" :text="row.text" :created-at="row.createdAt"
            :row-id="row.id" :user="row.kind === 'user'" :streaming="row.kind === 'message' && !row.status && !terminal" />
          <PiReasoning v-else-if="row.kind === 'reasoning'" :row="row" :terminal="terminal" />
          <ConversationQuestion v-else-if="row.question" :row="row" :question="row.question" :terminal="terminal"
            :busy="!!answering || !latest || stopping || !!inputSync" :submitting="row.question.requestId === answering" :submit="submit" />
          <p v-else-if="row.kind === 'error'" class="pi-inline-error preserve-lines">{{ row.text }}</p>
          <ConversationTool v-else :job-id="jobId" :row="row" :terminal="terminal" />
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
        <span class="sr-only">正在接收 Pi 更新</span>
      </template>
      <template v-else>{{ events.statusLabel.value }}</template>
    </p>
  </article>
</template>

<style scoped>
.pi-round { display: grid; gap: 14px; min-width: 0; overflow-wrap: anywhere; }
.pi-round-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 5px 12px; color: var(--quiet); font-size: 11px; }
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
.pi-sent-excerpt { width: min(100%, 560px); justify-self: end; border-inline-start: 2px solid var(--border); padding-inline: 12px; font-size: 12px; color: var(--quiet); }
.pi-sent-excerpt summary { cursor: pointer; }
.pi-sent-excerpt pre { white-space: pre-wrap; }
.pi-checkpoint { display: flex; align-items: center; gap: 9px; }
.pi-checkpoint-line { flex: 1; min-width: 0; }
.pi-checkpoint button { flex: none; color: var(--quiet); }
@media (pointer: coarse) {
  .pi-round-meta button, .pi-tool-trigger, .pi-checkpoint button { min-height: 44px; }
}
</style>
