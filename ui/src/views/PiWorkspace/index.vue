<script setup lang="ts">
import { computed, nextTick, onScopeDispose, reactive, shallowRef, useId, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMediaQuery } from '@vueuse/core'
import {
  ArrowLeft, ArrowUp, ChevronDown, FileDiff, FileText, Folder, ListChecks, ListPlus, PanelLeft,
  Settings2, Square, Terminal
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
import { terminalPanelOpenUrl } from '@/services/terminal-panel'
import { acknowledgeRequest, ApiError, contentEqual, errorMessage, resolvePendingRequest, type Stage } from '@/services/workbench'
import {
  admitAgent, confirmAgentHandoff, deleteAgentSession, getAgentContinuation, getAgentRounds, getAgentRun, getGrokRuntime, getPiRuntime, piModelKey,
  type AgentAttention, type AgentContinuation, type AgentContinuationPreview, type AgentContinuationSource, type AgentHandoffPreview, type AgentProjectAccess, type AgentStart, type AgentSummary,
  type ProjectAccessMode, type ReviewExcerpt
} from '@/services/workbench-agent'
import { agentHostLabels, agentPhaseLabel, agentRoundHost, cancelJob, getReceipt, grokRuntimeLabel, isWorkspaceAgent, jobStatusLabels,
  piCommandCapability, piCommandPermissionLabels, piRuntimeAvailable, piThinkingLabels, requestId, terminalJob,
  type AgentHost, type GrokRuntimeStatus, type JobRecord, type PiCommandExecution, type PiModel, type PiRuntimeStatus, type PiThinkingLevel } from '@/services/workbench-platform'
import PiSessionList from './components/PiSessionList.vue'
import PiWorkspaceSettings from './components/PiWorkspaceSettings.vue'
import PiRound from './components/PiRound.vue'
import PiExecutionStatus from './components/PiExecutionStatus.vue'
import PiReview from './components/PiReview.vue'
import PiQueuePanel from './components/PiQueuePanel.vue'
import PiLocalTasks from './components/PiLocalTasks.vue'
import PiHandoff from './components/PiHandoff.vue'
import AgentHandoffDialog from './components/AgentHandoffDialog.vue'
import { useAgentScope } from './useAgentScope'
import { usePiWorkspace } from './usePiWorkspace'
import { ciSelection, crossHandoffDraftKey, usePiDrafts, type PendingPiRequest, type PiCiAttachment } from './usePiDrafts'
import { usePiQueue, type PiEnqueueInput } from './usePiQueue'
import { projectCheckout, usePiProjects } from './usePiProjects'
import './workspace.css'

const props = defineProps<{ specId: string }>()
const route = useRoute()
const router = useRouter()
const terminalOpen = terminalPanelOpenUrl()
const { catalog } = useWorkbenchCatalog()
const { scopeOptions, lastAgentSpec } = useAgentScope()
const fromAgentWorkbench = computed(() => route.name === 'agent-workbench')
const backLabel = computed(() => fromAgentWorkbench.value ? '返回工作台' : '返回原 Spec')
const spec = computed(() => props.specId)
const session = computed(() => typeof route.query.session === 'string' ? route.query.session : '')
const handoffJob = computed(() => !session.value && typeof route.query.handoff === 'string' ? route.query.handoff : '')
const handoffCli = computed(() => handoffJob.value && isWorkspaceAgent(route.query.handoffCli) ? route.query.handoffCli : undefined)
const linkedRound = computed(() => typeof route.query.round === 'string' ? route.query.round : '')
const { context, contextError, pageNotice, listBusy, listCurrent, search, attentionOnly,
  run, runError, launchError, rounds, latest, summary, snapshotJobs, roundCiJobs, roundActions, reading,
  refresh, recordAccepted, forgetSession, scheduleRefresh, loadMore } = usePiWorkspace(spec, session, linkedRound)
const recordedHost = computed<AgentHost>(() => {
  const id = session.value
  const host = (run.value?.id === id ? run.value.host : undefined) ??
    (latest.value?.agentRunId === id ? latest.value.agentHost : undefined) ??
    (summary.value?.id === id ? summary.value.host : undefined)
  return isWorkspaceAgent(host) ? host : 'pi'
})
const { projectScope, projectBusy, projectError, refreshProjects, invalidateProjects } = usePiProjects(spec)
const projectNames = computed(() => Object.fromEntries(
  (projectScope.value?.projects ?? context.value?.projects ?? []).map((project) => [project.id, project.name])
))
const sourceSpec = computed(() => context.value?.spec ?? catalog.value?.specs.find((item) => item.id === spec.value))
const mainId = computed(() => sourceSpec.value?.projectId ?? '')
const mainName = computed(() => projectNames.value[mainId.value] ??
  catalog.value?.projects.find((project) => project.id === mainId.value)?.name)
const direct = computed(() => !session.value || (run.value?.workspaceMode ?? summary.value?.workspaceMode) === 'direct')
const snapshotIssue = computed(() => run.value?.snapshotError ?? summary.value?.snapshotError)
const title = computed(() => summary.value?.title || run.value?.prompt || (session.value ? agentHostLabels[recordedHost.value] + ' 会话' : '新会话'))
const progression = computed(() => context.value?.verification === 'current' ? context.value.stageBinding : null)
const goalExpectation = computed(() => context.value?.verification === 'current' ? context.value.goalExpectation : null)
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
  if (latest.value?.agentDispatchUncertain && terminalJob(latest.value.status)) return '结果待核实'
  if (active.value && stopState.value === 'requested') return '正在停止'
  return latest.value ? jobStatusLabels[latest.value.status] ?? latest.value.status : ''
})
const historyOpen = shallowRef(true)
const historyDocked = shallowRef(true)
const historyDrawer = shallowRef(false)
const historyPaneId = useId()
const summaryPaneId = useId()
const historyPane = useTemplateRef<HTMLElement>('historyPane')
const summaryPane = useTemplateRef<HTMLElement>('summaryPane')
const historyToggle = useTemplateRef<InstanceType<typeof Button>>('historyToggle')
const summaryToggle = useTemplateRef<InstanceType<typeof Button>>('summaryToggle')
const settingsOpen = shallowRef(false)
const summaryOpen = shallowRef(true)
const summaryDocked = shallowRef(true)
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
const scrollViewport = useTemplateRef<InstanceType<typeof MessageScrollerViewport>>('scrollViewport')
const draftKey = computed(() => spec.value + ':' + (session.value || (handoffJob.value
  ? handoffCli.value ? crossHandoffDraftKey(handoffJob.value, handoffCli.value) : 'handoff:' + handoffJob.value
  : 'new')))
const { draft, getDraft, removeDraft, persistDraft, storageIssue, pending, setPending, rememberAdmitted, admittedDraft, forgetAdmitted } =
  usePiDrafts(draftKey)
const currentExecution = shallowRef<{ jobId: string; label: string; terminal: boolean }>()
const liveExecution = computed(() => currentExecution.value?.jobId === latest.value?.id ? currentExecution.value : undefined)
function showExecutionStatus(jobId: string, label: string, terminal: boolean) {
  if (latest.value?.id === jobId) currentExecution.value = { jobId, label, terminal }
}
const executionLabel = computed(() => {
  if (pending.value) return sending.value ? '正在提交 ' + cliLabel.value + ' 任务…'
    : receiptBusy.value ? '正在核对受理记录…' : '本次请求的受理结果待核实'
  const job = latest.value
  if (!job) return ''
  if (terminalJob(job.status)) return status.value
  if (stopState.value === 'requested') return '正在停止'
  if (liveExecution.value) return liveExecution.value.label
  if (job.piPhase) return agentPhaseLabel(job.piPhase, recordedHost.value)
  return '已受理，正在准备运行'
})
const lockedCli = computed(() => session.value ? recordedHost.value : handoffJob.value ? handoffCli.value ?? 'pi' : undefined)
const targetCli = computed<AgentHost>(() => lockedCli.value ?? pending.value?.input.host ?? draft.value.cli ?? 'pi')
const cliLabel = computed(() => agentHostLabels[targetCli.value])
const grok = computed(() => targetCli.value === 'grok')
const nativeForkSelected = computed(() => !grok.value && !!draft.value.nativeForkDigest && !!draft.value.handoff)
const forkOffer = computed(() => draft.value.handoff?.nativeFork)
const nativeForkReady = computed(() => nativeForkSelected.value && forkOffer.value?.state === 'available' &&
  forkOffer.value.digest === draft.value.nativeForkDigest && !!forkOffer.value.model && !!forkOffer.value.projectIds?.includes(mainId.value))
const queuePerProject = computed(() => !!run.value?.projectAccess?.length)
const selectedProjectIds = computed(() => {
  if (grok.value) return mainId.value ? [mainId.value] : []
  if (nativeForkSelected.value) return forkOffer.value?.projectIds ?? []
  return [...new Set([mainId.value, ...(session.value ? run.value?.projectIds ?? [] : draft.value.selected)])].filter(Boolean)
})
const candidates = computed(() => new Map(projectScope.value?.projects.map((project) => [project.id, project])))
const hostScope = computed(() => {
  if (grok.value || (session.value && run.value?.id !== session.value)) return undefined
  const checkouts = sorted(selectedProjectIds.value).map((id) => [id, projectCheckout(candidates.value.get(id))] as const)
  if (!checkouts.length || checkouts.some(([, checkout]) => !checkout)) return undefined
  return { key: draftKey.value, jobId: session.value ? run.value?.jobId : undefined, checkouts: Object.fromEntries(checkouts) }
})
const hostConfirmation = shallowRef<{ scope: NonNullable<typeof hostScope.value>; explicit: boolean }>()
const hostConfirmationStale = shallowRef(false)
const staleProjectGrants = computed(() => projectScope.value
  && !nativeForkSelected.value && !grok.value ? Object.entries(draft.value.projectGrants)
    .filter(([id, grant]) => selectedProjectIds.value.includes(id) && grant.mode !== 'read' &&
      !contentEqual(grant.checkout, projectCheckout(candidates.value.get(id))))
    .map(([id]) => id)
  : [])
const projectGrantIssue = computed(() => staleProjectGrants.value.length
  ? '工程检出已变化，请重新选择权限或清除过期授权。' : '')
const projectModes = computed<Record<string, ProjectAccessMode>>({
  get: () => {
    if (nativeForkSelected.value || grok.value) return Object.fromEntries(selectedProjectIds.value.map((id) => [id, 'read' as const]))
    const defaultMainMode = commandUnavailable.value ? 'edit' : 'execute'
    return Object.fromEntries(selectedProjectIds.value.filter((id) => !staleProjectGrants.value.includes(id))
      .map((id) => [id, draft.value.projectGrants[id]?.mode ?? (id === mainId.value && !handoffJob.value ? defaultMainMode : 'read')]))
  },
  set: (modes) => {
    if (nativeForkSelected.value || grok.value) return
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
function revokeStaleProjectGrants(key: string, ids: string[], changedId: unknown) {
  const current = getDraft(key)
  const grants = { ...current.projectGrants }
  for (const id of ids) {
    if (typeof changedId !== 'string' || id === changedId) grants[id] = { mode: 'read' }
  }
  current.projectGrants = grants
  persistDraft(key)
  resetCommandExecution()
  void invalidateProjects()
}
function projectScopeChanged(value: unknown) {
  return value === true || value === 'true'
}
const ciIssue = computed(() => {
  if (grok.value || handoffCli.value) return ''
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
const handoffBusy = shallowRef('')
const handoffError = shallowRef('')
let handoffRequest: AbortController | undefined
watch([draftKey, nativeForkSelected, hostScope], ([key, selected, checkout], [previousKey, previousSelected, previous]) => {
  if (key === previousKey && selected && previousSelected && nativeForkReady.value && !handoffBusy.value && previous && !contentEqual(checkout, previous))
    invalidateForkPreview(draft.value, '来源工程检出已变化，请刷新历史摘要并重新选择原生分叉。')
}, { flush: 'sync' })
const recoverableDraft = computed(() => {
  const job = latest.value
  if (!job || !terminalJob(job.status) || job.status === 'completed') return undefined
  const saved = admittedDraft(job.id)
  if (!saved || job.agentDispatchUncertain || (saved.text.crossHandoff ? job.handoffSubmitted :
    job.agentStartedAt && !saved.text.nativeForkDigest && saved.text.cli !== 'grok')) return undefined
  const current = getDraft(saved.key)
  return (!current.prompt.trim() && !current.citations.length) ||
    (current.prompt === saved.text.prompt && contentEqual(current.citations, saved.text.citations)) ? saved : undefined
})
const preparationFailure = computed(() => launchError.value || (recoverableDraft.value
  ? latest.value?.error?.message || '本轮已结束，任务草稿仍可恢复。' : ''))
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
  target.crossHandoff = saved.text.crossHandoff
  target.handoffBinding = undefined
  target.cli = saved.text.cli
  target.selectedModel = saved.text.selectedModel
  target.thinkingLevel = saved.text.thinkingLevel
  target.nativeForkDigest = saved.text.nativeForkDigest
  if (saved.text.nativeForkDigest && (job.error?.context?.nativeForkIssue || projectScopeChanged(job.error?.context?.projectScopeChanged)))
    invalidateForkPreview(target, job.error?.message)
  target.selected = (saved.text.selected ?? []).filter((id) => !scope || scope.projects.some((item) =>
    item.id === id && item.status === 'ready' && item.head))
  target.readOnly = true
  target.sandboxOnly = true
  resetCommandExecution()
  target.projectGrants = Object.fromEntries([mainId.value, ...target.selected].filter(Boolean)
    .map((id) => [id, { mode: 'read' }]))
  target.ciAttachment = undefined
  target.ciRecovery = saved.text.ciRecovery
  target.error = ''
  if ((saved.text.cli === 'grok' || saved.text.crossHandoff) && requiresRuntimeReview(job.error?.context))
    markRuntimeForReview(saved.key, job.error?.message ?? '')
  if (persistDraft(saved.key)) forgetAdmitted(job.id)
  if (saved.key !== draftKey.value && target.crossHandoff) {
    await router.push(handoffLink(target.crossHandoff.source.jobId, target.crossHandoff.targetHost))
    await nextTick()
    focusDraft()
  } else if (saved.key !== draftKey.value && target.handoff) {
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
  return !grok.value && !!selection && (selection.goalVersion !== progression.value?.goalVersion || !availableStages.value.includes(selection.stage))
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
const grokRuntime = shallowRef<GrokRuntimeStatus>()
const runtimeRecheck = shallowRef(0)
const runtimeNeedsReview = computed(() => draft.value.runtimeReview)
const runtimeIdentity = computed(() => grok.value ? grokRuntime.value?.identity : runtimeStatus.value?.identity)
function recheckRuntime() {
  readyModel.value = undefined
  draft.value.runtimeReview = undefined
  draft.value.verifiedGrok = undefined
  draft.value.error = ''
  runtimeRecheck.value++
}
const runtimeReviewIssues = new Set([
  'runtimeChanged', 'identityChanged', 'resumeIdentityChanged', 'executable', 'version', 'configuration',
  'credentials', 'modelAccess', 'modelChanged', 'modelUnavailable', 'thinkingUnavailable', 'effortUnavailable'
])
function requiresRuntimeReview(context?: Record<string, unknown>) {
  return [context?.agentIssue, context?.grokIssue, context?.piIssue].some((code) =>
    typeof code === 'string' && runtimeReviewIssues.has(code))
}
function markRuntimeForReview(key: string, message: string) {
  const target = getDraft(key)
  target.runtimeReview = message || '目标 CLI 配置或模型已变化，请重新检查连接。'
  target.handoffBinding = undefined
  persistDraft(key)
}
const hostReady = computed(() => !!runtimeStatus.value && piCommandCapability(runtimeStatus.value, 'host').state === 'available')
const hostSelectable = computed(() => hostReady.value && !!hostScope.value && !projectBusy.value && !projectError.value)
const defaultHostScope = computed(() => hostSelectable.value && !nativeForkSelected.value && !handoffJob.value &&
  !draft.value.sandboxOnly ? hostScope.value : undefined)
function resetCommandExecution() {
  const scope = defaultHostScope.value
  hostConfirmation.value = scope ? { scope, explicit: false } : undefined
  hostConfirmationStale.value = false
}
watch([draftKey, targetCli], resetCommandExecution, { flush: 'sync' })
watch([hostScope, defaultHostScope], ([scope, defaultScope]) => {
  if (grok.value) return
  const confirmation = hostConfirmation.value
  if (confirmation && confirmation.scope.key === draftKey.value) {
    const previous = confirmation.scope
    // Adding a read-only participant does not change the checkouts already in the default scope.
    const checkoutChanged = !scope || (confirmation.explicit
      ? !contentEqual(scope.checkouts, previous.checkouts)
      : Object.entries(scope.checkouts).some(([id, checkout]) => Object.hasOwn(previous.checkouts, id) &&
        !contentEqual(checkout, previous.checkouts[id])))
    if (checkoutChanged) {
      hostConfirmationStale.value = true
      hostConfirmation.value = undefined
      draft.value.sandboxOnly = true
      return
    }
    if (scope.jobId !== previous.jobId) resetCommandExecution()
    else if (scope !== previous) hostConfirmation.value = { ...confirmation, scope }
  } else if (defaultScope && !hostConfirmationStale.value) {
    hostConfirmation.value = { scope: defaultScope, explicit: false }
  }
}, { immediate: true, flush: 'sync' })
const commandExecution = computed<PiCommandExecution>({
  get: () => !nativeForkSelected.value && !draft.value.sandboxOnly && hostReady.value &&
    hostConfirmation.value && hostConfirmation.value.scope === hostScope.value ? 'host' : 'sandbox',
  set: (value) => {
    if (value === 'host') {
      const scope = hostScope.value
      if (!scope || !hostSelectable.value) return
      hostConfirmation.value = { scope, explicit: true }
    } else hostConfirmation.value = undefined
    draft.value.sandboxOnly = value === 'sandbox'
    hostConfirmationStale.value = false
    draft.value.error = ''
  }
})
const commandCapability = computed(() => runtimeStatus.value && piCommandCapability(runtimeStatus.value, commandExecution.value))
const commandUnavailable = computed(() => commandCapability.value?.state !== 'available')
const commandUnavailableReason = computed(() => commandCapability.value?.reason || (commandExecution.value === 'host'
  ? '本机终端命令尚未就绪。' : '命令隔离尚未通过检测。'))
const legacyHostPending = computed(() => pending.value?.input.commandExecution === 'host' &&
  (pending.value.input.hostExecutionConfirmed !== true || !pending.value.input.expectedRepositoryKeys))
const displayedExecution = computed(() => pending.value ? pending.value.input.commandExecution ?? 'sandbox' : commandExecution.value)
const permissionProjects = computed(() => {
  const submitted = pending.value?.input
  if (submitted?.projectAccess) return submitted.projectAccess.map((grant) => ({
    id: grant.projectId, mode: grant.mode, path: grant.expectedRootPath, head: grant.expectedHead, branch: grant.expectedBranch,
    repositoryKey: submitted.expectedRepositoryKeys?.[grant.projectId] ?? null
  }))
  const ids = submitted?.projectIds ?? selectedProjectIds.value
  return ids.map((id) => {
    const checkout = submitted ? undefined : projectCheckout(candidates.value.get(id))
    const mode = submitted
      ? submitted.readOnly !== false ? 'read' : submitted.allowCommands ? 'execute' : 'edit'
      : projectModes.value[id] ?? 'read'
    return {
      id, mode, path: checkout?.rootPath, head: checkout?.head, branch: checkout?.branch, repositoryKey: checkout?.repositoryKey ?? null
    }
  })
})
const permissionLabel = computed(() => {
  const modes = permissionProjects.value.map((item) => item.mode)
  if (hostConfirmationStale.value && !pending.value) return '本机执行授权待核对'
  if (!modes.length) return '参与工程待核对'
  if (grok.value) return '只读分析'
  if (modes.every((mode) => mode === modes[0])) return modes[0] === 'execute'
    ? displayedExecution.value === 'host' ? '编辑原工程并运行本机终端命令' : '编辑原工程并运行隔离离线命令'
    : modes[0] === 'edit' ? '编辑原工程' : '只读原工程'
  const counts = [
    [modes.filter((mode) => mode === 'read').length, '只读'],
    [modes.filter((mode) => mode === 'edit').length, '可编辑'],
    [modes.filter((mode) => mode === 'execute').length, displayedExecution.value === 'host' ? '可运行本机命令' : '可运行隔离命令']
  ] as const
  return counts.filter(([count]) => count > 0).map(([count, label]) => count + ' 个工程' + label).join(' · ')
})
const selectedModel = computed({
  get: () => nativeForkSelected.value ? forkOffer.value?.model : draft.value.selectedModel,
  set: (model: PiModel | undefined) => {
    if (nativeForkSelected.value) return
    const previous = draft.value.selectedModel
    if (previous?.provider === model?.provider && previous?.id === model?.id) return
    if (runtimeNeedsReview.value || draft.value.verifiedGrok) recheckRuntime()
    readyModel.value = undefined
    runtimeStatus.value = undefined
    grokRuntime.value = undefined
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
const stopRequests = reactive(new Map<string, 'submitting' | 'requested'>())
const stopState = computed(() => stopRequests.get(latest.value?.id ?? ''))
const roundStep = 3
const maxMountedRounds = 9
const showing = shallowRef(roundStep)
const lastVisibleRound = shallowRef<string>()
const grokResumeIssue = computed(() => {
  const error = latest.value?.error
  return grok.value && error?.context?.grokIssue === 'resumeUnavailable' ? error.message : ''
})
const readyForResume = computed(() => !grokResumeIssue.value && direct.value && !runError.value && !latest.value?.agentDispatchUncertain &&
  (run.value?.actions?.resumeNative?.available ?? run.value?.canResume) && run.value &&
  (run.value.jobId === latest.value?.id || (!active.value && !!launchError.value)))
const modelPickerDisabled = computed(() => nativeForkSelected.value || !context.value || (!!session.value && !direct.value))
const modelSelectionLocked = computed(() => active.value || sending.value || !!pending.value || !!handoffBusy.value || (!!session.value && !readyForResume.value))
function selectTarget(host: AgentHost, model?: PiModel) {
  if (modelSelectionLocked.value || (lockedCli.value && host !== lockedCli.value)) return
  if (targetCli.value !== host) {
    readyModel.value = undefined
    runtimeStatus.value = undefined
    grokRuntime.value = undefined
    modelIssue.value = ''
    draft.value.cli = host
    draft.value.selectedModel = undefined
    draft.value.thinkingLevel = undefined
  }
  if (runtimeNeedsReview.value || (host !== 'grok' && draft.value.verifiedGrok)) recheckRuntime()
  selectedModel.value = model
  draft.value.error = ''
}

const recordedModel = computed<PiModel | undefined>(() => {
  const settings = run.value?.hostSettings
  return typeof settings?.provider === 'string' && typeof settings.model === 'string'
    ? { provider: settings.provider, id: settings.model } : undefined
})
const currentModel = computed(() => selectedModel.value ??
  (grok.value && runtimeNeedsReview.value ? draft.value.verifiedGrok?.model : undefined) ?? readyModel.value ?? recordedModel.value)
const thinkingLevels = computed<PiThinkingLevel[]>(() => {
  if (grok.value) return grokRuntime.value?.available ? grokRuntime.value.thinkingLevels ?? [] : []
  return !nativeForkSelected.value && runtimeStatus.value && piRuntimeAvailable(runtimeStatus.value)
    ? runtimeStatus.value.thinkingLevels ?? [] : []
})
const currentThinking = computed(() => {
  const remembered = run.value?.hostSettings?.thinkingLevel as PiThinkingLevel | undefined
  const model = currentModel.value
  const previous = recordedModel.value
  if (draft.value.thinkingLevel && thinkingLevels.value.includes(draft.value.thinkingLevel)) return draft.value.thinkingLevel
  if (remembered && previous && model && piModelKey(previous) === piModelKey(model) && thinkingLevels.value.includes(remembered)) return remembered
  return (grok.value ? grokRuntime.value?.thinkingLevel : runtimeStatus.value?.thinkingLevel) ?? undefined
})
const sendThinking = computed(() => {
  if (nativeForkSelected.value) return undefined
  const level = grok.value || handoffCli.value ? currentThinking.value : draft.value.thinkingLevel
  return level && thinkingLevels.value.includes(level) ? level : undefined
})
const modelLabel = computed(() => {
  const model = currentModel.value
  const level = currentThinking.value
  const thinking = level && thinkingLevels.value.length > 1 ? ' · ' + (piThinkingLabels[level] ?? level) : ''
  return cliLabel.value + (model ? ' · ' + model.provider + ' / ' + model.id + thinking : ' 默认模型')
})
const blocked = computed(() => {
  if (!context.value) return contextError.value || '正在读取 Spec'
  if (!context.value.spec.active) return '此 Spec 已归档'
  if (context.value.sourceError) return context.value.sourceError
  if (!context.value.projects.find((project) => project.id === mainId.value)?.active) return '主工程已停用，请检查工程登记'
  if (active.value) return ''
  if (latest.value?.agentDispatchUncertain) return '此轮结果待核实，原会话暂无法续接或交接。'
  if (session.value && !readyForResume.value) return grokResumeIssue.value || runError.value || run.value?.actions?.resumeNative?.reason || run.value?.resumeIssue ||
    (reading.value ? '正在核对原会话' : '当前会话无法续接，请从原 Spec 新建会话')
  return ''
})
const queueRun = computed(() => !grok.value && direct.value && run.value?.id === session.value ? session.value : '')
const { queue, busy: queueBusy, error: queueError, uncertain: queueUncertain, pendingEnqueue, retryRead: refreshQueue,
  enqueue: enqueueQueueTask, put: putQueueTask, remove: removeQueueTask, resume: resumeQueue } = usePiQueue(queueRun, scheduleRefresh)
const queueVerified = computed(() => context.value?.verification === 'current' &&
  !context.value.sourceError && !contextError.value && !pending.value)
const canEnqueue = computed(() => !!queueRun.value && queueVerified.value && !!queue.value &&
  !queueBusy.value && !queueUncertain.value && !pendingEnqueue.value && !sending.value && !deleteTarget.value && !!draft.value.prompt.trim())
const canEditQueueProjects = computed(() => run.value?.canEditQueuedProjects)
const queuePreparing = computed(() => !!queue.value?.items.some((item) => item.dispatching && item.jobId))
const localTaskUnavailable = computed(() => {
  if (active.value || (run.value && !terminalJob(run.value.status))) return '请等待当前 Pi 轮次结束。'
  if (sending.value || pending.value || pendingEnqueue.value || queuePreparing.value || deleteTarget.value)
    return '请等待当前会话操作完成。'
  if (runError.value || contextError.value) return runError.value || contextError.value
  if (reading.value || !latest.value || run.value?.jobId !== latest.value.id)
    return '正在核对 Pi 轮次。'
  if (linkedRound.value && linkedRound.value !== latest.value.id) return '请返回会话最新轮次。'
  return ''
})
const commandCapabilityIssue = computed(() => {
  if (!commandUnavailable.value) return ''
  const requestsCommands = selectedProjectIds.value.some((id) => projectModes.value[id] === 'execute')
  return requestsCommands ? `${commandUnavailableReason.value} 请在运行设置中调整命令运行方式或工程权限。` : ''
})
const nativeForkScopeIssue = computed(() => nativeForkReady.value && !projectBusy.value &&
  (projectError.value || (selectedProjectIds.value.some((id) => !projectCheckout(candidates.value.get(id)))
    ? '请重新核对分叉涉及的工程检出。' : '')))
const canSend = computed(() => !active.value && !blocked.value && !contextError.value && !verificationIssue.value && !sending.value && !pending.value &&
  (!queueRun.value || (!!queue.value && !queue.value.items.length && !queueBusy.value && !queueUncertain.value && !pendingEnqueue.value)) &&
  !runtimeNeedsReview.value && !crossHandoffIssue.value && !handoffTaskTooLong.value &&
  !checking.value && !projectBusy.value && !hostConfirmationStale.value && !settingsOpen.value && !deleteTarget.value &&
  !reviewState.value.busy && !reviewState.value.pending && !commandCapabilityIssue.value && !handoffBusy.value &&
  (!nativeForkSelected.value || (nativeForkReady.value && !projectBusy.value && !nativeForkScopeIssue.value &&
    contentEqual(readyModel.value, forkOffer.value?.model))) &&
  !!readyModel.value && !!draft.value.prompt.trim())
const canAppendAssistantUi = computed(() => !!session.value && run.value?.id === session.value
  && run.value.jobId === latest.value?.id && !latest.value?.agentDispatchUncertain
  && !blocked.value && !runError.value && !contextError.value && !pending.value && !sending.value)
const roundEnd = computed(() => {
  const index = lastVisibleRound.value ? rounds.value.findIndex((job) => job.id === lastVisibleRound.value) : -1
  return index < 0 ? rounds.value.length : index + 1
})
const roundStart = computed(() => Math.max(0, roundEnd.value - showing.value))
const visibleRounds = computed(() => rounds.value.slice(roundStart.value, roundEnd.value))
const suggestion = computed(() => {
  const { prompt, citations, handoff, ciAttachment, ciRecovery } = draft.value
  if (prompt || citations.length || draft.value.crossHandoff || handoff || ciAttachment || ciRecovery || handoffJob.value || recoverableDraft.value)
    return ''
  if (context.value?.spec.id !== spec.value || blocked.value || contextError.value || verificationIssue.value ||
    active.value || sending.value || pending.value || checking.value || projectBusy.value || settingsOpen.value ||
    deleteTarget.value || reviewOpen.value || handoffBusy.value) return ''
  if (!session.value) return '梳理当前规格'

  const job = latest.value
  if (!job || !readyForResume.value || run.value?.jobId !== job.id || reading.value ||
    (linkedRound.value && linkedRound.value !== job.id) || roundEnd.value < rounds.value.length ||
    (queueRun.value && (!queue.value || queue.value.items.length || queueBusy.value || queueUncertain.value || pendingEnqueue.value))) return ''
  if (job.status === 'failed') return '帮我定位本轮失败原因'
  const hasReview = snapshotJobs.value.has(job.id) ||
    (summary.value?.jobId === job.id && (summary.value.hasSnapshot || summary.value.hasAcceptance))
  return hasReview && (job.status === 'completed' || job.status === 'succeeded') ? '总结本轮成果' : ''
})
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
let focusRoundSummaryOnClose = false
let lastFocusedPanel: 'history' | 'summary' | undefined

function rememberInlinePanelFocus(event: FocusEvent) {
  const target = event.target
  lastFocusedPanel = target instanceof Node && historyPane.value?.contains(target) ? 'history'
    : target instanceof Node && summaryPane.value?.contains(target) ? 'summary' : undefined
}

function returnInlinePanelFocus(panel: 'history' | 'summary') {
  const pane = panel === 'history' ? historyPane.value : summaryPane.value
  const toggle = panel === 'history' ? historyToggle : summaryToggle
  const tookFocus = (element: Element | null) => pane?.contains(element) ||
    (element === document.body && lastFocusedPanel === panel)
  // WebKit and Chromium may drop focus before the breakpoint observer runs.
  if (!tookFocus(document.activeElement)) return
  void nextTick(() => {
    if (!document.hasFocus() || !tookFocus(document.activeElement)) return
    const trigger = toggle.value?.$el
    if (trigger instanceof HTMLElement) trigger.focus({ preventScroll: true })
  })
}

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
  if (!projectBusy.value) void refreshProjects()
}
function toggleHistory(event: MouseEvent) {
  if (compact.value) {
    rememberFocus('history', event.currentTarget)
    historyDrawer.value = true
  } else {
    if (historyOpen.value) returnInlinePanelFocus('history')
    historyOpen.value = !historyOpen.value
  }
}
function toggleSummary() {
  if (inlineSummary.value) {
    if (summaryOpen.value) returnInlinePanelFocus('summary')
    summaryOpen.value = !summaryOpen.value
  }
  else {
    rememberFocus('summary')
    summaryDrawer.value = true
  }
}
function settlePanel(panel: 'history' | 'summary') {
  const docked = panel === 'history' ? historyDocked : summaryDocked
  const open = panel === 'history' ? historyOpen.value : summaryOpen.value
  if (docked.value === open) return
  const update = () => { docked.value = open }
  if (scrollViewport.value) scrollViewport.value.preserveReadingPosition(update)
  else update()
}
function summaryClosed() {
  if (focusRoundSummaryOnClose) {
    focusRoundSummaryOnClose = false
    restore.delete('summary')
    void focusLatestRoundSummary()
  } else if (!reviewOpen.value) returnFocus('summary')
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
async function appendAssistantUi(jobId: string, text: string) {
  const runId = session.value
  const specId = spec.value
  const key = draftKey.value
  const current = () => canAppendAssistantUi.value && latest.value?.id === jobId && draftKey.value === key
  const addition = text.trim()
  if (!addition || !current()) return false
  try {
    const [verified, history] = await Promise.all([getAgentRun(runId), getAgentRounds(runId)])
    if (!current()) return false
    // An accepted request can appear in the rounds list before it is bound to the run.
    const newest = history.rounds.at(-1)
    if (verified.id !== runId || verified.specId !== specId || verified.jobId !== jobId
      || verified.host !== recordedHost.value || verified.recoveryJobId || newest?.id !== jobId
      || newest.agentDispatchUncertain
      || (terminalJob(newest.status) && !(verified.actions?.resumeNative?.available ?? verified.canResume))) {
      scheduleRefresh()
      return false
    }
    const previous = draft.value.prompt
    if (!previous.includes(addition)) {
      draft.value.prompt = previous + (previous && !previous.endsWith('\n') ? '\n' : '') + addition
      persistDraft(key)
    }
    focusDraft()
    return true
  } catch {
    scheduleRefresh()
    return false
  }
}
function fillSuggestion() {
  const prompt = suggestion.value
  if (!prompt) return
  draft.value.prompt = prompt
  focusDraft()
}
function reviewClosed() {
  if (moveToComposer) {
    moveToComposer = false
    focusDraft()
  } else returnFocus('review')
}
function addCitation(excerpt: ReviewExcerpt) {
  if (grok.value || handoffCli.value) return
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
function handoffLink(jobId: string, host?: AgentHost) {
  const target = sessionLink()
  return { ...target, query: { ...target.query, handoff: jobId, handoffCli: host } }
}
function sourceLink(source: AgentContinuationSource) {
  return sessionLink(source.runId, source.jobId)
}
function roundHandoff(job: JobRecord) {
  return run.value?.jobId === job.id ? run.value.handoff : undefined
}
function roundHandoffLink(job: JobRecord) {
  const source = job.agentHandoffSource ?? roundHandoff(job)?.source
  return source ? sourceLink(source) : undefined
}
function continuationPreview(result: AgentContinuation): AgentContinuationPreview {
  const { prompt, ...source } = result
  return { ...source, originalPrompt: prompt }
}
function canHandoff(job: JobRecord) {
  if (recordedHost.value !== 'pi') return false
  if (context.value && !context.value.spec.active) return false
  return roundActions.value[job.id]?.continueSameSpec.available ??
    (terminalJob(job.status) && (run.value?.specId === spec.value ||
      (job.agentRunId === session.value && job.agentSpecId === spec.value)))
}
const crossDialogOpen = shallowRef(false)
const crossDialogSource = shallowRef<AgentContinuationSource>()
const crossDialogTarget = shallowRef<AgentHost>('grok')
let crossDialogTrigger: HTMLElement | undefined
let crossDialogCreated = false
const crossDialogExisting = computed(() => {
  const reference = crossDialogSource.value
  return reference ? getDraft(spec.value + ':' + crossHandoffDraftKey(reference.jobId, crossDialogTarget.value)).crossHandoff : undefined
})
const crossDialogEditing = computed(() => !!draft.value.crossHandoff && handoffCli.value === crossDialogTarget.value)
function crossHandoffSourceIssue(job: JobRecord) {
  if (!context.value?.spec.active || context.value.sourceError || !session.value) return '当前规格不可交接。'
  if (active.value || !latest.value || !terminalJob(latest.value.status) || latest.value.status === 'recoveryRequired' ||
    latest.value.agentDispatchUncertain ||
    run.value?.recoveryJobId || pending.value || sending.value || (run.value && !terminalJob(run.value.status)))
    return '来源会话有未结束或待核实的轮次，请处理后再交接。'
  if (reading.value || runError.value) return '正在核对来源会话，请稍后重试。'
  if (!terminalJob(job.status) || job.status === 'recoveryRequired' || job.agentDispatchUncertain || roundActions.value[job.id]?.viewHistory.available === false)
    return '此轮结束状态或留存资料待核实。'
  return ''
}
const crossDialogSourceIssue = computed(() => {
  if (crossDialogEditing.value) return readyModel.value && runtimeIdentity.value && !runtimeNeedsReview.value
    ? '' : runtimeNeedsReview.value || modelIssue.value || '请先核对目标 CLI 和模型。'
  const job = rounds.value.find((item) => item.id === crossDialogSource.value?.jobId)
  return job ? crossHandoffSourceIssue(job) : ''
})
function crossBinding(
  identity: string | null | undefined, model: PiModel | undefined, level: PiThinkingLevel | undefined,
  permissions: typeof permissionProjects.value,
  execution?: PiCommandExecution
) {
  return model && identity
    ? JSON.stringify([identity, piModelKey(model), level ?? null, permissions, execution ?? null])
    : undefined
}
const crossHandoffBinding = computed(() => {
  const permissions = permissionProjects.value
  return crossBinding(runtimeIdentity.value, readyModel.value, sendThinking.value, permissions,
    permissions.some(({ mode }) => mode === 'execute') ? displayedExecution.value : undefined)
})
const handoffTaskTooLong = computed(() => {
  const transfer = draft.value.crossHandoff
  return !!transfer && new TextEncoder().encode(transfer.context + draft.value.prompt).length > 128 * 1024
})
const crossHandoffIssue = computed(() => {
  if (!handoffCli.value) return ''
  if (!draft.value.crossHandoff) return '此交接草稿已使用或不在此标签页，请从来源轮次重新打开。'
  return !draft.value.handoffBinding || draft.value.handoffBinding !== crossHandoffBinding.value
    ? '请复核交接来源、目标设置和本轮授权。' : ''
})
const crossDialogDestination = computed(() => crossDialogEditing.value ? {
  identity: runtimeIdentity.value, model: readyModel.value, level: sendThinking.value,
  status: [grok.value ? 'YSS 专用配置' : runtimeStatus.value?.configDir,
    grok.value ? grokRuntime.value?.version : runtimeStatus.value?.version, modelIssue.value].filter(Boolean).join(' · ')
} : undefined)
function openCrossHandoff(event: MouseEvent, job?: JobRecord) {
  const transfer = draft.value.crossHandoff
  if (job ? crossHandoffSourceIssue(job) : !transfer) return
  crossDialogTrigger = event.currentTarget instanceof HTMLElement ? event.currentTarget : undefined
  crossDialogCreated = false
  crossDialogSource.value = job ? { runId: session.value, jobId: job.id } : transfer?.source
  crossDialogTarget.value = job ? recordedHost.value === 'pi' ? 'grok' : 'pi' : transfer!.targetHost
  crossDialogOpen.value = true
}
async function confirmCrossHandoff(
  transfer: AgentHandoffPreview, destination: { identity?: string | null; model?: PiModel; level?: PiThinkingLevel }
) {
  if (crossDialogSourceIssue.value) return
  const key = spec.value + ':' + crossHandoffDraftKey(transfer.source.jobId, transfer.targetHost)
  const target = getDraft(key)
  const existing = !!target.crossHandoff
  target.crossHandoff = transfer
  target.cli = transfer.targetHost
  target.error = ''
  if (key === draftKey.value) target.handoffBinding = crossHandoffBinding.value
  else if (!existing) {
    const checkout = projectCheckout(candidates.value.get(mainId.value))
    target.handoffBinding = checkout ? crossBinding(destination.identity, destination.model, destination.level, [{
      id: mainId.value, mode: 'read', path: checkout.rootPath, head: checkout.head, branch: checkout.branch,
      repositoryKey: checkout.repositoryKey ?? null
    }]) : undefined
  } else target.handoffBinding = undefined
  persistDraft(key)
  crossDialogCreated = key !== draftKey.value
  if (crossDialogCreated) {
    historyDrawer.value = false
    summaryDrawer.value = false
    await router.push(handoffLink(transfer.source.jobId, transfer.targetHost))
    await nextTick()
    focusDraft()
  }
}
function closeCrossHandoff(event: Event) {
  event.preventDefault()
  if (!crossDialogCreated && crossDialogTrigger?.isConnected) crossDialogTrigger.focus()
  crossDialogTrigger = undefined
}
function invalidateForkPreview(target: ReturnType<typeof getDraft>, message?: string) {
  if (!target.handoff || !target.nativeForkDigest) return
  target.handoff = { ...target.handoff, nativeFork: { ...target.handoff.nativeFork,
    state: 'blocked', message: message || '请刷新历史摘要并重新核对原生分叉。' } }
}
function selectNativeFork(selected: boolean) {
  const offer = forkOffer.value
  if (selected && (offer?.state !== 'available' || !offer.digest || !offer.model || !offer.projectIds?.includes(mainId.value))) return
  draft.value.nativeForkDigest = selected ? offer?.digest : undefined
  draft.value.error = ''
  resetCommandExecution()
  persistDraft(draftKey.value)
}
async function createHandoff(job: JobRecord) {
  const runId = session.value
  const specId = spec.value
  if (!runId || !canHandoff(job) || handoffBusy.value) return
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
    if (result.runId !== runId || result.jobId !== job.id) throw new Error('交接摘要与所选来源轮次不一致。')
    const key = specId + ':handoff:' + result.jobId
    const target = getDraft(key)
    if (!target.handoff) {
      target.selected = result.projectIds.filter((id) => id !== mainId.value && (!scope || scope.projects.some((candidate) =>
        candidate.id === id && candidate.status === 'ready' && candidate.head)))
      target.prompt ||= result.prompt
    }
    target.handoff = continuationPreview(result)
    persistDraft(key)
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
async function refreshHandoff() {
  const target = draft.value
  if (!target.handoff || handoffBusy.value) return
  const key = draftKey.value
  const source = target.handoff
  const request = new AbortController()
  handoffRequest = request
  handoffBusy.value = source.jobId
  handoffError.value = ''
  try {
    const [result] = await Promise.all([
      getAgentContinuation(spec.value, source, request.signal),
      refreshProjects()
    ])
    if (alive && !request.signal.aborted && key === draftKey.value) {
      if (result.runId !== source.runId || result.jobId !== source.jobId) throw new Error('交接摘要与所选来源轮次不一致。')
      target.handoff = continuationPreview(result)
      target.error = ''
      persistDraft(key)
    }
  } catch (cause) {
    if (alive && !request.signal.aborted && key === draftKey.value) {
      handoffError.value = errorMessage(cause)
      invalidateForkPreview(target, handoffError.value)
    }
  } finally {
    if (handoffRequest === request) {
      handoffRequest = undefined
      handoffBusy.value = ''
    }
  }
}
async function confirmCrossHandoffAtSend(transfer: AgentHandoffPreview, key: string, input: AgentStart) {
  const { host, expectedRuntimeIdentity, expectedModel, thinkingLevel, projectAccess, expectedRepositoryKeys, commandExecution } = input
  if (!host || !expectedRuntimeIdentity || !expectedModel || !projectAccess) return
  const request = new AbortController()
  handoffRequest = request
  handoffBusy.value = transfer.source.jobId
  const current = getDraft(key)
  const stillCurrent = () => alive && !request.signal.aborted && key === draftKey.value
  try {
    const { confirmation } = await confirmAgentHandoff(input.specId, {
      host, expectedRuntimeIdentity, expectedModel, thinkingLevel, projectAccess, expectedRepositoryKeys, commandExecution,
      source: transfer.source, digest: transfer.digest, context: transfer.context
    }, request.signal)
    if (stillCurrent() && confirmation) return confirmation
  } catch (cause) {
    if (!stillCurrent()) return
    current.error = errorMessage(cause)
    if (cause instanceof ApiError && cause.status >= 400 && cause.status < 500) {
      current.handoffBinding = undefined
      persistDraft(key)
    }
  } finally {
    if (handoffRequest === request) {
      handoffRequest = undefined
      handoffBusy.value = ''
    }
  }
}
async function applyHandoffDraft(text: string) {
  draft.value.prompt = text
  persistDraft(draftKey.value)
  await nextTick()
  focusDraft()
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
function jumpToRoundSummary() {
  if (!latest.value) return
  if (summaryDrawer.value) {
    focusRoundSummaryOnClose = true
    summaryDrawer.value = false
  } else void focusLatestRoundSummary()
}
async function focusLatestRoundSummary() {
  const scope = draftKey.value
  lastVisibleRound.value = undefined
  focusActivity.value = undefined
  if (linkedRound.value) await router.replace(sessionLink(session.value))
  await nextTick()
  if (!alive || scope !== draftKey.value) return
  const trigger = document.getElementById('pi-turn-' + latest.value?.id)
    ?.querySelector<HTMLElement>('[data-pi-round-summary-trigger]')
  if (trigger) focusNode(trigger)
  else focusTranscript()
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
  if (isCompact) returnInlinePanelFocus('history')
  else historyDrawer.value = false
  historyDocked.value = historyOpen.value
})
watch(inlineSummary, (isInline) => {
  if (!isInline) returnInlinePanelFocus('summary')
  summaryDocked.value = summaryOpen.value
})
watch([context, session], ([workspace, id]) => {
  if (!workspace || workspace.spec.id !== spec.value) return
  if (workspace.spec.active) lastAgentSpec.value = spec.value
  if (id || route.query.new || pending.value || search.value || attentionOnly.value) return
  const first = workspace.sessions[0]
  if (first) void router.replace(sessionLink(first.id))
}, { immediate: true })
watch([spec, session], () => {
  crossDialogOpen.value = false
  showing.value = roundStep
  lastVisibleRound.value = undefined
  readyModel.value = undefined
  runtimeStatus.value = undefined
  grokRuntime.value = undefined
  modelIssue.value = ''
  settingsOpen.value = false
  reviewOpen.value = false
  focusActivity.value = undefined
  focusRoundSummaryOnClose = false
})
watch(draftKey, () => {
  crossDialogOpen.value = false
  handoffRequest?.abort()
  handoffRequest = undefined
  handoffBusy.value = ''
  handoffError.value = ''
  if (nativeForkSelected.value) void refreshHandoff()
}, { immediate: true })
watch([() => context.value?.spec.id, () => context.value?.spec.active, spec], ([id, enabled, target]) => {
  if (id === target && enabled && !projectScope.value && !projectBusy.value) void refreshProjects()
}, { immediate: true })
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
const runtimeResumeSource = computed(() => {
  const resumable = readyForResume.value
  if (!grok.value) return resumable
  return resumable ? JSON.stringify([run.value?.jobId, latest.value?.id]) : undefined
})
const activeGrokSpec = computed(() => context.value?.spec.active ? context.value.spec.id : undefined)
const piSettingsOpen = computed(() => !grok.value && settingsOpen.value)
watch([() => grok.value ? activeGrokSpec.value : context.value?.spec.id,
  session, runtimeResumeSource, selectedModel, piSettingsOpen, targetCli, runtimeRecheck, draftKey],
  ([source, id, resumable, selected, open, host, recheck, key], previous, onCleanup) => {
    checking.value = false
    if (!source || !context.value?.spec.active || (id && !resumable) || (open && host === 'pi')) return
    if (host === 'pi' && previous?.[4] && previous[5] === host && previous[6] === recheck && previous[7] === key &&
      (readyModel.value || modelIssue.value)) return
    const controller = new AbortController()
    onCleanup(() => controller.abort())
    const current = getDraft(key)
    const stillCurrent = () => !controller.signal.aborted && draftKey.value === key &&
      selectedModel.value === selected && targetCli.value === host && runtimeRecheck.value === recheck &&
      (host !== 'grok' || (activeGrokSpec.value === source && runtimeResumeSource.value === resumable))
    const explicitRecheck = previous?.[7] === key && previous[6] !== recheck
    checking.value = true
    readyModel.value = undefined
    runtimeStatus.value = undefined
    grokRuntime.value = undefined
    modelIssue.value = ''
    const target = { resumeRunId: id || undefined, selectedModel: selected }
    const probe = host === 'grok' ? getGrokRuntime(target, controller.signal).then((result) => {
      if (!stillCurrent()) return
      grokRuntime.value = result
      const { available, identity, model } = result
      if (!available || !identity || !model) {
        modelIssue.value = [grokRuntimeLabel(result), result.message,
          result.issue === 'resumeIdentityChanged' && !result.message ? '切回原会话账号后重新检查连接。' : undefined
        ].filter(Boolean).join(' · ')
        return
      }
      const previous = current.verifiedGrok
      if (previous?.identity && (previous.identity !== identity || !contentEqual(previous.model, model))) {
        markRuntimeForReview(key, 'Grok Build 的运行配置或默认模型已变化，请重新检查连接。')
      } else if (current.thinkingLevel && !result.thinkingLevels?.includes(current.thinkingLevel)) {
        if (explicitRecheck) current.thinkingLevel = undefined
        else markRuntimeForReview(key, '所选推理档位已失效，请重新检查连接。')
      }
      if (!current.runtimeReview) current.verifiedGrok = { identity, model }
      readyModel.value = model
    }) : getPiRuntime(target, controller.signal).then((result) => {
      if (!stillCurrent()) return
      runtimeStatus.value = result
      const model = piRuntimeAvailable(result) ? result.model ?? undefined : undefined
      modelIssue.value = model ? modelRestrictions.value[piModelKey(model)] ?? '' : result.capabilities?.pi.reason || result.message || 'Pi 模型未就绪'
      readyModel.value = modelIssue.value ? undefined : model
    })
    void probe.catch((cause) => {
      if (stillCurrent()) modelIssue.value = errorMessage(cause)
    }).finally(() => {
      if (stillCurrent()) checking.value = false
    })
  }, { immediate: true })
watch(rounds, (items) => {
  for (const job of items) if (terminalJob(job.status)) stopRequests.delete(job.id)
})
let staleScopeJob = ''
watch(latest, (job) => {
  const error = job?.error?.context
  if (!job || job.id === staleScopeJob || job.handoffSubmitted || !projectScopeChanged(error?.projectScopeChanged)) return
  const saved = admittedDraft(job.id)
  if (!saved || (job.agentStartedAt && !saved.text.crossHandoff)) return
  staleScopeJob = job.id
  if (saved.text.nativeForkDigest) void invalidateProjects()
  else revokeStaleProjectGrants(saved.key, [mainId.value, ...(saved.text.selected ?? [])].filter(Boolean), error?.projectId)
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
    (!item.input.host || job.agentHost === item.input.host) &&
    (!item.input.resumeRunId || !job.agentRunId || job.agentRunId === item.input.resumeRunId)
}
async function settlePending(item: PendingPiRequest, job: JobRecord) {
  if (!matchingReceipt(job, item)) throw new Error('受理记录与原请求不一致，请重新核对。')
  performance.mark('yss.pi.request.admitted', { detail: { requestId: item.id, jobId: job.id } })
  const source = item.input.specId
  const next = job.agentRunId || item.input.resumeRunId || job.id
  const current = getDraft(item.key)
  const fork = item.input.nativeForkSource
  const sourceRound = fork ?? item.input.continuationSource
  const excerpts = item.excerpts ?? []
  const cross = item.input.handoff
  const started = cross ? !!job.handoffSubmitted :
    job.status === 'completed' || (!!job.agentStartedAt && !fork && item.input.host !== 'grok')
  const ci = item.input.ciJob
  const expectedCi = ci ? { projectId: ci.projectId, linkId: ci.linkId, jobId: ci.jobId, mode: ci.mode } : undefined
  const expectedSelected = (item.input.projectAccess?.map((entry) => entry.projectId) ?? item.input.projectIds)
    ?.filter((id) => id !== mainId.value)
  const sameProjectGrants = Object.entries(current.projectGrants).every(([projectId, grant]) => {
    const submitted = item.input.projectAccess?.find((entry) => entry.projectId === projectId)
    const keys = item.input.expectedRepositoryKeys
    return submitted?.mode === grant.mode && (grant.mode === 'read' || (submitted.expectedHead === grant.checkout.head &&
      submitted.expectedRootPath === grant.checkout.rootPath && submitted.expectedBranch === grant.checkout.branch &&
      (!keys || keys[projectId] === (grant.checkout.repositoryKey ?? null))))
  })
  const sameStage = current.stageSelection === undefined || (current.stageSelection?.stage === item.input.lifecycleStage &&
    current.stageSelection?.goalVersion === item.input.expectedGoalVersion)
  const matchesSettings = fork ? current.nativeForkDigest === fork.expectedForkDigest : item.input.host === 'grok'
    ? current.cli === undefined || current.cli === 'grok' : contentEqual(current.citations, excerpts) && contentEqual(ciSelection(current), expectedCi) &&
    (item.input.resumeRunId || contentEqual(sorted(current.selected), sorted(expectedSelected ?? []))) &&
    sameProjectGrants && sameStage && (!current.selectedModel || contentEqual(current.selectedModel, item.input.selectedModel))
  const submittedDraftMatches = current.prompt === item.input.prompt && matchesSettings &&
    current.handoff?.jobId === sourceRound?.jobId &&
    current.handoff?.runId === sourceRound?.runId &&
    (!item.input.expectedContinuationDigest || current.handoff?.summaryDigest === item.input.expectedContinuationDigest) &&
    (!cross || (current.crossHandoff?.digest === cross.digest && current.crossHandoff.context === cross.context))
  const saved = started || rememberAdmitted(job.id, item.key, {
    prompt: item.input.prompt, citations: fork || (item.input.host === 'grok' && !cross) ? current.citations : excerpts,
    handoff: current.handoff, selected: fork || (item.input.host === 'grok' && !cross) ? current.selected : expectedSelected,
    ciRecovery: fork || (item.input.host === 'grok' && !cross) ? ciSelection(current) : expectedCi,
    nativeForkDigest: fork?.expectedForkDigest, crossHandoff: current.crossHandoff,
    handoffBinding: current.handoffBinding, cli: item.input.host, selectedModel: item.input.selectedModel,
    thinkingLevel: item.input.thinkingLevel
  })
  if (saved && submittedDraftMatches) {
    current.prompt = ''
    current.citations = []
    current.ciAttachment = undefined
    current.ciRecovery = undefined
    current.handoff = undefined
    current.crossHandoff = undefined
    current.handoffBinding = undefined
    current.nativeForkDigest = undefined
    if (!item.input.resumeRunId) {
      current.selected = []
      current.cli = undefined
      current.selectedModel = undefined
      current.thinkingLevel = undefined
      current.verifiedGrok = undefined
      current.runtimeReview = undefined
    }
    current.readOnly = true
    current.sandboxOnly = false
    current.projectGrants = {}
    current.stageSelection = null
    current.error = ''
    persistDraft(item.key)
  }
  setPending(item.key)
  resolvePendingRequest(item.id)
  recordAccepted(source, next, job)
  if (draftKey.value === item.key) {
    resetCommandExecution()
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
      if (legacyHostPending.value) {
        const current = getDraft(item.key)
        current.sandboxOnly = true
        if (!current.prompt.trim() && !current.citations.length) {
          current.prompt = item.input.prompt
          current.citations = item.excerpts ?? []
        }
        if (!persistDraft(item.key) || !setPending(item.key)) {
          receiptError.value = '无法保存草稿或清除原请求，草稿仍保留在此页面。'
          return
        }
        resolvePendingRequest(item.id)
        resetCommandExecution()
        current.error = '未找到原请求的受理记录。草稿已保留，请核对本轮权限后重新发送。'
        return
      }
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
const runtimeFailures = new Set<string>()
watch(latest, (job) => {
  if (!job) return
  const saved = admittedDraft(job.id)
  if (!saved) return
  if (saved.key === draftKey.value && saved.text.cli === 'grok' && terminalJob(job.status) &&
    !job.agentDispatchUncertain && !runtimeFailures.has(job.id) && requiresRuntimeReview(job.error?.context)) {
    runtimeFailures.add(job.id)
    markRuntimeForReview(saved.key, job.error?.message ?? '')
  }
  const submitted = saved.text.crossHandoff ? job.handoffSubmitted :
    job.status === 'completed' || (job.agentStartedAt && !saved.text.nativeForkDigest && saved.text.cli !== 'grok')
  if (submitted) forgetAdmitted(job.id)
})
let measuredResult = ''
watch([latest, snapshotJobs, submittedRound], ([job, snapshots, submitted]) => {
  if (!job || job.id !== submitted || job.id === measuredResult || !terminalJob(job.status) || !snapshots.has(job.id)) return
  measuredResult = job.id
  performance.mark('yss.pi.result.ready', { detail: { requestId: job.requestId, jobId: job.id } })
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
    const scopeChanged = projectScopeChanged(cause.context.projectScopeChanged)
    if (item.input.nativeForkSource && (cause.context.nativeForkIssue || scopeChanged)) {
      invalidateForkPreview(current, current.error)
      persistDraft(item.key)
    }
    if (draftKey.value === item.key && scopeChanged) {
      if (item.input.nativeForkSource) void invalidateProjects()
      else {
        revokeStaleProjectGrants(item.key, item.input.projectAccess?.map((grant) => grant.projectId) ?? [], cause.context.projectId)
        hostConfirmationStale.value = item.input.commandExecution === 'host'
        if (hostConfirmationStale.value) current.sandboxOnly = true
        openSettings()
      }
    }
    if (item.input.handoff) {
      current.handoffBinding = undefined
      persistDraft(item.key)
    }
    if ((item.input.host === 'grok' || item.input.handoff) && requiresRuntimeReview(cause.context))
      markRuntimeForReview(item.key, current.error)
    if (draftKey.value === item.key && (cause.context.piIssue || cause.context.grokIssue || cause.context.agentIssue ||
      item.input.host === 'grok')) {
      readyModel.value = undefined
      modelIssue.value = current.error
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
  if (legacyHostPending.value) void checkPending()
  else if (item) void dispatch(item)
}
async function send() {
  if (!canSend.value || !readyModel.value) return
  const clickedAt = performance.now()
  const current = draft.value
  const native = nativeForkSelected.value
  const key = draftKey.value
  const id = session.value
  const execution = commandExecution.value
  if (!projectScope.value) {
    current.error = projectError.value || '尚未核对参与工程，请重试。'
    openSettings()
    return
  }
  if (projectGrantIssue.value) {
    current.error = projectGrantIssue.value
    openSettings()
    return
  }
  const projectAccess: AgentProjectAccess[] = []
  const repositoryKeys: [string, string | null][] = []
  for (const projectId of selectedProjectIds.value) {
    const candidate = candidates.value.get(projectId)
    const checkout = projectCheckout(candidate)
    if (!checkout) {
      current.error = '参与工程 ' + (candidate?.name ?? projectId) + ' 当前不可用，请检查运行范围。'
      openSettings()
      return
    }
    projectAccess.push({
      projectId, mode: projectModes.value[projectId] ?? 'read',
      expectedHead: checkout.head, expectedRootPath: checkout.rootPath, expectedBranch: checkout.branch
    })
    repositoryKeys.push([projectId, checkout.repositoryKey ?? null])
  }
  if (!native && stageNeedsReview.value) {
    current.error = '目标版本或适用阶段已变化，请重新选择本轮跟进阶段。'
    openSettings()
    return
  }
  if (!native && ciIssue.value) {
    current.error = ciIssue.value
    openSettings()
    return
  }
  if (handoffJob.value && !current.handoff && !current.crossHandoff) {
    current.error = '此接续草稿已用完或不在此标签页，请从来源轮次重新生成。'
    return
  }
  const transfer = current.crossHandoff
  const attachment = native || grok.value || transfer ? undefined : current.ciAttachment
  const handoff = current.handoff ? { runId: current.handoff.runId, jobId: current.handoff.jobId } : undefined
  const excerpts = native || grok.value || transfer ? [] : current.citations
  const requestsCommands = projectAccess.some((entry) => entry.mode === 'execute')
  const item: PendingPiRequest = {
    id: requestId(),
    key,
    input: {
      specId: spec.value,
      host: grok.value || transfer ? targetCli.value : undefined,
      expectedRuntimeIdentity: grok.value || transfer ? runtimeIdentity.value ?? undefined : undefined,
      handoff: transfer ? { source: transfer.source, digest: transfer.digest, context: transfer.context } : undefined,
      projectAccess,
      expectedRepositoryKeys: Object.fromEntries(repositoryKeys),
      commandExecution: requestsCommands ? execution : undefined,
      hostExecutionConfirmed: requestsCommands && execution === 'host' ? true : undefined,
      continuationSource: native ? undefined : handoff,
      expectedContinuationDigest: native ? undefined : current.handoff?.summaryDigest,
      nativeForkSource: native && handoff && current.nativeForkDigest ? { ...handoff, expectedForkDigest: current.nativeForkDigest } : undefined,
      readOnly: native || grok.value ? true : undefined,
      ciJob: attachment ? {
        projectId: attachment.job.projectId, linkId: attachment.job.linkId, jobId: attachment.job.jobId,
        mode: attachment.mode, expectedSha: attachment.job.sha
      } : undefined,
      resumeRunId: id || undefined, expectedJobId: id ? run.value?.jobId : undefined,
      selectedModel: selectedModel.value ? { ...selectedModel.value } : undefined, expectedModel: { ...readyModel.value },
      thinkingLevel: sendThinking.value,
      prompt: current.prompt,
      lifecycleStage: native || grok.value ? undefined : current.stageSelection?.stage,
      expectedGoalVersion: native || grok.value ? undefined : current.stageSelection?.goalVersion,
      goalExpectation: native || grok.value ? undefined : goalExpectation.value ?? undefined,
      citations: excerpts.map(({ reviewToken, projectId, path, side, startLine, endLine, note }) =>
        ({ reviewToken, projectId, path, side, startLine, endLine, note }))
    },
    excerpts: excerpts.map((excerpt) => ({ ...excerpt }))
  }
  if (transfer && item.input.handoff) {
    const binding = current.handoffBinding
    const confirmation = await confirmCrossHandoffAtSend(transfer, key, item.input)
    if (!confirmation || !canSend.value || key !== draftKey.value || current.prompt !== item.input.prompt ||
      current.handoffBinding !== binding || current.crossHandoff !== transfer) return
    item.input.handoff.confirmation = confirmation
  }
  persistDraft(key)
  if (!setPending(key, item)) {
    current.error = '此标签页无法保存本次请求，任务尚未发送。'
    return
  }
  performance.mark('yss.pi.request.clicked', { startTime: clickedAt, detail: { requestId: item.id } })
  await dispatch(item)
}

function queueInput(current: typeof draft.value): PiEnqueueInput {
  return {
    prompt: current.prompt, readOnly: current.readOnly,
    expectedGoalVersion: goalExpectation.value?.version ?? progression.value?.goalVersion,
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
    current.readOnly = true
    current.sandboxOnly = false
    resetCommandExecution()
    current.projectGrants = {}
    persistDraft(key)
  }
}

async function stop() {
  const job = latest.value
  if (!job || !active.value || stopRequests.has(job.id)) return
  const current = draft.value
  stopRequests.set(job.id, 'submitting')
  try {
    await cancelJob(job.id)
    if (alive && stopRequests.has(job.id)) stopRequests.set(job.id, 'requested')
  } catch (cause) {
    if (alive) {
      stopRequests.delete(job.id)
      current.error = errorMessage(cause)
    }
  } finally {
    if (alive) scheduleRefresh()
  }
}
function onEnter(event: KeyboardEvent) {
  if (event.key !== 'Enter' || event.isComposing || event.keyCode === 229 || event.shiftKey ||
    event.ctrlKey || event.altKey || event.metaKey) return
  if (event.repeat) {
    event.preventDefault()
    return
  }
  if (!canSend.value) return
  event.preventDefault()
  void send()
}
</script>

<template>
  <div class="pi-workspace" :data-history="historyDocked" :data-summary="summaryDocked && inlineSummary" @focusin="rememberInlinePanelFocus">
    <Transition name="pi-panel" @after-enter="settlePanel('history')" @after-leave="settlePanel('history')">
      <aside v-if="!compact" v-show="historyOpen" :id="historyPaneId" ref="historyPane" class="pi-history-pane" :inert="!historyOpen">
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
          <Button ref="historyToggle" type="button" variant="ghost" size="icon" aria-label="会话历史"
            :aria-expanded="compact ? historyDrawer : historyOpen" :aria-controls="compact ? undefined : historyPaneId"
            :aria-haspopup="compact ? 'dialog' : undefined" @click="toggleHistory">
            <PanelLeft :size="18" aria-hidden="true" />
          </Button>
        </ControlTooltip>
        <Folder :size="16" class="pi-heading-icon" />
        <h1 :title="title">{{ title }}</h1>
        <span v-if="session" class="shrink-0 text-xs text-muted-foreground">{{ agentHostLabels[recordedHost] }}</span>
        <ControlTooltip v-if="terminalOpen" label="终端">
          <Button variant="ghost" size="icon" as-child>
            <a :href="terminalOpen" aria-label="终端" draggable="false">
              <Terminal :size="18" aria-hidden="true" />
            </a>
          </Button>
        </ControlTooltip>
        <ControlTooltip label="会话摘要">
          <Button ref="summaryToggle" type="button" variant="ghost" size="icon" aria-label="会话摘要" class="pi-summary-toggle"
            :aria-expanded="inlineSummary ? summaryOpen : summaryDrawer" :aria-controls="inlineSummary ? summaryPaneId : undefined"
            :aria-haspopup="inlineSummary ? undefined : 'dialog'" @click="toggleSummary">
            <ListChecks :size="18" aria-hidden="true" />
          </Button>
        </ControlTooltip>
      </header>
      <PiExecutionStatus :label="executionLabel" :started-at="active && !liveExecution?.terminal ? latest?.createdAt : undefined" />
      <div v-if="contextError || (context && blocked && !active)" class="pi-top-note" role="status">
        {{ contextError || blocked }}
        <RouterLink v-if="run?.recoveryJobId && run.recoveryRunId !== session" :to="'/jobs/' + run.recoveryJobId">
          查看待核实的接收轮次
        </RouterLink>
        <Button v-else-if="run?.recoveryJobId && summary?.hasAcceptance" type="button" variant="ghost" size="sm" @click="openReview()">
          查看待核实的接收记录
        </Button>
        <Button v-if="contextError || (session && blocked)" type="button" variant="ghost" size="sm" @click="refresh">刷新</Button>
        <Button v-if="session && latest && canHandoff(latest) && !readyForResume && !run?.recoveryJobId &&
          context?.spec.active && !context.sourceError && !contextError"
          type="button" variant="outline" size="sm" :disabled="!!handoffBusy" @click="createHandoff(latest)">
          带历史摘要新建
        </Button>
        <Button v-if="session && context && !context.sourceError && context.spec.active" type="button" variant="ghost" size="sm" @click="newSession">新会话</Button>
      </div>
      <div v-if="verificationIssue && !active" class="pi-top-note" role="status">
        {{ verificationIssue }}
        <Button v-if="context?.verification === 'failed'" type="button" variant="ghost" size="sm" @click="refresh">重新读取</Button>
      </div>
      <div v-if="preparationFailure" class="pi-top-note" role="status">
        {{ preparationFailure }}
        <Button v-if="recoverableDraft" type="button" variant="ghost" size="sm" :disabled="projectBusy" @click="restoreSubmittedDraft">
          <LoadingIndicator :active="projectBusy" />恢复任务草稿
        </Button>
        <RouterLink v-if="latest" :to="{ path: '/jobs/' + latest.id, query: { detail: '1' } }">查看执行记录</RouterLink>
      </div>
      <div v-if="pending && !sending && !receiptBusy" class="pi-top-note">
        <span v-if="pending.input.allowCommands || pending.input.projectAccess?.some((item) => item.mode === 'execute')">
          原请求允许编辑工程并{{ piCommandPermissionLabels[pending.input.commandExecution ?? 'sandbox'] }}。
        </span>
        <span v-else-if="pending.input.readOnly === false || pending.input.projectAccess?.some((item) => item.mode === 'edit')">原请求允许编辑原工程。</span>
        <Button type="button" variant="ghost" size="sm" :disabled="sending || receiptBusy" @click="checkPending">核对回执</Button>
        <Button v-if="!legacyHostPending" type="button" variant="outline" size="sm" :disabled="sending || receiptBusy"
          @click="retryPending">按同一请求重试</Button>
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
            <MessageScrollerViewport ref="scrollViewport" aria-label="Agent 会话对话" class="pi-viewport" :preserve-scroll-on-prepend="true">
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
                  <PiRound v-if="roundActions[job.id]?.viewHistory.available !== false"
                    :job="job" :host="agentRoundHost(job, recordedHost)" :direct="direct" :latest="job.id === latest?.id" :stopping="stopRequests.get(job.id) === 'requested'"
                    :handoff="roundHandoff(job)"
                    :handoff-submitted="job.handoffSubmitted"
                    :handoff-source-link="roundHandoffLink(job)"
                    :track-first-text="job.id === submittedRound"
                    :focus-activity="focusActivity?.jobId === job.id ? focusActivity : undefined"
                    :can-review="snapshotJobs.has(job.id)" :can-review-latest="!!latest && snapshotJobs.has(latest.id)"
                    :project-names="projectNames" :read-only="run?.jobId === job.id ? run?.readOnly : undefined"
                    :command-execution="run?.jobId === job.id ? run?.commandExecution : undefined"
                    :append-assistant-ui="canAppendAssistantUi ? appendAssistantUi : undefined"
                    :review-targets="job.id === summary?.jobId ? summary.reviewTargets : undefined"
                    @updated="scheduleRefresh" @execution-status="showExecutionStatus" @focus="focusNode" @review="(project, path) => openReview(job.id, project, path)"
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
                  <p v-if="roundActions[job.id]?.viewHistory.available === false" class="pi-round-note">
                    {{ roundActions[job.id]?.viewHistory.reason }}
                  </p>
                  <Button v-if="canHandoff(job)"
                    type="button" variant="ghost" size="sm" :disabled="!!handoffBusy" @click="createHandoff(job)">
                    <LoadingIndicator :active="handoffBusy === job.id" />{{ handoffBusy === job.id ? '正在读取历史摘要…' : '带历史摘要新建' }}
                  </Button>
                  <Button v-if="terminalJob(job.status) && context?.spec.active"
                    type="button" variant="ghost" size="sm" :aria-disabled="!!crossHandoffSourceIssue(job)"
                    :title="crossHandoffSourceIssue(job) || undefined" @click="openCrossHandoff($event, job)">
                    交接给其他 CLI
                  </Button>
                  <p v-if="terminalJob(job.status) && context?.spec.active && crossHandoffSourceIssue(job)"
                    class="pi-round-note">{{ crossHandoffSourceIssue(job) }}</p>
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
            {{ legacyHostPending ? '核对回执' : '按原请求重试' }}
          </Button>
        </div>
        <div v-if="queueRun || summary?.hasSnapshot || summary?.hasAcceptance" class="pi-composer-actions">
          <Button v-if="summary?.hasSnapshot || summary?.hasAcceptance" type="button" variant="outline"
            class="pi-diff-shortcut" @click="openReview()"><FileDiff :size="15" />
            {{ summary.pendingFiles ? summary.pendingFiles + ' 个文件待审阅' : '查看执行成果' }}
          </Button>
          <PiLocalTasks v-if="queueRun && run" :key="draftKey" :run-id="queueRun" :job-id="run.jobId" :main-id="mainId"
            :project-ids="run.projectIds" :projects="projectScope?.projects ?? []" :project-names="projectNames"
            :project-busy="projectBusy" :project-error="projectError" :unavailable="localTaskUnavailable"
            @refresh-projects="refreshProjects" @recheck-round="refresh" />
        </div>
        <form :aria-label="'发送 ' + cliLabel + ' 任务'" @submit.prevent="send">
          <Button v-if="suggestion" type="button" size="sm" variant="outline" class="mb-1.5 rounded-full"
            title="填入任务草稿" @click="fillSuggestion">{{ suggestion }}</Button>
          <details class="pi-composer-permissions">
            <summary>本轮权限 · {{ permissionLabel }}</summary>
            <ul v-if="permissionProjects.length" class="pi-permission-projects">
              <li v-for="project in permissionProjects" :key="project.id">
                <strong>{{ projectNames[project.id] ?? project.id }}</strong>
                <span>{{ project.mode === 'execute' ? '编辑并' + piCommandPermissionLabels[displayedExecution]
                  : project.mode === 'edit' ? '编辑' : '只读' }}</span>
                <template v-if="project.head">
                  <code v-if="project.path">{{ project.path }}</code>
                  <span v-if="project.branch !== undefined">{{ project.branch ?? '游离 HEAD' }}</span>
                  <code>HEAD {{ project.head }}</code>
                </template>
                <span v-else>{{ projectBusy ? '正在核对工程检出…' : projectError || '工程检出待核对' }}</span>
              </li>
            </ul>
            <p v-if="grok" class="text-xs text-muted-foreground">主工程只读分析。当前版本未开放工程编辑与命令。</p>
          </details>
          <section v-if="draft.crossHandoff" class="mb-2 grid gap-1.5 rounded-md border bg-card p-3 text-sm" aria-label="本轮交接">
            <strong>已准备交接 · {{ cliLabel }}</strong>
            <div class="flex flex-wrap items-center gap-2">
              <RouterLink :to="sourceLink(draft.crossHandoff.source)" class="underline underline-offset-4">查看来源轮次</RouterLink>
              <Button type="button" variant="outline" size="sm" :disabled="sending || !!pending || !!handoffBusy"
                @click="openCrossHandoff($event)">{{ crossHandoffIssue ? '复核交接来源' : '核对或编辑交接正文' }}</Button>
            </div>
            <pre class="max-h-32 overflow-auto whitespace-pre-wrap break-words">{{ draft.crossHandoff.context }}</pre>
            <p v-if="handoffTaskTooLong" class="text-destructive" role="status">本轮要求与交接正文合计超过 128 KiB，请缩减后发送。</p>
          </section>
          <PiHandoff v-if="draft.handoff" :key="draftKey" :spec-id="spec" :source="draft.handoff"
            :source-link="sourceLink(draft.handoff)" :project-ids="selectedProjectIds.filter(Boolean)"
            :project-names="projectNames" :model="readyModel" :disabled="sending || !!pending"
            :native-selected="nativeForkSelected" :native-ready="nativeForkReady"
            :refreshing="handoffBusy === draft.handoff.jobId" @refresh="refreshHandoff" @apply="applyHandoffDraft"
            @select-native="selectNativeFork" />
          <InputGroup class="pi-composer">
            <div v-if="!nativeForkSelected && !grok && !draft.crossHandoff && (draft.ciAttachment || draft.ciRecovery)" class="pi-draft-excerpts">
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
            <div v-if="!nativeForkSelected && !grok && !draft.crossHandoff && draft.citations.length" class="pi-draft-excerpts">
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
            <InputGroupTextarea id="pi-workspace-draft" v-model="draft.prompt" aria-label="本轮任务"
              :placeholder="active ? '准备下一轮要求' : '给 ' + cliLabel + ' 发一项任务'" rows="2" :disabled="sending" @keydown="onEnter" />
            <InputGroupAddon align="block-end" class="pi-composer-tools">
              <Button type="button" variant="ghost" size="sm" title="运行设置"
                :disabled="!context || (!!session && !direct)" @click="openSettings">
                <Settings2 :size="15" aria-hidden="true" />运行设置
              </Button>
              <PiModelPicker :selected-model="selectedModel" :current-model="currentModel"
                show-thinking :thinking-levels="thinkingLevels" :thinking-level="currentThinking" :checking-thinking="checking"
                :cli="targetCli" :locked-cli="lockedCli" :selection-locked="modelSelectionLocked" :issue="modelIssue"
                :disabled="modelPickerDisabled" @target="selectTarget" @new-session="newSession" @thinking="draft.thinkingLevel = $event">
                <template #trigger>
                  <Button type="button" variant="ghost" size="sm" class="pi-model-button" aria-label="选择 CLI 和模型" :title="modelLabel"
                    :disabled="modelPickerDisabled">
                    <LoadingIndicator :active="checking" /><span class="pi-model-label">{{ modelLabel }}</span><ChevronDown :size="13" aria-hidden="true" />
                  </Button>
                </template>
              </PiModelPicker>
              <Button v-if="queueRun" type="button" variant="ghost" size="sm" :disabled="!canEnqueue"
                aria-label="加入续办队列" title="按本会话范围排队，默认只读" @click="enqueue()">
                <ListPlus :size="15" />排队
              </Button>
              <Button v-if="active" type="button" size="icon" class="pi-send"
                :aria-label="preparing ? '取消准备' : '停止运行'" :title="preparing ? '取消准备' : '停止运行'"
                :disabled="!!stopState" @click="stop">
                <LoadingIndicator v-if="stopState === 'submitting'" :active="true" /><Square v-else :size="14" fill="currentColor" />
              </Button>
              <Button v-else type="submit" size="icon" class="pi-send" aria-label="发送" title="发送" :disabled="!canSend">
                <LoadingIndicator :active="sending || (!!draft.crossHandoff && !!handoffBusy)" />
                <ArrowUp v-if="!sending && (!draft.crossHandoff || !handoffBusy)" :size="19" />
              </Button>
            </InputGroupAddon>
          </InputGroup>
        </form>
        <p v-if="hostConfirmationStale && !grok" class="pi-composer-error" role="status">
          参与工程或检出已变化，请重新确认本机执行。
          <Button type="button" variant="ghost" size="sm" @click="commandExecution = 'sandbox'">改用沙箱离线执行</Button>
        </p>
        <p v-if="draft.error || storageIssue || (!active && (runtimeNeedsReview || crossHandoffIssue || nativeForkScopeIssue || commandCapabilityIssue || (modelIssue && !readyModel)))" class="pi-composer-error" role="status">
          {{ draft.error || storageIssue || nativeForkScopeIssue || commandCapabilityIssue || modelIssue ||
            runtimeNeedsReview || crossHandoffIssue }}
          <Button v-if="runtimeNeedsReview || (grok && modelIssue)" type="button" variant="ghost" size="sm"
            :disabled="checking || !!pending" @click="recheckRuntime">重新检查连接</Button>
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
      <Transition name="pi-panel" @after-enter="settlePanel('summary')" @after-leave="settlePanel('summary')">
      <aside v-if="inlineSummary" v-show="summaryOpen" :id="summaryPaneId" ref="summaryPane" :inert="!summaryOpen" class="pi-summary" aria-label="会话摘要">
        <section>
          <h2>会话摘要</h2>
          <p v-if="status">{{ status }}</p>
          <Button v-if="latest" type="button" variant="ghost" class="pi-summary-action" @click="jumpToRoundSummary">
            <ListChecks :size="15" aria-hidden="true" /> 本轮执行摘要
          </Button>
          <RouterLink v-if="run?.nativeForkSource" :to="sourceLink(run.nativeForkSource)">查看分叉来源</RouterLink>
          <RouterLink v-else-if="run?.continuationSource" :to="sourceLink(run.continuationSource)">查看接续来源</RouterLink>
          <p v-if="run?.ciJob">CI #{{ run.ciJob.jobId }} · {{ run.ciJob.mode === 'reproduce' ? '同提交复现' : '差异对照' }} · <code>{{ run.ciJob.sha }}</code><br v-if="run.ciJob.traceStatus">{{ run.ciJob.traceStatus }}</p>
          <p v-if="summary?.reviewError" class="pi-issue">{{ summary.reviewError }}</p>
          <Button v-if="latest && (summary?.hasSnapshot || summary?.hasAcceptance)" type="button" variant="ghost" class="pi-summary-action"
            @click="openReview()"><FileDiff :size="15" /> {{ summary.pendingFiles ? summary.pendingFiles + ' 个文件待审阅' : '执行成果' }}</Button>
          <Button v-for="(item, index) in summaryActions" :key="index" type="button" variant="ghost" class="pi-summary-action"
            @click="jump(item)">{{ item.status === 'saving' ? '答复保存中' : attentionLabels[item.kind] }}</Button>
        </section>
        <PiQueuePanel v-if="queueRun" :queue="queue" :busy="queueBusy" :error="queueError" :uncertain="queueUncertain || !!pendingEnqueue"
          :active="active" :verified="queueVerified" :goal-version="progression?.goalVersion"
          :per-project="queuePerProject" :can-edit-projects="canEditQueueProjects"
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
        <SheetHeader class="pi-sheet-header sr-only"><SheetTitle>会话历史</SheetTitle><SheetDescription>当前 Spec 的 Agent 会话</SheetDescription></SheetHeader>
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
          <DialogTitle>删除会话？</DialogTitle>
          <DialogDescription>
            将移除「{{ deleteTarget?.session.title }}」的工作台对话、执行记录和审阅入口。工程文件及本机 CLI 会话文件会保留。
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
    <AgentHandoffDialog v-model:open="crossDialogOpen" :spec-id="spec" :source="crossDialogSource"
      :target="crossDialogTarget" :existing="crossDialogExisting" :source-issue="crossDialogSourceIssue"
      :project-name="mainName || '主工程'" :permission="crossDialogEditing ? permissionLabel : '只读分析'"
      :destination="crossDialogDestination" @confirm="confirmCrossHandoff" @closed="closeCrossHandoff" />
    <Sheet v-model:open="settingsOpen">
      <SheetContent class="pi-sheet" @close-auto-focus.prevent="returnFocus('settings')">
        <SheetHeader class="pi-sheet-header"><SheetTitle>运行设置</SheetTitle>
          <SheetDescription>{{ context?.spec.title }}</SheetDescription></SheetHeader>
        <div v-if="context" class="pi-sheet-body">
          <PiWorkspaceSettings :key="draftKey" :host="targetCli" :grok-runtime="grokRuntime" :grok-checking="checking"
            :runtime-review="runtimeNeedsReview" :cross-handoff="!!handoffCli"
            v-model:ready-model="readyModel" v-model:selected-model="selectedModel"
            v-model:runtime-status="runtimeStatus"
            v-model:blocked-reason="modelIssue" v-model:model-restrictions="modelRestrictions"
            v-model:selected="draft.selected" v-model:project-modes="projectModes"
            v-model:stage="selectedStage" v-model:command-execution="commandExecution"
            :command-unavailable="commandUnavailable" :host-selectable="hostSelectable"
            :ci-attachment="draft.ciAttachment" :ci-recovery="draft.ciRecovery" @update:ci-attachment="changeCiAttachment"
            :spec-id="spec" :main-id="mainId" :projects="context.projects" :project-scope="projectScope"
            :project-busy="projectBusy" :project-error="projectError" :ci-issue="ciIssue"
            :project-grant-issue="projectGrantIssue" @clear-stale-project-grants="clearStaleProjectGrants"
            :review-resume-id="session || undefined" :busy="sending || !!pending" :native-fork="nativeForkSelected"
            :participating-ids="nativeForkSelected ? selectedProjectIds : run?.projectIds"
            :available-stages="availableStages" :automatic-stage="automaticStage"
            :stage-needs-review="stageNeedsReview" @refresh-projects="refreshProjects" @recheck-runtime="recheckRuntime" />
        </div>
      </SheetContent>
    </Sheet>
    <Sheet v-model:open="summaryDrawer">
      <SheetContent class="pi-sheet" @close-auto-focus.prevent="summaryClosed">
        <SheetHeader class="pi-sheet-header"><SheetTitle>会话摘要</SheetTitle><SheetDescription>{{ title }}</SheetDescription></SheetHeader>
        <div class="pi-sheet-body pi-mobile-summary">
          <p v-if="status">{{ status }}</p>
          <Button v-if="latest" type="button" variant="outline" class="max-sm:min-h-11" @click="jumpToRoundSummary">
            <ListChecks :size="15" aria-hidden="true" /> 本轮执行摘要
          </Button>
          <RouterLink v-if="run?.nativeForkSource" :to="sourceLink(run.nativeForkSource)">查看分叉来源</RouterLink>
          <RouterLink v-else-if="run?.continuationSource" :to="sourceLink(run.continuationSource)">查看接续来源</RouterLink>
          <p v-if="run?.ciJob">CI #{{ run.ciJob.jobId }} · {{ run.ciJob.mode === 'reproduce' ? '同提交复现' : '差异对照' }} · <code>{{ run.ciJob.sha }}</code><br v-if="run.ciJob.traceStatus">{{ run.ciJob.traceStatus }}</p>
          <RouterLink :to="{ path: '/specs/' + spec }" class="pi-summary-link"><FileText :size="15" />{{ context?.spec.title || '原 Spec' }}</RouterLink>
          <Button v-if="latest && (summary?.hasSnapshot || summary?.hasAcceptance)" type="button" variant="outline"
            @click="openReview()"><FileDiff :size="15" />执行成果</Button>
          <Button v-for="(item, index) in summaryActions" :key="index" type="button" variant="ghost" @click="jump(item)">
            {{ item.status === 'saving' ? '答复保存中' : attentionLabels[item.kind] }}
          </Button>
          <PiQueuePanel v-if="queueRun" :queue="queue" :busy="queueBusy" :error="queueError" :uncertain="queueUncertain || !!pendingEnqueue"
            :active="active" :verified="queueVerified" :goal-version="progression?.goalVersion"
            :per-project="queuePerProject" :can-edit-projects="canEditQueueProjects"
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
.pi-composer-permissions {
  margin-bottom: 6px;
  padding: 5px 0;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
  overflow-wrap: anywhere;
}
.pi-composer-permissions summary { color: var(--foreground); cursor: pointer; }
.pi-permission-projects {
  display: grid;
  gap: 5px;
  max-height: min(25vh, 240px);
  overflow: auto;
  padding-top: 5px;
}
.pi-permission-projects li {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 8px;
  padding: 4px 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}
.pi-composer-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 6px;
  padding-bottom: 7px;
}
.pi-composer-actions :deep(.pi-diff-shortcut) { margin: 0; }
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
