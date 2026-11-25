<script setup lang="ts">
import { computed, nextTick, onScopeDispose, reactive, shallowRef, useId, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMediaQuery } from '@vueuse/core'
import {
  ArrowLeft, ArrowUp, ChevronDown, FileDiff, FileText, Folder, ListChecks, PanelLeft,
  Settings2, Square
} from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { InputGroup, InputGroupAddon, InputGroupTextarea } from '@/components/ui/input-group'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import {
  MessageScroller, MessageScrollerButton, MessageScrollerContent, MessageScrollerItem,
  MessageScrollerProvider, MessageScrollerViewport
} from '@/components/ui/message-scroller'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import PiModelPicker from '@/components/PiModelPicker.vue'
import ControlTooltip from '@/components/ControlTooltip.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { ApiError, errorMessage, stages, type Stage } from '@/services/workbench'
import {
  deleteAgentSession, getPiRuntime, piModelKey, startAgent, type AgentAttention, type AgentSummary, type ReviewExcerpt
} from '@/services/workbench-agent'
import { cancelJob, jobStatusLabels, piThinkingLabels, terminalJob,
  type PiModel, type PiRuntimeStatus, type PiThinkingLevel } from '@/services/workbench-platform'
import PiSessionList from './components/PiSessionList.vue'
import PiWorkspaceSettings from './components/PiWorkspaceSettings.vue'
import PiRound from './components/PiRound.vue'
import PiReview from './components/PiReview.vue'
import { useAgentScope } from './useAgentScope'
import { usePiWorkspace } from './usePiWorkspace'
import './workspace.css'

const props = defineProps<{ specId: string }>()
const route = useRoute()
const router = useRouter()
const { snapshot } = useWorkbench()
const { scopeOptions, lastAgentSpec } = useAgentScope()
const fromAgentWorkbench = computed(() => route.name === 'agent-workbench')
const backLabel = computed(() => fromAgentWorkbench.value ? '返回工作台' : '返回原 Spec')
const spec = computed(() => props.specId)
const session = computed(() => typeof route.query.session === 'string' ? route.query.session : '')
const linkedRound = computed(() => typeof route.query.round === 'string' ? route.query.round : '')
const { context, contextError, run, runError, launchError, rounds, latest, summary, snapshotJobs, reading,
  refresh, recordAccepted, forgetSession, scheduleRefresh } = usePiWorkspace(spec, session)
const projectNames = computed(() => Object.fromEntries(context.value?.projects.map((project) => [project.id, project.name]) ?? []))
const mainId = computed(() => context.value?.spec.projectId ?? '')
const title = computed(() => summary.value?.title || run.value?.prompt || (session.value ? 'Pi 会话' : '新会话'))
const progression = computed(() => snapshot.value?.progress?.[spec.value])
const stage = computed(() => {
  const desired = typeof route.query.stage === 'string' && Object.hasOwn(stages, route.query.stage)
    ? route.query.stage as Stage
    : progression.value?.primaryStage
  return progression.value?.goalVersion && progression.value.stages.some((entry) => entry.stage === desired && entry.applicable)
    ? desired : undefined
})
const active = computed(() => !!latest.value && !terminalJob(latest.value.status))
const status = computed(() => {
  if (latest.value && !terminalJob(latest.value.status) && stoppingFor.value === latest.value.id) return '正在停止'
  return latest.value ? jobStatusLabels[latest.value.status] ?? latest.value.status : ''
})
const historyOpen = shallowRef(true)
const historyDrawer = shallowRef(false)
const historyPaneId = useId()
const settingsOpen = shallowRef(false)
const summaryOpen = shallowRef(true)
const summaryDrawer = shallowRef(false)
const reviewOpen = shallowRef(false)
const reviewState = shallowRef({ busy: false, pending: false })
const reviewJob = shallowRef('')
const reviewFile = shallowRef<{ projectId: string; path: string }>()
const deleteTarget = shallowRef<{ specId: string; session: AgentSummary }>()
const deleting = shallowRef(false)
const deleteError = shallowRef('')
let deleteTrigger: HTMLElement | undefined
const focusActivity = shallowRef<{ jobId: string; id: number }>()
const compact = useMediaQuery('(width < 960px)')
const inlineSummary = useMediaQuery('(width >= 1280px)')
const conversation = useTemplateRef<HTMLElement>('conversation')
const drafts = reactive(new Map<string, {
  prompt: string; citations: ReviewExcerpt[]; selected: string[]; carry: Record<string, string>; allowCommands: boolean;
  selectedModel?: PiModel; thinkingLevel?: PiThinkingLevel; readOnly?: boolean; error: string
}>())
const draftKey = computed(() => spec.value + ':' + (session.value || 'new'))
function getDraft(key: string) {
  if (!drafts.has(key)) drafts.set(key, { prompt: '', citations: [], selected: [], carry: {}, allowCommands: false, error: '' })
  return drafts.get(key)!
}
const draft = computed(() => getDraft(draftKey.value))
const readyModel = shallowRef<PiModel>()
const runtimeStatus = shallowRef<PiRuntimeStatus>()
const selectedModel = computed({
  get: () => draft.value.selectedModel,
  set: (model: PiModel | undefined) => {
    const previous = draft.value.selectedModel
    if (previous?.provider === model?.provider && previous?.id === model?.id) return
    readyModel.value = undefined
    runtimeStatus.value = undefined
    modelIssue.value = ''
    draft.value.selectedModel = model
    draft.value.thinkingLevel = undefined
  }
})
const modelIssue = shallowRef('')
const modelRestrictions = shallowRef<Record<string, string>>({})
const checking = shallowRef(false)
const sending = shallowRef(false)
const submittedRound = shallowRef<string>()
const stopBusy = shallowRef(false)
const stoppingFor = shallowRef('')
const showing = shallowRef(3)
const readyForResume = computed(() => !runError.value && run.value?.canResume && run.value.jobId === latest.value?.id)
const modelPickerDisabled = computed(() => !context.value || active.value || sending.value || (!!session.value && !readyForResume.value))
const recordedModel = computed<PiModel | undefined>(() => {
  const settings = run.value?.hostSettings
  return typeof settings?.provider === 'string' && typeof settings.model === 'string'
    ? { provider: settings.provider, id: settings.model } : undefined
})
const currentModel = computed(() => selectedModel.value ?? readyModel.value ?? recordedModel.value)
const thinkingLevels = computed(() => runtimeStatus.value?.available ? runtimeStatus.value.thinkingLevels ?? [] : [])
const currentThinking = computed(() => {
  const remembered = run.value?.hostSettings?.thinkingLevel as PiThinkingLevel | undefined
  const model = currentModel.value
  const previous = recordedModel.value
  if (draft.value.thinkingLevel && thinkingLevels.value.includes(draft.value.thinkingLevel)) return draft.value.thinkingLevel
  if (remembered && previous && model && piModelKey(previous) === piModelKey(model) && thinkingLevels.value.includes(remembered)) return remembered
  return runtimeStatus.value?.thinkingLevel ?? undefined
})
const modelLabel = computed(() => {
  const model = currentModel.value
  const level = currentThinking.value
  const thinking = level && thinkingLevels.value.length > 1 ? ' · ' + piThinkingLabels[level] : ''
  return model ? model.provider + ' / ' + model.id + thinking : 'Pi 模型'
})
const blocked = computed(() => {
  if (!context.value) return contextError.value || '正在读取 Spec'
  if (!context.value.spec.active) return '此 Spec 已归档'
  if (context.value.sourceError) return context.value.sourceError
  if (!context.value.projects.find((project) => project.id === mainId.value)?.active) return '主工程已停用，请检查工程登记'
  if (active.value) return ''
  if (session.value && !readyForResume.value) return run.value?.resumeIssue || runError.value ||
    (reading.value ? '正在核对原会话' : '原会话或隔离工作区不可用，可从原 Spec 新建会话')
  return ''
})
const canSend = computed(() => !active.value && !blocked.value && !contextError.value && !sending.value &&
  !checking.value && !settingsOpen.value && !deleteTarget.value &&
  !reviewState.value.busy && !reviewState.value.pending && !!readyModel.value && !!draft.value.prompt.trim())
const visibleRounds = computed(() => rounds.value.slice(-showing.value))
const summaryActions = computed(() => summary.value?.attention.filter((item) => item.kind !== 'review') ?? [])
const attentionLabels: Record<AgentAttention['kind'], string> = {
  recovery: '需恢复', question: '待答复', answerFailed: '答复保存失败',
  interrupted: '中断待核对', failed: '运行失败', review: '待审阅'
}
let alive = true
const restore = new Map<string, HTMLElement>()
let moveToComposer = false

function rememberFocus(key: string, target: EventTarget | null = document.activeElement) {
  if (target instanceof HTMLElement) restore.set(key, target)
}
function returnFocus(key: string) {
  const previous = restore.get(key)
  restore.delete(key)
  // The drawer may have moved focus from a trigger that was just removed by navigation.
  if (previous?.isConnected) previous.focus({ preventScroll: true })
  else conversation.value?.querySelector<HTMLElement>('[data-slot="message-scroller-viewport"]')?.focus({ preventScroll: true })
}
function openSettings() {
  rememberFocus('settings')
  settingsOpen.value = true
}
function toggleHistory(event: MouseEvent) {
  if (compact.value) {
    rememberFocus('history', event.currentTarget)
    historyDrawer.value = true
  } else historyOpen.value = !historyOpen.value
}
function toggleSummary() {
  if (inlineSummary.value) summaryOpen.value = !summaryOpen.value
  else {
    rememberFocus('summary')
    summaryDrawer.value = true
  }
}
function summaryClosed() {
  if (!reviewOpen.value) returnFocus('summary')
}
function openReview(job = latest.value?.id, projectId?: string, path?: string) {
  if (!job || !session.value) return
  if (!reviewOpen.value) rememberFocus('review')
  reviewFile.value = projectId && path ? { projectId, path } : undefined
  reviewJob.value = job
  summaryDrawer.value = false
  reviewOpen.value = true
}
function focusDraft() {
  document.getElementById('pi-workspace-draft')?.focus({ preventScroll: true })
}
function reviewClosed() {
  if (moveToComposer) {
    moveToComposer = false
    focusDraft()
  } else returnFocus('review')
}
function addCitation(excerpt: ReviewExcerpt) {
  if (draft.value.citations.length >= 12) {
    draft.value.error = '下一轮最多引用 12 段差异，请先移除一段。'
    return
  }
  draft.value.citations.push(excerpt)
  moveToComposer = true
  reviewOpen.value = false
}
function sessionLink(id = '', round?: string) {
  const name = fromAgentWorkbench.value ? 'agent-workbench' : 'pi-workspace'
  return { name, params: { specId: spec.value }, query: id
    ? { session: id, round, stage: route.query.stage }
    : { new: '1', stage: route.query.stage } }
}
function selectAgentScope(specId: string) {
  if (specId === spec.value || !scopeOptions.value.some((option) => option.value === specId)) return
  historyDrawer.value = false
  void router.push({ name: 'agent-workbench', params: { specId } })
}
async function newSession() {
  if (summaryDrawer.value) summaryDrawer.value = false
  historyDrawer.value = false
  await router.push(sessionLink())
  await nextTick()
  focusDraft()
}
async function choose(item: AgentSummary, attention?: AgentAttention) {
  const mobile = historyDrawer.value
  historyDrawer.value = false
  await router.push(sessionLink(item.id, attention ? item.jobId : undefined))
  if (attention) {
    await nextTick()
    jump(attention, item)
  } else if (mobile) {
    await nextTick()
    focusTranscript()
  }
}
function askDelete(item: AgentSummary, trigger: EventTarget | null) {
  if (item.deleteIssue || deleting.value) return
  deleteTrigger = trigger instanceof HTMLElement ? trigger : undefined
  deleteError.value = ''
  deleteTarget.value = { specId: spec.value, session: item }
}
function closeDelete() {
  if (!deleting.value) deleteTarget.value = undefined
}
async function restoreDeleteFocus(event: Event) {
  event.preventDefault()
  const trigger = deleteTrigger
  deleteTrigger = undefined
  await nextTick()
  if (deleteTarget.value) return
  if (trigger?.isConnected && trigger.getClientRects().length) {
    trigger.focus({ preventScroll: true })
    return
  }
  const list = historyDrawer.value ? '.pi-history-sheet' : '.pi-history-pane'
  const fallback = document.querySelector<HTMLElement>(list + ' .pi-history-new')
  if (fallback?.getClientRects().length) fallback.focus({ preventScroll: true })
  else focusDraft()
}
async function confirmDelete() {
  const target = deleteTarget.value
  if (!target || deleting.value) return
  deleting.value = true
  deleteError.value = ''
  try {
    await deleteAgentSession(target.session.id, target.session.jobId)
    if (!alive) return
    forgetSession(target.specId, target.session.id)
    drafts.delete(`${target.specId}:${target.session.id}`)
    if (spec.value === target.specId && session.value === target.session.id) {
      await router.replace(sessionLink(context.value?.sessions[0]?.id))
    }
    deleteTarget.value = undefined
  } catch (cause) {
    if (alive) deleteError.value = errorMessage(cause)
  } finally {
    if (alive) {
      deleting.value = false
      scheduleRefresh()
    }
  }
}
function jump(item: AgentAttention, target = summary.value) {
  if (!target) return
  summaryDrawer.value = false
  if (item.kind === 'review' || (item.kind === 'recovery' && target.hasAcceptance)) {
    openReview(target.jobId)
  } else {
    focusActivity.value = { jobId: target.jobId, id: item.activityId ?? 0 }
  }
}
function focusTranscript() {
  const viewport = conversation.value?.querySelector<HTMLElement>('[data-slot="message-scroller-viewport"]')
  viewport?.focus({ preventScroll: true })
}
function focusNode(node: HTMLElement) {
  node.focus({ preventScroll: true })
  node.scrollIntoView({ block: 'nearest' })
}
function backToLatest() {
  void router.replace(sessionLink(session.value))
  const viewport = conversation.value?.querySelector<HTMLElement>('[data-slot="message-scroller-viewport"]')
  viewport?.scrollTo({ top: viewport.scrollHeight, behavior: 'instant' })
  focusTranscript()
}
watch(compact, (isCompact) => {
  if (!isCompact) historyDrawer.value = false
})
watch([context, session], ([workspace, id]) => {
  if (!workspace || workspace.spec.id !== spec.value) return
  if (workspace.spec.active && !workspace.sourceError) lastAgentSpec.value = spec.value
  if (id || route.query.new) return
  const first = workspace.sessions[0]
  if (first) void router.replace(sessionLink(first.id))
}, { immediate: true })
watch([spec, session], () => {
  showing.value = 3
  readyModel.value = undefined
  runtimeStatus.value = undefined
  modelIssue.value = ''
  settingsOpen.value = false
  reviewOpen.value = false
  focusActivity.value = undefined
  draft.value.allowCommands = false
})
let requestedRound = ''
watch([rounds, linkedRound], async ([items, id]) => {
  if (!id) { requestedRound = ''; return }
  if (id === requestedRound) return
  const index = items.findIndex((job) => job.id === id)
  if (index < 0) return
  requestedRound = id
  showing.value = Math.max(showing.value, items.length - index)
  await nextTick()
  const node = document.getElementById('pi-turn-' + id)?.querySelector<HTMLElement>('article')
  if (node && !focusActivity.value) focusNode(node)
}, { flush: 'post' })
watch([() => context.value?.spec.id, session, readyForResume, selectedModel, settingsOpen], ([source, id, resumable, selected, open], previous, onCleanup) => {
  checking.value = false
  if (!source || !context.value?.spec.active || context.value.sourceError || (id && !resumable) || open) return
  if (previous?.[4] && (readyModel.value || modelIssue.value)) return
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  checking.value = true
  readyModel.value = undefined
  runtimeStatus.value = undefined
  modelIssue.value = ''
  void getPiRuntime({ resumeRunId: id || undefined, selectedModel: selected }, controller.signal).then((result) => {
    if (controller.signal.aborted) return
    runtimeStatus.value = result
    const model = result.available ? result.model ?? undefined : undefined
    modelIssue.value = model ? modelRestrictions.value[piModelKey(model)] ?? '' : result.message || 'Pi 模型未就绪'
    readyModel.value = modelIssue.value ? undefined : model
  }).catch((cause) => {
    if (!controller.signal.aborted) modelIssue.value = errorMessage(cause)
  }).finally(() => {
    if (!controller.signal.aborted) checking.value = false
  })
}, { immediate: true })
watch(latest, (job) => {
  if (!job || terminalJob(job.status)) stoppingFor.value = ''
})
onScopeDispose(() => { alive = false })

async function send() {
  if (!canSend.value || !readyModel.value) return
  const current = draft.value
  const key = draftKey.value
  const id = session.value
  const source = spec.value
  const projectIds = id ? run.value?.projectIds ?? [] : [mainId.value, ...current.selected]
  const carryFiles = id ? {} : Object.fromEntries(projectIds.flatMap((project) => {
    const paths = (current.carry[project] ?? '').split('\n').map((name) => name.trim()).filter(Boolean)
    return paths.length ? [[project, paths]] : []
  }))
  const expected = latest.value?.id
  sending.value = true
  current.error = ''
  try {
    const job = await startAgent({
      specId: source, projectIds, carryFiles, resumeRunId: id || undefined, expectedJobId: id ? expected : undefined,
      selectedModel: current.selectedModel, expectedModel: readyModel.value,
      thinkingLevel: current.thinkingLevel,
      readOnly: current.readOnly,
      allowCommands: current.allowCommands, prompt: current.prompt, lifecycleStage: id ? undefined : stage.value,
      citations: current.citations.map(({ reviewToken, projectId, path, side, startLine, endLine, note }) =>
        ({ reviewToken, projectId, path, side, startLine, endLine, note }))
    })
    if (!alive) return
    const next = id || job.id
    current.prompt = ''
    current.citations = []
    current.allowCommands = false
    recordAccepted(source, next, job)
    if (draftKey.value === key) {
      submittedRound.value = job.id
      await router.replace(sessionLink(next))
      await nextTick()
      focusTranscript()
    }
  } catch (cause) {
    if (!alive) return
    current.error = errorMessage(cause)
    if (cause instanceof ApiError && cause.context.piIssue && draftKey.value === key) {
      readyModel.value = undefined
      openSettings()
    }
    scheduleRefresh()
  } finally {
    if (alive) sending.value = false
  }
}

async function stop() {
  const job = latest.value
  if (!job || !active.value || stopBusy.value || stoppingFor.value === job.id) return
  const current = draft.value
  stopBusy.value = true
  try {
    await cancelJob(job.id)
    if (alive) stoppingFor.value = job.id
  } catch (cause) {
    if (alive) current.error = errorMessage(cause)
  } finally {
    if (alive) {
      stopBusy.value = false
      scheduleRefresh()
    }
  }
}
function onEnter(event: KeyboardEvent) {
  if (event.key !== 'Enter' || event.isComposing || event.keyCode === 229 || event.shiftKey ||
    event.ctrlKey || event.altKey || event.metaKey || !canSend.value) return
  event.preventDefault()
  void send()
}
</script>

<template>
  <div class="pi-workspace" :data-history="historyOpen" :data-summary="summaryOpen && inlineSummary">
    <Transition name="workbench-feedback">
      <aside v-show="historyOpen" :id="historyPaneId" class="pi-history-pane" :inert="compact || !historyOpen">
        <PiSessionList v-if="context" :context="context" :active-session="session" :error="contextError"
          :scope-options="fromAgentWorkbench ? scopeOptions : undefined"
          @new="newSession" @choose="choose" @refresh="refresh" @remove="askDelete" @select-scope="selectAgentScope" />
        <LoadingSkeleton v-else-if="!contextError" variant="text" :rows="4" label="正在读取会话列表" />
      </aside>
    </Transition>
    <div class="pi-stage">
      <header class="pi-bar">
        <Button variant="ghost" size="icon" as-child>
          <RouterLink :to="fromAgentWorkbench ? '/workbench' : '/specs/' + spec" :aria-label="backLabel" :title="backLabel">
            <ArrowLeft :size="17" aria-hidden="true" />
          </RouterLink>
        </Button>
        <ControlTooltip label="会话历史">
          <Button type="button" variant="ghost" size="icon" aria-label="会话历史"
            :aria-expanded="compact ? historyDrawer : historyOpen" :aria-controls="compact ? undefined : historyPaneId"
            :aria-haspopup="compact ? 'dialog' : undefined" @click="toggleHistory">
            <PanelLeft :size="18" aria-hidden="true" />
          </Button>
        </ControlTooltip>
        <Folder :size="16" class="pi-heading-icon" />
        <h1 :title="title">{{ title }}</h1>
        <ControlTooltip label="会话摘要">
          <Button type="button" variant="ghost" size="icon" aria-label="会话摘要" class="pi-summary-toggle"
            :aria-expanded="inlineSummary ? summaryOpen : summaryDrawer" @click="toggleSummary">
            <ListChecks :size="18" aria-hidden="true" />
          </Button>
        </ControlTooltip>
      </header>
      <div v-if="contextError || (context && blocked && !active)" class="pi-top-note" role="status">
        {{ contextError || blocked }}
        <RouterLink v-if="run?.recoveryJobId && run.recoveryRunId !== session" :to="'/jobs/' + run.recoveryJobId">
          查看待核实的接收轮次
        </RouterLink>
        <Button v-else-if="run?.recoveryJobId && summary?.hasAcceptance" type="button" variant="ghost" size="sm" @click="openReview()">
          查看待核实的接收记录
        </Button>
        <Button v-if="contextError || (session && blocked)" type="button" variant="ghost" size="sm" @click="refresh">刷新</Button>
        <Button v-if="session && context && !context.sourceError && context.spec.active" type="button" variant="ghost" size="sm" @click="newSession">新会话</Button>
      </div>
      <div v-if="launchError" class="pi-top-note" role="status">{{ launchError }}</div>
      <div v-if="reviewState.pending && !reviewState.busy && !reviewOpen && !active && !blocked" class="pi-top-note" role="status">
        接收结果待核实。
        <Button type="button" variant="ghost" size="sm" @click="openReview()">查看待核实的接收记录</Button>
      </div>
      <div v-if="linkedRound && !reading && rounds.length && !rounds.some((job) => job.id === linkedRound)" class="pi-top-note" role="status">
        此会话没有留存该轮次。
        <Button type="button" variant="ghost" size="sm" @click="backToLatest">返回最新轮</Button>
      </div>
      <div v-else-if="linkedRound && linkedRound !== latest?.id" class="pi-top-note">
        正在查看旧轮
        <Button type="button" variant="ghost" size="sm" @click="backToLatest">返回最新轮</Button>
      </div>
      <div ref="conversation" class="pi-conversation">
        <MessageScrollerProvider :key="draftKey" auto-scroll default-scroll-position="end" :scroll-edge-threshold="24">
          <MessageScroller class="pi-scroller">
            <MessageScrollerViewport aria-label="Pi 会话对话" class="pi-viewport" :preserve-scroll-on-prepend="true">
              <MessageScrollerContent class="pi-transcript">
                <div v-if="reading && !visibleRounds.length"><LoadingSkeleton :rows="3" variant="text" label="正在读取会话轮次" /></div>
                <div v-if="!session && context" class="pi-new-session">
                  <h2>{{ context.spec.title }}</h2>
                  <p>{{ projectNames[mainId] || '主工程' }} · {{ context.spec.sourcePath }}</p>
                  <Button v-if="context.spec.active && !context.sourceError" variant="outline" size="sm" @click="openSettings">
                    <Settings2 :size="14" /> 查看运行范围
                  </Button>
                </div>
                <p v-if="runError && !visibleRounds.length" class="pi-issue" role="alert">{{ runError }}</p>
                <Button v-if="rounds.length > showing" type="button" variant="ghost" size="sm" class="pi-more-rounds" @click="showing += 3">
                  加载更早轮次
                </Button>
                <MessageScrollerItem v-for="job in visibleRounds" :id="'pi-turn-' + job.id" :key="job.id"
                  :message-id="job.id" :scroll-anchor="job.id === submittedRound && !terminalJob(job.status)"
                  :data-workbench-reveal="job.id === submittedRound ? '' : undefined" class="pi-turn">
                  <PiRound :job="job" :latest="job.id === latest?.id" :stopping="job.id === stoppingFor"
                    :focus-activity="focusActivity?.jobId === job.id ? focusActivity : undefined"
                    :can-review="snapshotJobs.has(job.id)" :can-review-latest="!!latest && snapshotJobs.has(latest.id)"
                    :project-names="projectNames"
                    :review-targets="job.id === summary?.jobId ? summary.reviewTargets : undefined"
                    @updated="scheduleRefresh" @focus="focusNode" @review="(project, path) => openReview(job.id, project, path)"
                    @review-latest="openReview()" />
                </MessageScrollerItem>
              </MessageScrollerContent>
            </MessageScrollerViewport>
            <MessageScrollerButton size="sm" variant="outline" behavior="auto">回到最新</MessageScrollerButton>
          </MessageScroller>
        </MessageScrollerProvider>
      </div>
      <div class="pi-composer-wrap">
        <Button v-if="summary?.hasSnapshot || summary?.hasAcceptance" type="button" variant="outline"
          class="pi-diff-shortcut" @click="openReview()"><FileDiff :size="15" />
          {{ summary.pendingFiles ? summary.pendingFiles + ' 个文件待审阅' : '查看执行成果' }}
        </Button>
        <form aria-label="发送 Pi 任务" @submit.prevent="send">
          <InputGroup class="pi-composer">
            <div v-if="draft.citations.length" class="pi-draft-excerpts">
              <div v-for="(excerpt, index) in draft.citations" :key="index" class="pi-draft-excerpt">
                <details>
                  <summary>{{ projectNames[excerpt.projectId] ?? excerpt.projectId }} / {{ excerpt.path }} · {{ excerpt.startLine }}–{{ excerpt.endLine }} 行</summary>
                  <p>{{ excerpt.side === 'initial' ? '运行起点' : '隔离结果' }} · 轮次 {{ excerpt.jobId }} · 版本 {{ excerpt.reviewToken.slice(0, 10) }}</p>
                  <pre>{{ excerpt.snippet }}</pre>
                  <p>{{ excerpt.note }}</p>
                </details>
                <Button type="button" variant="ghost" size="sm" @click="draft.citations.splice(index, 1)">移除引用</Button>
              </div>
            </div>
            <InputGroupTextarea id="pi-workspace-draft" v-model="draft.prompt" aria-label="Pi 任务"
              :placeholder="active ? '准备下一轮要求' : '给 Pi 发一项任务'" rows="2" :disabled="sending" @keydown="onEnter" />
            <InputGroupAddon align="block-end" class="pi-composer-tools">
              <Button type="button" variant="ghost" size="sm" :disabled="!context" @click="openSettings">
                <Settings2 :size="15" /><span>{{ draft.readOnly ? '只读模式' : draft.allowCommands ? '本轮允许离线命令' : '离线命令关闭' }}</span>
              </Button>
              <PiModelPicker :selected-model="selectedModel" :current-model="currentModel"
                show-thinking :thinking-levels="thinkingLevels" :thinking-level="currentThinking" :checking-thinking="checking"
                :default-label="session ? '沿用会话模型' : undefined"
                :disabled="modelPickerDisabled" @select="selectedModel = $event" @thinking="draft.thinkingLevel = $event">
                <template #trigger>
                  <Button type="button" variant="ghost" size="sm" class="pi-model-button" aria-label="切换 Pi 模型" :title="modelLabel"
                    :disabled="modelPickerDisabled">
                    <LoadingIndicator :active="checking" /><span class="pi-model-label">{{ modelLabel }}</span><ChevronDown :size="13" aria-hidden="true" />
                  </Button>
                </template>
              </PiModelPicker>
              <Button v-if="active" type="button" size="icon" class="pi-send" aria-label="停止运行" title="停止运行"
                :disabled="stopBusy || !!stoppingFor" @click="stop">
                <LoadingIndicator v-if="stopBusy" :active="true" /><Square v-else :size="14" fill="currentColor" />
              </Button>
              <Button v-else type="submit" size="icon" class="pi-send" aria-label="发送" title="发送" :disabled="!canSend">
                <LoadingIndicator :active="sending" /><ArrowUp v-if="!sending" :size="19" />
              </Button>
            </InputGroupAddon>
          </InputGroup>
        </form>
        <p v-if="draft.error || (!active && modelIssue && !readyModel)" class="pi-composer-error" role="status">{{ draft.error || modelIssue }}</p>
      </div>
      <Transition name="workbench-feedback">
      <aside v-if="inlineSummary" v-show="summaryOpen" :inert="!summaryOpen" class="pi-summary" aria-label="会话摘要">
        <section>
          <h2>会话摘要</h2>
          <p v-if="status">{{ status }}</p>
          <p v-if="summary?.reviewError" class="pi-issue">{{ summary.reviewError }}</p>
          <Button v-if="latest && (summary?.hasSnapshot || summary?.hasAcceptance)" type="button" variant="ghost" class="pi-summary-action"
            @click="openReview()"><FileDiff :size="15" /> {{ summary.pendingFiles ? summary.pendingFiles + ' 个文件待审阅' : '执行成果' }}</Button>
          <Button v-for="(item, index) in summaryActions" :key="index" type="button" variant="ghost" class="pi-summary-action"
            @click="jump(item)">{{ item.status === 'saving' ? '答复保存中' : attentionLabels[item.kind] }}</Button>
        </section>
        <section>
          <h2>来源</h2>
          <RouterLink :to="{ path: '/specs/' + spec }" class="pi-summary-link"><FileText :size="15" />{{ context?.spec.title || '原 Spec' }}</RouterLink>
        </section>
      </aside>
      </Transition>
    </div>

    <Sheet v-model:open="historyDrawer">
      <SheetContent side="left" class="pi-sheet pi-history-sheet" @close-auto-focus.prevent="returnFocus('history')">
        <SheetHeader class="pi-sheet-header sr-only"><SheetTitle>会话历史</SheetTitle><SheetDescription>当前 Spec 的 Pi 会话</SheetDescription></SheetHeader>
        <PiSessionList v-if="context" :context="context" :active-session="session" :error="contextError"
          :scope-options="fromAgentWorkbench ? scopeOptions : undefined"
          @new="newSession" @choose="choose" @refresh="refresh" @remove="askDelete" @select-scope="selectAgentScope" />
      </SheetContent>
    </Sheet>
    <Dialog :open="!!deleteTarget" @update:open="!$event && closeDelete()">
      <DialogContent class="workbench-dialog pi-delete-dialog" :show-close-button="false"
        @interact-outside.prevent @escape-key-down="(event) => { if (deleting) event.preventDefault() }"
        @close-auto-focus="restoreDeleteFocus">
        <DialogHeader>
          <DialogTitle>删除 Pi 会话？</DialogTitle>
          <DialogDescription>
            将移除「{{ deleteTarget?.session.title }}」的工作台对话、执行记录和审阅入口。工程文件及 Pi 本机会话文件会保留。
          </DialogDescription>
        </DialogHeader>
        <p v-if="deleteTarget?.session.pendingFiles" class="pi-delete-warning">
          此会话还有 {{ deleteTarget.session.pendingFiles }} 个文件待审阅。
        </p>
        <p v-if="deleteError" class="pi-delete-warning" role="alert">{{ deleteError }}</p>
        <DialogFooter>
          <Button type="button" variant="outline" autofocus :disabled="deleting" @click="closeDelete">取消</Button>
          <Button type="button" variant="destructive" :disabled="deleting" @click="confirmDelete">
            <LoadingIndicator :active="deleting" />{{ deleting ? '正在删除…' : '删除会话' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
    <Sheet v-model:open="settingsOpen">
      <SheetContent class="pi-sheet" @close-auto-focus.prevent="returnFocus('settings')">
        <SheetHeader class="pi-sheet-header"><SheetTitle>运行设置</SheetTitle>
          <SheetDescription>{{ context?.spec.title }}<template v-if="stage && !session"> · {{ stages[stage] }}</template></SheetDescription></SheetHeader>
        <div v-if="context" class="pi-sheet-body">
          <PiWorkspaceSettings :key="session || 'new'" v-model:ready-model="readyModel" v-model:selected-model="selectedModel"
            v-model:runtime-status="runtimeStatus"
            v-model:blocked-reason="modelIssue" v-model:model-restrictions="modelRestrictions"
            v-model:selected="draft.selected" v-model:carry="draft.carry" v-model:allow-commands="draft.allowCommands"
            v-model:read-only="draft.readOnly"
            :main-id="mainId" :projects="context.projects" :source-path="context.spec.sourcePath"
            :review-resume-id="session || undefined" :busy="sending"
            :participating-ids="run?.projectIds" />
        </div>
      </SheetContent>
    </Sheet>
    <Sheet v-model:open="summaryDrawer">
      <SheetContent class="pi-sheet" @close-auto-focus.prevent="summaryClosed">
        <SheetHeader class="pi-sheet-header"><SheetTitle>会话摘要</SheetTitle><SheetDescription>{{ title }}</SheetDescription></SheetHeader>
        <div class="pi-sheet-body pi-mobile-summary">
          <p v-if="status">{{ status }}</p>
          <RouterLink :to="{ path: '/specs/' + spec }" class="pi-summary-link"><FileText :size="15" />{{ context?.spec.title || '原 Spec' }}</RouterLink>
          <Button v-if="latest && (summary?.hasSnapshot || summary?.hasAcceptance)" type="button" variant="outline"
            @click="openReview()"><FileDiff :size="15" />执行成果</Button>
          <Button v-for="(item, index) in summaryActions" :key="index" type="button" variant="ghost" @click="jump(item)">
            {{ item.status === 'saving' ? '答复保存中' : attentionLabels[item.kind] }}
          </Button>
        </div>
      </SheetContent>
    </Sheet>
    <PiReview v-model="reviewOpen" :run-id="session" :job-id="reviewJob" :latest-id="latest?.id || ''" :spec-id="spec"
      :running="active" :run="run" :focus-file="reviewFile" :project-names="projectNames" @cite="addCitation"
      @changed="refresh" @latest="openReview()" @closed="reviewClosed" @review-state="reviewState = $event" />
  </div>
</template>
