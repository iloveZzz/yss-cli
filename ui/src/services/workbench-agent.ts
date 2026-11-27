import { apiRequest, type SpecView, type SourceDocument, type SpecRecord, type Stage } from './workbench'
import { requestId, type JobRecord, type JsonObject, type LocalProjectCheckout, type LocalProjectTaskAction,
  type LocalProjectTaskJob, type PiCommandExecution, type PiModel, type PiRuntimeStatus, type PiThinkingLevel } from './workbench-platform'
import type { Verification } from './workbench-read'
export type AgentWorkspaceMode = 'direct' | 'isolated'
export type ProjectAccessMode = 'read' | 'edit' | 'execute'
export interface AgentProjectAccess {
  projectId: string
  mode: ProjectAccessMode
  expectedHead: string
  expectedRootPath?: string
  expectedBranch?: string | null
}
export interface AgentContinuationSource {
  runId: string
  jobId: string
}
export interface AgentAction {
  available: boolean
  reason: string | null
  reasonCode?: string | null
}
export interface AgentRoundActions {
  viewHistory: AgentAction
  continueSameSpec: AgentAction
}
export interface NativeForkPreview {
  state: 'available' | 'blocked' | 'unsupported'
  reasonCode?: string
  message?: string
  digest?: string
  model?: PiModel
  projectIds?: string[]
}
export interface AgentContinuation extends AgentContinuationSource {
  prompt: string
  projectIds: string[]
  workspaces: { projectIds: string[]; branch: string | null; head: string }[]
  summary?: AgentContinuationSummary
  summaryDigest?: string
  nativeFork?: NativeForkPreview
}
export type AgentContinuationPreview = Omit<AgentContinuation, 'prompt'> & { originalPrompt?: string }
interface AgentContinuationSummary {
  status: string
  errorCode?: string | null
  goalVersion?: string | null
  specDigest?: string | null
  lifecycleStage?: Stage | null
  projects: { projectId: string; branch?: string | null; head?: string | null; frozenPaths?: string[] | null }[]
  ciJob?: { projectId: string; jobId: number; sha: string; status?: string | null } | null
  localTasks: { projectId: string; jobId: string; action: string; status: string; exitCode?: number | null }[]
  responseIds: string[]
  gaps: string[]
}
export interface AgentContinuationDraftInput extends AgentContinuationSource {
  expectedContinuationDigest: string
  expectedModel: PiModel
  projectIds: string[]
  notes: string
}
export interface AgentContinuationDraftPreview {
  input: { systemPrompt: string; prompt: string }
  previewDigest: string
}
export type AgentCiMode = 'reproduce' | 'compare'
export interface AgentCiJob {
  projectId: string
  linkId: string
  jobId: number
  mode: AgentCiMode
  expectedSha: string
}
export interface AgentCiEvidence extends AgentCiJob {
  sha: string
  name?: string
  url?: string
  traceStatus?: string
  localHead?: string
  localBranch?: string | null
}
export interface AgentCiPreview {
  projectId: string
  linkId: string
  jobId: number
  sha: string
  pipelineId: number
  name: string
  status: string
  url: string
}

export interface FileDescriptor {
  sha256: string
  size: number
  mode: number
}
export interface AgentFile {
  projectId: string
  gitRoot: string
  path: string
  workspacePath: string
  initial: FileDescriptor | null
  baseline: FileDescriptor | null
  result: FileDescriptor | null
  current: FileDescriptor | null
  conflict: boolean
  pending: boolean
  accepted: boolean
  contentMatches: boolean
  binary: boolean
  initialText?: string
  resultText?: string
  currentText?: string
  reviewedBefore?: string | null
  reviewedBeforeDescriptor?: FileDescriptor | null
  reviewedBeforeKnown?: boolean
  acceptedReviewToken?: string | null
}
export interface AgentDiff {
  runId: string
  jobId: string
  historical: boolean
  workspaceMode?: AgentWorkspaceMode
  reviewToken: string
  gitBaselines: Record<string, GitBaseline>
  files: AgentFile[]
}
export type AgentFileSummary = Omit<AgentFile, 'binary' | 'initialText' | 'resultText' | 'currentText' | 'reviewedBefore'>
export type AgentDiffSummary = Omit<AgentDiff, 'files'> & { files: AgentFileSummary[]; bindingRevision?: number }
export interface AgentDiffHunk {
  side: 'initial' | 'current'
  oldStart: number
  newStart: number
  oldLines: string[]
  newLines: string[]
}
export interface AgentDiffFile extends AgentFile {
  hunks: AgentDiffHunk[]
  hunksTruncated: boolean
}
export interface GitBaseline {
  head: string
  branch: string | null
  indexDigest: string
  gitDir: string
  gitDirIdentity: { volume: number; index: number }
  commonDir: string
  commonDirIdentity: { volume: number; index: number }
}
export interface AgentBinding {
  projectIds: string[]
  previousRoot: string
  root: string
  rootIdentity: { volume: number; index: number }
  projectPrefixes: Record<string, string>
  gitBaseline: GitBaseline
}
export interface AgentBindingPreview {
  runId: string
  previewToken: string
  bindings: AgentBinding[]
}
export interface AgentBindingResult {
  runId: string
  status: 'rebound'
  bindings: AgentBinding[]
  rebindId: string
}
export interface AgentRun {
  lifecycleStage?: Stage | null
  goalVersion?: string | null
  specDigest?: string | null
  id: string
  specId: string
  prompt: string
  projectIds: string[]
  status: string
  jobId: string
  host: 'codex' | 'pi'
  workspaceMode?: AgentWorkspaceMode
  snapshotError?: string
  readOnly?: boolean | null
  commandExecution?: PiCommandExecution
  projectAccess?: AgentProjectAccess[]
  canEditQueuedProjects?: boolean
  continuationSource?: AgentContinuationSource
  nativeForkSource?: AgentContinuationSource
  ciJob?: AgentCiEvidence
  canResume: boolean
  resumeIssue?: string
  resumeReason?: string
  actions?: { resumeNative: AgentAction }
  recoveryJobId?: string
  recoveryRunId?: string
  rebindings?: { id: string; createdAt: string; previousAppliedFiles: { projectId: string; path: string; acceptedAt: string }[] }[]
  threadId?: string
  turnId?: string
  hostSettings?: JsonObject
  workspaces: unknown[]
  answers: unknown[]
  appliedFiles: unknown[]
  createdAt: string
  updatedAt: string
}
export interface AgentStart {
  lifecycleStage?: Stage
  expectedGoalVersion?: string
  specId: string
  prompt: string
  projectIds?: string[]
  projectAccess?: AgentProjectAccess[]
  expectedRepositoryKeys?: Record<string, string | null>
  continuationSource?: AgentContinuationSource
  expectedContinuationDigest?: string
  nativeForkSource?: AgentContinuationSource & { expectedForkDigest: string }
  ciJob?: AgentCiJob
  resumeRunId?: string
  expectedJobId?: string
  citations?: ReviewCitation[]
  selectedModel?: PiModel
  thinkingLevel?: PiThinkingLevel
  readOnly?: boolean
  expectedModel?: PiModel
  allowCommands?: boolean
  commandExecution?: PiCommandExecution
  hostExecutionConfirmed?: boolean
}
export interface RunActivity {
  runId: string
  jobId: string
  stage: Stage
  goalVersion: string
  specDigest: string
  status: string
  projectIds: string[]
  readOnly: boolean | null
  resultAvailable: boolean
  acceptance: {
    projectId: string
    status: 'writing' | 'accepted' | 'noWrite' | 'failed' | 'needsVerification'
  }[]
}

export function loadSpecRunActivities(
  specId: string, goalVersion: string, specDigest: string, signal?: AbortSignal
) {
  const scope = new URLSearchParams({ goalVersion, specDigest })
  return apiRequest<{ items: RunActivity[] }>(`/api/specs/${encodeURIComponent(specId)}/activity?${scope}`, { signal })
}
export interface ReviewCitation {
  reviewToken: string
  projectId: string
  path: string
  side: 'initial' | 'result'
  startLine: number
  endLine: number
  note: string
}
export interface ReviewExcerpt extends ReviewCitation {
  jobId: string
  snippet: string
  runId?: string
  bindingRevision?: number
}
export interface PiQueueEntry {
  id: string
  prompt: string
  citations: ReviewCitation[]
  readOnly: boolean
  goalVersion?: string | null
  dispatching: boolean
  jobId?: string | null
}
export interface PiQueue {
  runId: string
  version: number
  pausedReason: string | null
  lastJobId: string
  items: PiQueueEntry[]
}
export interface PiQueueDraft {
  version: number
  clientId?: string
  prompt: string
  citations?: ReviewCitation[]
  readOnly: boolean
  expectedGoalVersion?: string
}
const queuePath = (run: string) => `/api/agent/${encodeURIComponent(run)}/queue`
export const getPiQueueReceipt = (run: string, body: PiQueueDraft) =>
  apiRequest<{ found: boolean; matches: boolean | null }>(`${queuePath(run)}/receipt`, { body })
export const getPiQueue = (run: string, signal?: AbortSignal) =>
  apiRequest<PiQueue>(queuePath(run), { signal })
export const putPiQueueItem = (run: string, body: PiQueueDraft, itemId?: string) =>
  itemId
    ? apiRequest<PiQueue>(`${queuePath(run)}/${encodeURIComponent(itemId)}`, { method: 'PATCH', body })
    : apiRequest<PiQueue>(queuePath(run), { body })
export const removePiQueueItem = (run: string, itemId: string, version: number) =>
  apiRequest<PiQueue>(`${queuePath(run)}/${encodeURIComponent(itemId)}`, { method: 'DELETE', body: { version } })
export const resumePiQueue = (run: string, version: number, confirmDirectEdit = false) =>
  apiRequest<PiQueue>(`${queuePath(run)}/resume`, { body: { version, confirmDirectEdit } })
export interface AgentAttention {
  kind: 'recovery' | 'question' | 'answerFailed' | 'interrupted' | 'failed' | 'review'
  activityId?: number
  status?: string
}
export interface AgentSummary {
  id: string
  jobId: string
  title: string
  status: string
  updatedAt: string
  attention: AgentAttention[]
  workspaceMode?: AgentWorkspaceMode
  snapshotError?: string
  pendingFiles: number | null
  hasSnapshot: boolean
  hasAcceptance: boolean
  deleteIssue?: string | null
  reviewError?: string
  reviewTargets: [string, string][]
}
export interface AgentProject {
  id: string
  name: string
  rootPath: string
  active: boolean
}
export interface AgentProjectCandidate extends AgentProject {
  status: 'ready' | 'inactive' | 'unavailable'
  branch: string | null
  head: string | null
  repositoryKey?: string | null
  repositoryGroupKey?: string | null
}
export interface AgentHomeEntry {
  id: string
  projectId: string | null
  projectIds: string[]
  status: 'ready' | 'inactive' | 'unbound' | 'unavailable' | 'stale'
  name?: string
  rootPath?: string
}
export interface AgentProjects {
  projects: AgentProjectCandidate[]
  homes: { id: string; root: string; sourcePath: string; stale: boolean; entries: AgentHomeEntry[] }[]
}
export interface AgentWorkspace {
  spec: SpecRecord
  sourceError: string | null
  projects: AgentProject[]
  sessions: AgentSummary[]
}
export interface AgentSessionPage extends AgentWorkspace {
  verification: Verification
  verificationError: string | null
  stageBinding: { goalVersion: string; specDigest: string; stages: Stage[]; primaryStage: Stage | null } | null
  total: number
  attentionTotal: number
  version: string
  nextCursor: string | null
  selectedSession?: AgentSummary | null
}
export type AcceptanceDisposition = 'applied' | 'noWrite' | 'rolledBack' | 'recoveryRequired'
  | 'committedPendingCleanup' | 'unverified' | 'verified' | 'writing'
export interface AcceptanceReport {
  projectId: string
  jobId?: string
  root: string
  disposition: AcceptanceDisposition
  status?: string
  transaction?: { id?: string; operations: number }
  error?: { message: string }
  verificationError?: string
  observed?: { path: string; matchesResult: boolean; matchesBefore: boolean }[]
}
export interface AgentAcceptance {
  jobId?: string
  attemptId?: string | null
  notReceived?: boolean
  projects: AcceptanceReport[]
  refreshError?: { message: string } | null
}
type PiRunTarget = Pick<AgentStart, 'resumeRunId' | 'selectedModel'>
export const piModelKey = (model: PiModel) => JSON.stringify([model.provider, model.id])

export const getPiModels = (signal?: AbortSignal) =>
  apiRequest<{ models: PiModel[] }>('/api/agent/models', { signal })

export function getPiRuntime({ resumeRunId, selectedModel }: PiRunTarget, signal?: AbortSignal) {
  const query = new URLSearchParams()
  if (resumeRunId) query.set('resumeRunId', resumeRunId)
  if (selectedModel) {
    query.set('selectedProvider', selectedModel.provider)
    query.set('selectedModel', selectedModel.id)
  }
  const params = query.toString()
  return apiRequest<PiRuntimeStatus>(`/api/agent/runtime${params ? `?${params}` : ''}`, { signal })
}

export const testPiConnection = (expectedModel: PiModel, target: PiRunTarget, signal?: AbortSignal) =>
  apiRequest<{ connected: boolean }>('/api/agent/test-model', {
    body: { expectedModel, ...target },
    signal
  })
export const startAgent = (value: AgentStart, id: string = requestId()) =>
  apiRequest<JobRecord>('/api/agent/start', { body: { ...value, requestId: id } })
export const admitAgent = (value: AgentStart, id: string = requestId()) =>
  apiRequest<JobRecord>('/api/agent/start?admission=async', {
    body: { ...value, requestId: id },
    retryUncertain: false
  })
export const getAgentProjects = (specId: string, signal?: AbortSignal) =>
  apiRequest<AgentProjects>(`/api/specs/${encodeURIComponent(specId)}/agent-projects`, { signal })
export function getAgentContinuation(specId: string, source: AgentContinuationSource, signal?: AbortSignal) {
  const query = new URLSearchParams({ runId: source.runId, jobId: source.jobId })
  return apiRequest<AgentContinuation>(`/api/specs/${encodeURIComponent(specId)}/agent-continuation?${query}`, { signal })
}
function continuationDraftPath(specId: string) {
  return `/api/specs/${encodeURIComponent(specId)}/agent-continuation/draft`
}
export const previewAgentContinuationDraft = (specId: string, input: AgentContinuationDraftInput, signal: AbortSignal) =>
  apiRequest<AgentContinuationDraftPreview>(continuationDraftPath(specId), { body: { ...input, previewOnly: true }, signal })
export const generateAgentContinuationDraft = (
  specId: string, input: AgentContinuationDraftInput, expectedPreviewDigest: string, signal: AbortSignal
) => apiRequest<{ draft: string }>(continuationDraftPath(specId), {
  body: { ...input, expectedPreviewDigest }, signal, retryUncertain: false
})
export const previewAgentCiJob = (
  specId: string, body: { projectId: string; linkId: string; jobRef: string }, signal?: AbortSignal
) => apiRequest<AgentCiPreview>(`/api/specs/${encodeURIComponent(specId)}/agent-ci-job/preview`, { body, signal })
export const getAgentRun = (id: string, signal?: AbortSignal) =>
  apiRequest<AgentRun>(`/api/agent/${encodeURIComponent(id)}`, { signal })
export const getAgentWorkspace = (specId: string, signal?: AbortSignal) =>
  apiRequest<AgentWorkspace>(`/api/specs/${encodeURIComponent(specId)}/agent-sessions`, { signal })
export function getAgentSessionPage(
  specId: string,
  page: { search: string; attention: boolean; cursor?: string; runId?: string },
  signal?: AbortSignal
) {
  const query = new URLSearchParams({ compact: '1' })
  if (page.search) query.set('q', page.search)
  if (page.attention) query.set('attention', '1')
  if (page.cursor) query.set('cursor', page.cursor)
  if (page.runId) query.set('runId', page.runId)
  return apiRequest<AgentSessionPage>(`/api/specs/${encodeURIComponent(specId)}/agent-sessions?${query}`, { signal })
}
export const getAgentRounds = (id: string, signal?: AbortSignal) =>
  apiRequest<{ rounds: JobRecord[]; snapshotJobIds?: string[]; roundCiJobs?: Record<string, AgentCiEvidence>; roundActions?: Record<string, AgentRoundActions> }>(
    `/api/agent/${encodeURIComponent(id)}/rounds`, { signal }
  )
export interface LocalProjectTaskInput {
  projectId: string
  directory: string
  action: LocalProjectTaskAction
  expectedJobId: string
  expectedCheckout?: LocalProjectCheckout
  requestId: string
}
export const agentLocalTasksPath = (id: string) => `/api/agent/${encodeURIComponent(id)}/local-tasks`
export const getAgentLocalTasks = (id: string, signal?: AbortSignal) =>
  apiRequest<{ items: LocalProjectTaskJob[] }>(agentLocalTasksPath(id), { signal })
export const startAgentLocalTask = (id: string, body: LocalProjectTaskInput) =>
  apiRequest<LocalProjectTaskJob>(agentLocalTasksPath(id), { body, retryUncertain: false, persistInBrowser: true })
export const deleteAgentSession = (id: string, expectedJobId: string) =>
  apiRequest<{ deleted: boolean; runId: string }>(`/api/agent/${encodeURIComponent(id)}`, {
    method: 'DELETE', body: { expectedJobId }
  })
export const getAgentDiff = (id: string, jobId?: string, signal?: AbortSignal) =>
  apiRequest<AgentDiff>(`/api/agent/${encodeURIComponent(id)}/diff${jobId ? `?jobId=${encodeURIComponent(jobId)}` : ''}`, { signal })
export function getAgentDiffSummary(id: string, jobId: string, signal?: AbortSignal) {
  const query = new URLSearchParams({ compact: '1', jobId })
  return apiRequest<AgentDiffSummary>(`/api/agent/${encodeURIComponent(id)}/diff?${query}`, { signal })
}
export function getAgentDiffFile(
  id: string, jobId: string, reviewToken: string, file: Pick<AgentFileSummary, 'projectId' | 'path'>,
  full: boolean, signal?: AbortSignal
) {
  const query = new URLSearchParams({ jobId, reviewToken, projectId: file.projectId, path: file.path })
  if (full) query.set('full', '1')
  return apiRequest<AgentDiffFile>(`/api/agent/${encodeURIComponent(id)}/diff/file?${query}`, { signal })
}
export const getAgentAcceptance = (id: string, attemptId?: string, signal?: AbortSignal) =>
  apiRequest<AgentAcceptance | null>(
    `/api/agent/${encodeURIComponent(id)}/acceptance${attemptId ? `?attemptId=${encodeURIComponent(attemptId)}` : ''}`,
    { signal }
  )
export const verifyAgentAcceptance = (id: string, jobId: string, attemptId?: string) =>
  apiRequest<AgentAcceptance | null>(`/api/agent/${encodeURIComponent(id)}/verify-acceptance`, { body: { jobId, attemptId } })
export const getAgentBindingPreview = (id: string) =>
  apiRequest<AgentBindingPreview>(`/api/agent/${encodeURIComponent(id)}/rebind-preview`)
export const rebindAgent = (id: string, previewToken: string) =>
  apiRequest<AgentBindingResult>(`/api/agent/${encodeURIComponent(id)}/rebind`, {
    body: { previewToken }
  })
export const acceptAgentFiles = (id: string, reviewToken: string, files: AgentFileSummary[], jobId?: string, attemptId?: string) =>
  apiRequest<AgentAcceptance>(`/api/agent/${encodeURIComponent(id)}/accept`, {
    body: {
      reviewToken,
      jobId,
      attemptId,
      files: files.map((file) => ({
        projectId: file.projectId,
        path: file.path,
        expectedCurrent: file.current,
        expectedResult: file.result,
        // Callers collect explicit confirmation before passing a conflicting file.
        approveConflict: file.conflict
      }))
    }
  })

const roundQuery = (jobId?: string) => jobId ? `?jobId=${encodeURIComponent(jobId)}` : ''
export const getAgentSpecView = (runId: string, specId: string, jobId?: string) =>
  apiRequest<SpecView>(
    `/api/agent/${encodeURIComponent(runId)}/specs/${encodeURIComponent(specId)}/view${roundQuery(jobId)}`
  )
export const loadAgentSource = (runId: string, specId: string, signal?: AbortSignal, jobId?: string) =>
  apiRequest<SourceDocument>(
    `/api/agent/${encodeURIComponent(runId)}/specs/${encodeURIComponent(specId)}/source${roundQuery(jobId)}`,
    { signal }
  )
export const loadAgentDocument = (
  runId: string,
  specId: string,
  path: string,
  signal?: AbortSignal,
  jobId?: string
) =>
  apiRequest<SourceDocument>(`/api/agent/${encodeURIComponent(runId)}/document${roundQuery(jobId)}`, {
    body: { specId, path },
    signal
  })
export const prepareAgentPreview = (
  runId: string,
  specId: string,
  target: string,
  signal?: AbortSignal,
  jobId?: string
) =>
  apiRequest<{ url: string; message: string }>(`/api/agent/${encodeURIComponent(runId)}/preview${roundQuery(jobId)}`, {
    body: { specId, target },
    signal
  })
