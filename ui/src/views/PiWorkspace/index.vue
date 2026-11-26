<script setup lang="ts">
import { computed, nextTick, onScopeDispose, shallowRef, useId, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMediaQuery } from '@vueuse/core'
import {
  ArrowLeft, ArrowUp, ChevronDown, FileDiff, FileText, Folder, ListChecks, ListPlus, PanelLeft,
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
import { useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'
import { sorted } from '@/lib/collections'
import { acknowledgeRequest, ApiError, contentEqual, errorMessage, resolvePendingRequest, type Stage } from '@/services/workbench'
import {
  admitAgent, deleteAgentSession, getAgentContinuation, getPiRuntime, piModelKey,
  type AgentAttention, type AgentContinuationSource, type AgentProjectAccess, type AgentSummary,
  type ProjectAccessMode, type ReviewExcerpt
} from '@/services/workbench-agent'
import { cancelJob, getReceipt, jobStatusLabels, piThinkingLabels, requestId, terminalJob,
  type JobRecord, type PiModel, type PiRuntimeStatus, type PiThinkingLevel } from '@/services/workbench-platform'
import PiSessionList from './components/PiSessionList.vue'
import PiWorkspaceSettings from './components/PiWorkspaceSettings.vue'
import PiRound from './components/PiRound.vue'
import PiReview from './components/PiReview.vue'
import PiQueuePanel from './components/PiQueuePanel.vue'
import { useAgentScope } from './useAgentScope'
import { usePiWorkspace } from './usePiWorkspace'
import { ciSelection, usePiDrafts, type PendingPiRequest, type PiCiAttachment } from './usePiDrafts'
import { usePiQueue, type PiEnqueueInput } from './usePiQueue'
import { projectCheckout, usePiProjects } from './usePiProjects'
import './workspace.css'

const props = defineProps<{ specId: string }>()
const route = useRoute()
const router = useRouter()
const { catalog } = useWorkbenchCatalog()
const { scopeOptions, lastAgentSpec } = useAgentScope()
const fromAgentWorkbench = computed(() => route.name === 'agent-workbench')
const backLabel = computed(() => fromAgentWorkbench.value ? '返回工作台' : '返回原 Spec')
const spec = computed(() => props.specId)
const session = computed(() => typeof route.query.session === 'string' ? route.query.session : '')
const handoffJob = computed(() => !session.value && typeof route.query.handoff === 'string' ? route.query.handoff : '')
const linkedRound = computed(() => typeof route.query.round === 'string' ? route.query.round : '')
const { context, contextError, pageNotice, listBusy, listCurrent, search, attentionOnly,
  run, runError, launchError, rounds, latest, summary, snapshotJobs, roundCiJobs, reading,
  refresh, recordAccepted, forgetSession, scheduleRefresh, loadMore } = usePiWorkspace(spec, session)
const { projectScope, projectBusy, projectError, refreshProjects } = usePiProjects(spec)
const projectNames = computed(() => Object.fromEntries(
  (projectScope.value?.projects ?? context.value?.projects ?? []).map((project) => [project.id, project.name])
))
const sourceSpec = computed(() => context.value?.spec ?? catalog.value?.specs.find((item) => item.id === spec.value))
const mainId = computed(() => sourceSpec.value?.projectId ?? '')
const mainName = computed(() => projectNames.value[mainId.value] ??
  catalog.value?.projects.find((project) => project.id === mainId.value)?.name)
const direct = computed(() => !session.value || (run.value?.workspaceMode ?? summary.value?.workspaceMode) === 'direct')
const snapshotIssue = computed(() => run.value?.snapshotError ?? summary.value?.snapshotError)
const title = computed(() => summary.value?.title || run.value?.prompt || (session.value ? 'Pi 会话' : '新会话'))
const progression = computed(() => context.value?.verification === 'current' ? context.value.stageBinding : null)
const availableStages = computed(() => progression.value?.stages ?? [])
const automaticStage = computed(() => progression.value?.primaryStage ?? undefined)
const active = computed(() => !!latest.value && !terminalJob(latest.value.status))
const preparing = computed(() => active.value &&
  (!run.value || latest.value?.status === 'queued' || latest.value?.status === 'preparing'))
const verificationIssue = computed(() => {
  if (!context.value || context.value.verification === 'current') return ''
  if (context.value.verification === 'failed') return context.value.verificationError || '规格来源核对失败。'
  return context.value.verification === 'stale' ? '规格来源有变化，正在重新核对。' : '已登记规格，正在核对来源。'
})
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
const draftKey = computed(() => spec.value + ':' + (session.value || (handoffJob.value ? 'handoff:' + handoffJob.value : 'new')))
const { draft, getDraft, removeDraft, persistDraft, storageIssue, pending, setPending, rememberAdmitted, admittedDraft, forgetAdmitted } =
  usePiDrafts(draftKey)
const perProject = computed(() => !session.value || !!run.value?.projectAccess?.length)
const selectedProjectIds = computed(() => [...new Set([mainId.value, ...(session.value ? run.value?.projectIds ?? [] : draft.value.selected)])])
const candidates = computed(() => new Map(projectScope.value?.projects.map((project) => [project.id, project])))
const staleProjectGrants = computed(() => projectScope.value
  ? Object.entries(draft.value.projectGrants)
    .filter(([id, grant]) => grant.mode !== 'read' && !contentEqual(grant.checkout, projectCheckout(candidates.value.get(id))))
    .map(([id]) => id)
  : [])
const projectGrantIssue = computed(() => staleProjectGrants.value.length
  ? '工程检出已变化，请重新选择权限或清除过期授权。' : '')
const projectModes = computed<Record<string, ProjectAccessMode>>({
  get: () => {
    const defaultMode = handoffJob.value ? 'read' : 'execute'
    return Object.fromEntries(selectedProjectIds.value.filter((id) => id && !staleProjectGrants.value.includes(id))
      .map((id) => [id, draft.value.projectGrants[id]?.mode ?? defaultMode]))
  },
  set: (modes) => {
    const current = { ...draft.value.projectGrants }
    for (const [id, mode] of Object.entries(modes)) {
      if (mode === projectModes.value[id]) continue
      const checkout = projectCheckout(candidates.value.get(id))
      if (mode === 'read') current[id] = { mode }
      else if (checkout) current[id] = { mode, checkout }
    }
    draft.value.projectGrants = Object.fromEntries(Object.entries(current)
      .filter(([id]) => selectedProjectIds.value.includes(id)))
    draft.value.error = ''
  }
})
function clearStaleProjectGrants() {
  const current = { ...draft.value.projectGrants }
  for (const id of staleProjectGrants.value) current[id] = { mode: 'read' }
  draft.value.projectGrants = current
  draft.value.error = ''
}
const ciIssue = computed(() => {
  if (draft.value.ciRecovery) return '请重新核对原 GitLab 作业并附加，或移除作业。'
  const attachment = draft.value.ciAttachment
  if (!attachment || !projectScope.value) return ''
  const checkout = projectCheckout(candidates.value.get(attachment.job.projectId))
  if (!selectedProjectIds.value.includes(attachment.job.projectId) || !checkout)
    return 'CI 作业的工程已移除或不可用，请重新选择作业或移除附件。'
  if (checkout.head !== attachment.checkout.head)
    return 'CI 作业所选工程的 HEAD 已变化，请重新核对并附加作业。'
  if (!contentEqual(checkout, attachment.checkout))
    return 'CI 作业所选工程的检出已变化，请重新核对并附加作业。'
  return ''
})
const permissionLabel = computed(() => {
  if (!perProject.value)
    return draft.value.readOnly ? '只读原工程' : draft.value.allowCommands ? '编辑原工程 · 已授予离线命令' : '编辑原工程'
  const modes = selectedProjectIds.value.map((id) => projectModes.value[id] ?? 'read')
  const execute = modes.filter((mode) => mode === 'execute').length
  const edit = modes.filter((mode) => mode === 'edit').length
  if (execute || edit) return [edit ? edit + ' 个工程可编辑' : '', execute ? execute + ' 个工程可运行离线命令' : ''].filter(Boolean).join(' · ')
  return selectedProjectIds.value.length > 1 ? selectedProjectIds.value.length + ' 个工程只读' : '只读原工程'
})
const handoffBusy = shallowRef('')
const handoffError = shallowRef('')
let handoffRequest: AbortController | undefined
const recoverableDraft = computed(() => {
  const job = latest.value
  if (!job || !terminalJob(job.status) || job.agentStartedAt || job.status === 'completed') return undefined
  const saved = admittedDraft(job.id)
  if (!saved) return undefined
  const current = getDraft(saved.key)
  return (!current.prompt.trim() && !current.citations.length) ||
    (current.prompt === saved.text.prompt && contentEqual(current.citations, saved.text.citations)) ? saved : undefined
})
const preparationFailure = computed(() => launchError.value || (recoverableDraft.value
  ? latest.value?.error?.message || '准备结束，任务草稿仍可恢复。' : ''))
async function restoreSubmittedDraft() {
  const saved = recoverableDraft.value
  const job = latest.value
  const owner = draftKey.value
  if (!saved || !job || projectBusy.value) return
  const scope = await refreshProjects()
  if (!alive || owner !== draftKey.value || job.id !== latest.value?.id || !recoverableDraft.value) return
  const target = getDraft(saved.key)
  target.prompt = saved.text.prompt
  target.citations = saved.text.citations
  target.handoff = saved.text.handoff
  target.selected = (saved.text.selected ?? []).filter((id) => !scope || scope.projects.some((item) =>
    item.id === id && item.status === 'ready' && item.head))
  target.readOnly = true
  target.allowCommands = false
  target.projectGrants = Object.fromEntries([mainId.value, ...target.selected].filter(Boolean)
    .map((id) => [id, { mode: 'read' }]))
  target.ciAttachment = undefined
  target.ciRecovery = saved.text.ciRecovery
  target.error = ''
  if (persistDraft(saved.key)) forgetAdmitted(job.id)
  if (saved.key !== draftKey.value && target.handoff) {
    await router.push(handoffLink(target.handoff.jobId))
    await nextTick()
    focusDraft()
  } else if (saved.key !== draftKey.value) await newSession()
  else focusDraft()
}
function changeCiAttachment(value?: PiCiAttachment) {
  draft.value.ciAttachment = value
  if (value) draft.value.ciRecovery = undefined
  draft.value.error = ''
}
function removeCiRecovery() {
  draft.value.ciRecovery = undefined
  draft.value.error = ''
}
const stageNeedsReview = computed(() => {
  const selection = draft.value.stageSelection
  return !!selection && (selection.goalVersion !== progression.value?.goalVersion || !availableStages.value.includes(selection.stage))
})
const selectedStage = computed<Stage | ''>({
  get: () => draft.value.stageSelection?.stage ?? '',
  set: (stage) => {
    const goalVersion = progression.value?.goalVersion
    const previous = draft.value.stageSelection
    if (!stage) draft.value.stageSelection = null
    else if (goalVersion && availableStages.value.includes(stage)) draft.value.stageSelection = { stage, goalVersion }
    if (previous !== draft.value.stageSelection) draft.value.error = ''
  }
})
watch([draftKey, availableStages], () => {
  if (session.value || draft.value.stageSelection !== undefined) return
  const requested = route.query.stage
  if (typeof requested === 'string' && availableStages.value.includes(requested as Stage)) {
    selectedStage.value = requested as Stage
  }
}, { immediate: true })
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
const receiptBusy = shallowRef(false)
const receiptError = shallowRef('')
let receiptRead: AbortController | undefined
const submittedRound = shallowRef<string>()
const stopBusy = shallowRef(false)
const stoppingFor = shallowRef('')
const roundStep = 3
const maxMountedRounds = 9
const showing = shallowRef(roundStep)
const lastVisibleRound = shallowRef<string>()
const readyForResume = computed(() => direct.value && !runError.value && run.value?.canResume &&
  (run.value.jobId === latest.value?.id || (!active.value && !!launchError.value)))
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
    (reading.value ? '正在核对原会话' : '当前会话无法续接，请从原 Spec 新建会话')
  return ''
})
const queueRun = computed(() => direct.value && run.value?.id === session.value ? session.value : '')
const { queue, busy: queueBusy, error: queueError, uncertain: queueUncertain, pendingEnqueue, retryRead: refreshQueue,
  enqueue: enqueueQueueTask, put: putQueueTask, remove: removeQueueTask, resume: resumeQueue } = usePiQueue(queueRun, scheduleRefresh)
const queueVerified = computed(() => context.value?.verification === 'current' &&
  !context.value.sourceError && !contextError.value && !pending.value)
const canEnqueue = computed(() => !!queueRun.value && queueVerified.value && !!queue.value &&
  !queueBusy.value && !queueUncertain.value && !pendingEnqueue.value && !sending.value && !deleteTarget.value && !!draft.value.prompt.trim())
const canEditQueueProjects = computed(() => run.value?.canEditQueuedProjects)
const queuePreparing = computed(() => !!queue.value?.items.some((item) => item.dispatching && item.jobId))
const canSend = computed(() => !active.value && !blocked.value && !contextError.value && !verificationIssue.value && !sending.value && !pending.value &&
  (!queueRun.value || (!!queue.value && !queue.value.items.length && !queueBusy.value && !queueUncertain.value && !pendingEnqueue.value)) &&
  !checking.value && !projectBusy.value && !settingsOpen.value && !deleteTarget.value &&
  !reviewState.value.busy && !reviewState.value.pending && !!readyModel.value && !!draft.value.prompt.trim())
const roundEnd = computed(() => {
  const index = lastVisibleRound.value ? rounds.value.findIndex((job) => job.id === lastVisibleRound.value) : -1
  return index < 0 ? rounds.value.length : index + 1
})
const roundStart = computed(() => Math.max(0, roundEnd.value - showing.value))
const visibleRounds = computed(() => rounds.value.slice(roundStart.value, roundEnd.value))
function moveRounds(older: boolean) {
  if (older && showing.value < maxMountedRounds) {
    showing.value = Math.min(maxMountedRounds, showing.value + roundStep)
    return
  }
  const end = Math.min(rounds.value.length, Math.max(showing.value, roundEnd.value + (older ? -roundStep : roundStep)))
  lastVisibleRound.value = end >= rounds.value.length ? undefined : rounds.value[end - 1]?.id
}
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
  void refreshProjects()
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
function handoffLink(jobId: string) {
  const target = sessionLink()
  return { ...target, query: { ...target.query, handoff: jobId } }
}
function sourceLink(source: AgentContinuationSource) {
  return sessionLink(source.runId, source.jobId)
}
async function createHandoff(job: JobRecord) {
  const runId = session.value
  const specId = spec.value
  if (!runId || !terminalJob(job.status) || handoffBusy.value) return
  const request = new AbortController()
  handoffRequest = request
  handoffBusy.value = job.id
  handoffError.value = ''
  const stillCurrent = () => alive && !request.signal.aborted && session.value === runId && spec.value === specId
  try {
    const [result, scope] = await Promise.all([
      getAgentContinuation(specId, { runId, jobId: job.id }, request.signal),
      refreshProjects()
    ])
    if (!stillCurrent()) return
    if (!scope) throw new Error(projectError.value || '请重新核对参与工程。')
    const key = specId + ':handoff:' + result.jobId
    const target = getDraft(key)
    if (!target.handoff) {
      const { prompt, ...handoff } = result
      target.handoff = handoff
      target.selected = result.projectIds.filter((id) => id !== mainId.value && scope.projects.some((candidate) =>
        candidate.id === id && candidate.status === 'ready' && candidate.head))
      target.prompt = prompt
      persistDraft(key)
    }
    historyDrawer.value = false
    summaryDrawer.value = false
    await router.push(handoffLink(result.jobId))
    await nextTick()
    focusDraft()
  } catch (cause) {
    if (stillCurrent()) handoffError.value = errorMessage(cause)
  } finally {
    if (handoffRequest === request) {
      handoffRequest = undefined
      handoffBusy.value = ''
    }
  }
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
    removeDraft(`${target.specId}:${target.session.id}`)
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
async function backToLatest() {
  lastVisibleRound.value = undefined
  await router.replace(sessionLink(session.value))
  await nextTick()
  const viewport = conversation.value?.querySelector<HTMLElement>('[data-slot="message-scroller-viewport"]')
  viewport?.scrollTo({ top: viewport.scrollHeight, behavior: 'instant' })
  focusTranscript()
}
watch(compact, (isCompact) => {
  if (!isCompact) historyDrawer.value = false
})
watch([context, session], ([workspace, id]) => {
  if (!workspace || workspace.spec.id !== spec.value) return
  if (workspace.spec.active) lastAgentSpec.value = spec.value
  if (id || route.query.new || pending.value || search.value || attentionOnly.value) return
  const first = workspace.sessions[0]
  if (first) void router.replace(sessionLink(first.id))
}, { immediate: true })
watch([spec, session], () => {
  showing.value = roundStep
  lastVisibleRound.value = undefined
  readyModel.value = undefined
  runtimeStatus.value = undefined
  modelIssue.value = ''
  settingsOpen.value = false
  reviewOpen.value = false
  focusActivity.value = undefined
  draft.value.allowCommands = false
  handoffRequest?.abort()
  handoffError.value = ''
})
let requestedRound = ''
watch([rounds, linkedRound], async ([items, id]) => {
  if (!id) { requestedRound = ''; return }
  if (id === requestedRound) return
  const index = items.findIndex((job) => job.id === id)
  if (index < 0) return
  requestedRound = id
  if (index < roundStart.value || index >= roundEnd.value) {
    const end = Math.min(items.length, Math.max(showing.value, index + 2))
    lastVisibleRound.value = end >= items.length ? undefined : items[end - 1]?.id
  }
  await nextTick()
  const node = document.getElementById('pi-turn-' + id)?.querySelector<HTMLElement>('article')
  if (node && !focusActivity.value) focusNode(node)
}, { flush: 'post' })
watch([() => context.value?.spec.id, session, readyForResume, selectedModel, settingsOpen], ([source, id, resumable, selected, open], previous, onCleanup) => {
  checking.value = false
  if (!source || !context.value?.spec.active || (id && !resumable) || open) return
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
onScopeDispose(() => {
  alive = false
  receiptRead?.abort()
  handoffRequest?.abort()
})

function matchingReceipt(value: unknown, item: PendingPiRequest): value is JobRecord {
  if (!value || typeof value !== 'object') return false
  const job = value as Partial<JobRecord>
  return job.kind === 'agent' && typeof job.id === 'string' && typeof job.status === 'string' &&
    job.requestId === item.id && (!job.agentSpecId || job.agentSpecId === item.input.specId) &&
    (!item.input.resumeRunId || !job.agentRunId || job.agentRunId === item.input.resumeRunId)
}
async function settlePending(item: PendingPiRequest, job: JobRecord) {
  if (!matchingReceipt(job, item)) throw new Error('受理记录与原请求不一致，请重新核对。')
  const source = item.input.specId
  const next = job.agentRunId || item.input.resumeRunId || job.id
  const current = getDraft(item.key)
  const excerpts = item.excerpts ?? []
  const started = !!job.agentStartedAt || job.status === 'completed'
  const ci = item.input.ciJob
  const expectedCi = ci ? { projectId: ci.projectId, linkId: ci.linkId, jobId: ci.jobId, mode: ci.mode } : undefined
  const expectedSelected = (item.input.projectAccess?.map((entry) => entry.projectId) ?? item.input.projectIds)
    ?.filter((id) => id !== mainId.value)
  const sameProjectGrants = Object.entries(current.projectGrants).every(([projectId, grant]) => {
    const submitted = item.input.projectAccess?.find((entry) => entry.projectId === projectId)
    return submitted?.mode === grant.mode && (grant.mode === 'read' || (submitted.expectedHead === grant.checkout.head &&
      submitted.expectedRootPath === grant.checkout.rootPath && submitted.expectedBranch === grant.checkout.branch))
  })
  const sameStage = current.stageSelection === undefined || (current.stageSelection?.stage === item.input.lifecycleStage &&
    current.stageSelection?.goalVersion === item.input.expectedGoalVersion)
  const submittedDraftMatches = current.prompt === item.input.prompt && contentEqual(current.citations, excerpts) &&
    contentEqual(ciSelection(current), expectedCi) &&
    (item.input.resumeRunId || contentEqual(sorted(current.selected), sorted(expectedSelected ?? []))) &&
    sameProjectGrants && sameStage && (!current.selectedModel || contentEqual(current.selectedModel, item.input.selectedModel)) &&
    current.handoff?.jobId === item.input.continuationSource?.jobId &&
    current.handoff?.runId === item.input.continuationSource?.runId
  const saved = started || rememberAdmitted(job.id, item.key, {
    prompt: item.input.prompt, citations: excerpts, handoff: current.handoff,
    selected: expectedSelected, ciRecovery: expectedCi
  })
  if (saved && submittedDraftMatches) {
    current.prompt = ''
    current.citations = []
    current.ciAttachment = undefined
    current.ciRecovery = undefined
    current.handoff = undefined
    if (!item.input.resumeRunId) current.selected = []
    current.readOnly = true
    current.allowCommands = false
    current.stageSelection = null
    current.error = ''
    persistDraft(item.key)
  }
  current.projectGrants = {}
  setPending(item.key)
  resolvePendingRequest(item.id)
  recordAccepted(source, next, job)
  if (draftKey.value === item.key) {
    receiptError.value = ''
    submittedRound.value = job.id
    lastVisibleRound.value = undefined
    await router.replace(sessionLink(next))
    await nextTick()
    focusTranscript()
  }
}
async function checkPending() {
  const item = pending.value
  if (!item || sending.value || receiptBusy.value) return
  receiptRead?.abort()
  const read = new AbortController()
  receiptRead = read
  receiptBusy.value = true
  receiptError.value = ''
  try {
    const response = await getReceipt(item.id, read.signal)
    if (read.signal.aborted || pending.value?.id !== item.id) return
    if (!response.found) {
      receiptError.value = '尚未找到受理记录，可以按同一请求重试。'
      return
    }
    if (!matchingReceipt(response.result, item)) {
      receiptError.value = '受理记录与原请求不一致，请查看执行记录。'
      return
    }
    await settlePending(item, response.result)
    void acknowledgeRequest(item.id)
  } catch (cause) {
    if (!read.signal.aborted) receiptError.value = errorMessage(cause)
  } finally {
    if (receiptRead === read) {
      receiptRead = undefined
      receiptBusy.value = false
    }
  }
}
watch(draftKey, () => {
  receiptRead?.abort()
  receiptRead = undefined
  receiptBusy.value = false
  receiptError.value = ''
  void checkPending()
}, { immediate: true })
watch([() => latest.value?.id, () => latest.value?.agentStartedAt, () => latest.value?.status], ([id, started, status]) => {
  if (id && (started || status === 'completed')) forgetAdmitted(id)
})

async function dispatch(item: PendingPiRequest) {
  if (sending.value || receiptBusy.value) return
  sending.value = true
  receiptError.value = ''
  const current = getDraft(item.key)
  current.error = ''
  try {
    const job = await admitAgent(item.input, item.id)
    if (!alive) return
    await settlePending(item, job)
  } catch (cause) {
    if (!alive) return
    const definitive = cause instanceof ApiError && cause.status >= 400 && cause.status < 500 &&
      cause.status !== 408 && cause.context.uncertain !== true
    if (!definitive) {
      if (draftKey.value === item.key) receiptError.value = errorMessage(cause)
      return
    }
    setPending(item.key)
    current.error = errorMessage(cause)
    if (draftKey.value === item.key && cause.context.piIssue) {
      readyModel.value = undefined
      openSettings()
    }
    if (draftKey.value === item.key && cause.context.progressBinding) {
      void refresh()
      openSettings()
    }
    scheduleRefresh()
  } finally {
    sending.value = false
    if (alive && draftKey.value !== item.key && pending.value) void checkPending()
  }
}
function retryPending() {
  const item = pending.value
  if (item) void dispatch(item)
}
async function send() {
  if (!canSend.value || !readyModel.value) return
  const current = draft.value
  const key = draftKey.value
  const id = session.value
  let projectAccess: AgentProjectAccess[] | undefined
  if (perProject.value) {
    const scope = projectScope.value && !projectError.value ? projectScope.value : await refreshProjects()
    if (!alive || draftKey.value !== key || !canSend.value || !readyModel.value) return
    if (!scope || projectError.value) {
      current.error = projectError.value || '尚未核对参与工程，请重试。'
      openSettings()
      return
    }
    if (projectGrantIssue.value) {
      current.error = projectGrantIssue.value
      openSettings()
      return
    }
    projectAccess = []
    for (const projectId of selectedProjectIds.value) {
      const candidate = candidates.value.get(projectId)
      if (!candidate?.head || candidate.status !== 'ready') {
        current.error = '参与工程 ' + (candidate?.name ?? projectId) + ' 当前不可用，请检查运行范围。'
        openSettings()
        return
      }
      projectAccess.push({
        projectId, mode: projectModes.value[projectId] ?? 'read',
        expectedHead: candidate.head, expectedRootPath: candidate.rootPath, expectedBranch: candidate.branch
      })
    }
  }
  if (stageNeedsReview.value) {
    current.error = '目标版本或适用阶段已变化，请重新选择本轮跟进阶段。'
    openSettings()
    return
  }
  if (ciIssue.value) {
    current.error = ciIssue.value
    openSettings()
    return
  }
  if (handoffJob.value && !current.handoff) {
    current.error = '此接续草稿已用完或不在此标签页，请从来源轮次重新生成。'
    return
  }
  const attachment = current.ciAttachment
  const item: PendingPiRequest = {
    id: requestId(),
    key,
    input: {
      specId: spec.value,
      ...(projectAccess ? { projectAccess } : {
        projectIds: selectedProjectIds.value, readOnly: current.readOnly, allowCommands: !current.readOnly && current.allowCommands
      }),
      continuationSource: current.handoff ? { runId: current.handoff.runId, jobId: current.handoff.jobId } : undefined,
      ciJob: attachment ? {
        projectId: attachment.job.projectId, linkId: attachment.job.linkId, jobId: attachment.job.jobId,
        mode: attachment.mode, expectedSha: attachment.job.sha
      } : undefined,
      resumeRunId: id || undefined, expectedJobId: id ? run.value?.jobId : undefined,
      selectedModel: current.selectedModel ? { ...current.selectedModel } : undefined, expectedModel: { ...readyModel.value },
      thinkingLevel: current.thinkingLevel, prompt: current.prompt,
      lifecycleStage: current.stageSelection?.stage, expectedGoalVersion: current.stageSelection?.goalVersion,
      citations: current.citations.map(({ reviewToken, projectId, path, side, startLine, endLine, note }) =>
        ({ reviewToken, projectId, path, side, startLine, endLine, note }))
    },
    excerpts: current.citations.map((excerpt) => ({ ...excerpt }))
  }
  persistDraft(key)
  if (!setPending(key, item)) {
    current.error = '此标签页无法保存本次请求，任务尚未发送。'
    return
  }
  await dispatch(item)
}

function queueInput(current: typeof draft.value): PiEnqueueInput {
  return {
    prompt: current.prompt, readOnly: current.readOnly, expectedGoalVersion: progression.value?.goalVersion,
    citations: current.citations.map(({ reviewToken, projectId, path, side, startLine, endLine, note }) =>
      ({ reviewToken, projectId, path, side, startLine, endLine, note }))
  }
}
async function enqueue(retrying = false) {
  const saved = pendingEnqueue.value
  if (retrying ? (!saved || !queueVerified.value) : !canEnqueue.value) return
  const key = draftKey.value
  const current = draft.value
  const input: PiEnqueueInput = saved ? {
    prompt: saved.prompt, readOnly: saved.readOnly, expectedGoalVersion: saved.expectedGoalVersion, citations: saved.citations
  } : queueInput(current)
  const added = await enqueueQueueTask(retrying ? undefined : input)
  if (added && key === draftKey.value && contentEqual(queueInput(current), input)) {
    current.prompt = ''
    current.citations = []
    current.allowCommands = false
    current.readOnly = true
    current.projectGrants = {}
    persistDraft(key)
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
        <PiSessionList v-if="context" v-model:search="search" v-model:attention-only="attentionOnly"
          :context="context" :active-session="session" :error="contextError" :notice="pageNotice" :busy="listBusy" :current="listCurrent"
          :scope-options="fromAgentWorkbench ? scopeOptions : undefined"
          @new="newSession" @choose="choose" @refresh="refresh" @load-more="loadMore"
          @remove="askDelete" @select-scope="selectAgentScope" />
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
      <div v-if="verificationIssue && !active" class="pi-top-note" role="status">
        {{ verificationIssue }}
        <Button v-if="context?.verification === 'failed'" type="button" variant="ghost" size="sm" @click="refresh">重新读取</Button>
      </div>
      <div v-if="preparing" class="pi-top-note" role="status">
        <LoadingIndicator :active="true" />{{ stoppingFor ? '正在取消准备' : '正在准备 Pi 运行' }}
        <Button type="button" variant="ghost" size="sm" :disabled="stopBusy || !!stoppingFor" @click="stop">取消准备</Button>
      </div>
      <div v-if="preparationFailure" class="pi-top-note" role="status">
        {{ preparationFailure }}
        <Button v-if="recoverableDraft" type="button" variant="ghost" size="sm" :disabled="projectBusy" @click="restoreSubmittedDraft">
          <LoadingIndicator :active="projectBusy" />恢复任务草稿
        </Button>
        <RouterLink v-if="latest" :to="'/jobs/' + latest.id">查看执行记录</RouterLink>
      </div>
      <div v-if="pending" class="pi-top-note" role="status">
        {{ sending ? '正在提交 Pi 任务…' : receiptBusy ? '正在核对受理记录…' : receiptError || '本次请求的受理结果待核对。' }}
        <span v-if="pending.input.projectAccess?.some((item) => item.mode !== 'read')">原请求包含工程编辑或离线命令权限。</span>
        <span v-else-if="pending.input.readOnly === false">原请求允许编辑原工程<template v-if="pending.input.allowCommands">并运行离线命令</template>。</span>
        <Button type="button" variant="ghost" size="sm" :disabled="sending || receiptBusy" @click="checkPending">核对回执</Button>
        <Button type="button" variant="outline" size="sm" :disabled="sending || receiptBusy" @click="retryPending">按同一请求重试</Button>
      </div>
      <div v-if="handoffError" class="pi-top-note" role="status">{{ handoffError }}</div>
      <div v-if="direct && snapshotIssue" class="pi-top-note" role="status">
        本轮差异待核实。{{ snapshotIssue }}
      </div>
      <div v-if="!direct && reviewState.pending && !reviewState.busy && !reviewOpen && !active && !blocked" class="pi-top-note" role="status">
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
                <div v-if="!session && sourceSpec" class="pi-new-session">
                  <h2>{{ sourceSpec.title }}</h2>
                  <p>{{ mainName || '主工程' }} · {{ sourceSpec.sourcePath }}</p>
                  <Button v-if="context?.spec.active && !context.sourceError" variant="outline" size="sm" @click="openSettings">
                    <Settings2 :size="14" /> 查看运行范围
                  </Button>
                </div>
                <p v-if="runError && !visibleRounds.length" class="pi-issue" role="alert">{{ runError }}</p>
                <Button v-if="roundStart" type="button" variant="ghost" size="sm" class="pi-more-rounds" @click="moveRounds(true)">
                  加载更早轮次
                </Button>
                <MessageScrollerItem v-for="job in visibleRounds" :id="'pi-turn-' + job.id" :key="job.id"
                  :message-id="job.id" :scroll-anchor="job.id === submittedRound && !terminalJob(job.status)"
                  :data-workbench-reveal="job.id === submittedRound ? '' : undefined" class="pi-turn">
                  <PiRound :job="job" :direct="direct" :latest="job.id === latest?.id" :stopping="job.id === stoppingFor"
                    :track-first-text="job.id === submittedRound"
                    :focus-activity="focusActivity?.jobId === job.id ? focusActivity : undefined"
                    :can-review="snapshotJobs.has(job.id)" :can-review-latest="!!latest && snapshotJobs.has(latest.id)"
                    :project-names="projectNames" :read-only="run?.jobId === job.id ? run?.readOnly : undefined"
                    :review-targets="job.id === summary?.jobId ? summary.reviewTargets : undefined"
                    @updated="scheduleRefresh" @focus="focusNode" @review="(project, path) => openReview(job.id, project, path)"
                    @review-latest="openReview()" />
                  <aside v-if="roundCiJobs[job.id]" class="pi-ci-evidence" aria-label="本轮 GitLab CI 证据">
                    <p>GitLab CI #{{ roundCiJobs[job.id]?.jobId }} ·
                      {{ roundCiJobs[job.id]?.mode === 'reproduce' ? '同提交复现' : '差异对照' }}</p>
                    <p>CI 提交 <code>{{ roundCiJobs[job.id]?.sha }}</code></p>
                    <p v-if="roundCiJobs[job.id]?.localHead">
                      本机 {{ roundCiJobs[job.id]?.localBranch || '游离 HEAD' }} · <code>{{ roundCiJobs[job.id]?.localHead }}</code>
                    </p>
                    <p v-if="roundCiJobs[job.id]?.traceStatus">{{ roundCiJobs[job.id]?.traceStatus }}</p>
                  </aside>
                  <Button v-if="run && terminalJob(job.status)"
                    type="button" variant="ghost" size="sm" :disabled="!!handoffBusy" @click="createHandoff(job)">
                    <LoadingIndicator :active="handoffBusy === job.id" />{{ handoffBusy === job.id ? '正在生成接续草稿…' : '生成接续草稿' }}
                  </Button>
                </MessageScrollerItem>
                <div v-if="roundEnd < rounds.length" class="pi-more-rounds">
                  <Button type="button" variant="ghost" size="sm" @click="moveRounds(false)">加载较新轮次</Button>
                  <Button type="button" variant="ghost" size="sm" @click="backToLatest">返回最新轮</Button>
                </div>
              </MessageScrollerContent>
            </MessageScrollerViewport>
            <MessageScrollerButton size="sm" variant="outline" behavior="auto">回到最新</MessageScrollerButton>
          </MessageScroller>
        </MessageScrollerProvider>
      </div>
      <div class="pi-composer-wrap">
        <p v-if="storageIssue" class="pi-issue" role="status">{{ storageIssue }}</p>
        <div v-if="pending && receiptError" class="pi-top-note" role="status">
          {{ receiptError }}
          <Button type="button" variant="ghost" size="sm" :disabled="sending || receiptBusy" @click="retryPending">
            按原请求重试
          </Button>
        </div>
        <Button v-if="summary?.hasSnapshot || summary?.hasAcceptance" type="button" variant="outline"
          class="pi-diff-shortcut" @click="openReview()"><FileDiff :size="15" />
          {{ summary.pendingFiles ? summary.pendingFiles + ' 个文件待审阅' : '查看执行成果' }}
        </Button>
        <form aria-label="发送 Pi 任务" @submit.prevent="send">
          <InputGroup class="pi-composer">
            <div v-if="draft.handoff || draft.ciAttachment || draft.ciRecovery" class="pi-draft-excerpts">
              <div v-if="draft.handoff" class="pi-draft-excerpt">
                <details>
                  <summary>接续来源 · {{ draft.handoff.jobId.slice(0, 8) }}</summary>
                  <RouterLink :to="sourceLink(draft.handoff)">查看来源轮次</RouterLink>
                  <p v-for="workspace in draft.handoff.workspaces" :key="workspace.projectIds.join(',')">
                    {{ workspace.projectIds.map((id) => projectNames[id] ?? id).join('、') }} ·
                    {{ workspace.branch || '游离 HEAD' }} · <code>{{ workspace.head }}</code>
                  </p>
                </details>
              </div>
              <div v-if="draft.ciAttachment" class="pi-draft-excerpt">
                <span>CI #{{ draft.ciAttachment.job.jobId }} · {{ draft.ciAttachment.mode === 'reproduce' ? '同提交复现' : '差异对照' }}</span>
                <Button type="button" size="sm" variant="ghost" @click="changeCiAttachment()">移除作业</Button>
              </div>
              <div v-if="draft.ciRecovery" class="pi-draft-excerpt">
                <span>CI #{{ draft.ciRecovery.jobId }} · 作业待重新核对</span>
                <Button type="button" size="sm" variant="ghost" @click="openSettings">重新核对作业</Button>
                <Button type="button" size="sm" variant="ghost" @click="removeCiRecovery">移除作业</Button>
              </div>
            </div>
            <div v-if="draft.citations.length" class="pi-draft-excerpts">
              <div v-for="(excerpt, index) in draft.citations" :key="index" class="pi-draft-excerpt">
                <details>
                  <summary>{{ projectNames[excerpt.projectId] ?? excerpt.projectId }} / {{ excerpt.path }} · {{ excerpt.startLine }}–{{ excerpt.endLine }} 行</summary>
                  <p>{{ excerpt.side === 'initial' ? '运行起点' : '本轮结束' }} · 轮次 {{ excerpt.jobId }} · 版本 {{ excerpt.reviewToken.slice(0, 10) }}</p>
                  <pre>{{ excerpt.snippet }}</pre>
                  <p>{{ excerpt.note }}</p>
                </details>
                <Button type="button" variant="ghost" size="sm" @click="draft.citations.splice(index, 1)">移除引用</Button>
              </div>
            </div>
            <InputGroupTextarea id="pi-workspace-draft" v-model="draft.prompt" aria-label="Pi 任务"
              :placeholder="active ? '准备下一轮要求' : '给 Pi 发一项任务'" rows="2" :disabled="sending" @keydown="onEnter" />
            <InputGroupAddon align="block-end" class="pi-composer-tools">
              <Button type="button" variant="ghost" size="sm" :disabled="!context || (!!session && !direct)" @click="openSettings">
                <Settings2 :size="15" /><span>{{ permissionLabel }}</span>
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
              <Button v-if="queueRun" type="button" variant="ghost" size="sm" :disabled="!canEnqueue"
                aria-label="加入续办队列" :title="perProject ? '按本会话范围排队，默认只读' : '沿用本会话范围与模型，离线命令关闭'" @click="enqueue()">
                <ListPlus :size="15" />排队
              </Button>
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
        <p v-if="draft.error || storageIssue || (!active && modelIssue && !readyModel)" class="pi-composer-error" role="status">
          {{ draft.error || storageIssue || modelIssue }}
        </p>
        <p v-if="pendingEnqueue" class="pi-composer-error" role="status">
          {{ queueBusy ? '正在读取队列…' : queueError || '原入队请求待核对。' }}
          <Button type="button" variant="ghost" size="sm" :disabled="queueBusy || !queue || !queueVerified"
            @click="enqueue(true)">按原任务重试入队</Button>
        </p>
        <p v-else-if="queue?.items.length" class="pi-composer-error" role="status">
          {{ queue.items.length }} 条待发任务
          <span v-if="queuePreparing">· 队首已受理，等待 Pi 确认</span>
          <Button type="button" variant="ghost" size="sm" @click="toggleSummary">查看队列</Button>
        </p>
        <p v-else-if="queueError && queueRun" class="pi-composer-error" role="status">
          {{ queueError }}
          <Button type="button" variant="ghost" size="sm" :disabled="queueBusy" @click="refreshQueue">重读队列</Button>
        </p>
      </div>
      <Transition name="workbench-feedback">
      <aside v-if="inlineSummary" v-show="summaryOpen" :inert="!summaryOpen" class="pi-summary" aria-label="会话摘要">
        <section>
          <h2>会话摘要</h2>
          <p v-if="status">{{ status }}</p>
          <RouterLink v-if="run?.continuationSource" :to="sourceLink(run.continuationSource)">查看接续来源</RouterLink>
          <p v-if="run?.ciJob">CI #{{ run.ciJob.jobId }} · {{ run.ciJob.mode === 'reproduce' ? '同提交复现' : '差异对照' }} · <code>{{ run.ciJob.sha }}</code><br v-if="run.ciJob.traceStatus">{{ run.ciJob.traceStatus }}</p>
          <p v-if="summary?.reviewError" class="pi-issue">{{ summary.reviewError }}</p>
          <Button v-if="latest && (summary?.hasSnapshot || summary?.hasAcceptance)" type="button" variant="ghost" class="pi-summary-action"
            @click="openReview()"><FileDiff :size="15" /> {{ summary.pendingFiles ? summary.pendingFiles + ' 个文件待审阅' : '执行成果' }}</Button>
          <Button v-for="(item, index) in summaryActions" :key="index" type="button" variant="ghost" class="pi-summary-action"
            @click="jump(item)">{{ item.status === 'saving' ? '答复保存中' : attentionLabels[item.kind] }}</Button>
        </section>
        <PiQueuePanel v-if="queueRun" :queue="queue" :busy="queueBusy" :error="queueError" :uncertain="queueUncertain || !!pendingEnqueue"
          :active="active" :verified="queueVerified" :goal-version="progression?.goalVersion"
          :per-project="perProject" :can-edit-projects="canEditQueueProjects"
          :refresh="refreshQueue" :put="putQueueTask" :remove="removeQueueTask" :resume="resumeQueue" />
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
        <PiSessionList v-if="context" v-model:search="search" v-model:attention-only="attentionOnly"
          :context="context" :active-session="session" :error="contextError" :notice="pageNotice" :busy="listBusy" :current="listCurrent"
          :scope-options="fromAgentWorkbench ? scopeOptions : undefined"
          @new="newSession" @choose="choose" @refresh="refresh" @load-more="loadMore"
          @remove="askDelete" @select-scope="selectAgentScope" />
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
          <SheetDescription>{{ context?.spec.title }}</SheetDescription></SheetHeader>
        <div v-if="context" class="pi-sheet-body">
          <PiWorkspaceSettings :key="draftKey" v-model:ready-model="readyModel" v-model:selected-model="selectedModel"
            v-model:runtime-status="runtimeStatus"
            v-model:blocked-reason="modelIssue" v-model:model-restrictions="modelRestrictions"
            v-model:selected="draft.selected" v-model:allow-commands="draft.allowCommands" v-model:project-modes="projectModes"
            v-model:read-only="draft.readOnly" v-model:stage="selectedStage"
            :ci-attachment="draft.ciAttachment" :ci-recovery="draft.ciRecovery" @update:ci-attachment="changeCiAttachment"
            :spec-id="spec" :main-id="mainId" :projects="context.projects" :project-scope="projectScope"
            :project-busy="projectBusy" :project-error="projectError" :per-project="perProject" :ci-issue="ciIssue"
            :project-grant-issue="projectGrantIssue" @clear-stale-project-grants="clearStaleProjectGrants"
            :review-resume-id="session || undefined" :busy="sending"
            :participating-ids="run?.projectIds" :available-stages="availableStages" :automatic-stage="automaticStage"
            :stage-needs-review="stageNeedsReview" @refresh-projects="refreshProjects" />
        </div>
      </SheetContent>
    </Sheet>
    <Sheet v-model:open="summaryDrawer">
      <SheetContent class="pi-sheet" @close-auto-focus.prevent="summaryClosed">
        <SheetHeader class="pi-sheet-header"><SheetTitle>会话摘要</SheetTitle><SheetDescription>{{ title }}</SheetDescription></SheetHeader>
        <div class="pi-sheet-body pi-mobile-summary">
          <p v-if="status">{{ status }}</p>
          <RouterLink v-if="run?.continuationSource" :to="sourceLink(run.continuationSource)">查看接续来源</RouterLink>
          <p v-if="run?.ciJob">CI #{{ run.ciJob.jobId }} · {{ run.ciJob.mode === 'reproduce' ? '同提交复现' : '差异对照' }} · <code>{{ run.ciJob.sha }}</code><br v-if="run.ciJob.traceStatus">{{ run.ciJob.traceStatus }}</p>
          <RouterLink :to="{ path: '/specs/' + spec }" class="pi-summary-link"><FileText :size="15" />{{ context?.spec.title || '原 Spec' }}</RouterLink>
          <Button v-if="latest && (summary?.hasSnapshot || summary?.hasAcceptance)" type="button" variant="outline"
            @click="openReview()"><FileDiff :size="15" />执行成果</Button>
          <Button v-for="(item, index) in summaryActions" :key="index" type="button" variant="ghost" @click="jump(item)">
            {{ item.status === 'saving' ? '答复保存中' : attentionLabels[item.kind] }}
          </Button>
          <PiQueuePanel v-if="queueRun" :queue="queue" :busy="queueBusy" :error="queueError" :uncertain="queueUncertain || !!pendingEnqueue"
            :active="active" :verified="queueVerified" :goal-version="progression?.goalVersion"
          :per-project="perProject" :can-edit-projects="canEditQueueProjects"
            :refresh="refreshQueue" :put="putQueueTask" :remove="removeQueueTask" :resume="resumeQueue" />
        </div>
      </SheetContent>
    </Sheet>
    <PiReview v-model="reviewOpen" :run-id="session" :job-id="reviewJob" :latest-id="latest?.id || ''" :spec-id="spec"
      :running="active" :direct="direct" :run="run" :focus-file="reviewFile" :project-names="projectNames" @cite="addCitation"
      @changed="refresh" @latest="openReview()" @closed="reviewClosed" @review-state="reviewState = $event" />
  </div>
</template>

<style scoped>
.pi-ci-evidence {
  margin-block: 8px;
  padding: 8px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  color: var(--brand-color-text-secondary);
  font-size: 12px;
  overflow-wrap: anywhere;
}
</style>
