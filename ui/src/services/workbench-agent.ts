import { apiRequest, type SpecView, type SourceDocument, type SpecRecord, type Stage } from './workbench'
import { requestId, type JobRecord, type JsonObject, type PiModel, type PiRuntimeStatus, type PiThinkingLevel } from './workbench-platform'
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
  reviewToken: string
  gitBaselines: Record<string, GitBaseline>
  files: AgentFile[]
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
  canResume: boolean
  resumeIssue?: string
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
  specId: string
  prompt: string
  projectIds: string[]
  carryFiles: Record<string, string[]>
  resumeRunId?: string
  expectedJobId?: string
  citations?: ReviewCitation[]
  selectedModel?: PiModel
  thinkingLevel?: PiThinkingLevel
  readOnly?: boolean
  expectedModel?: PiModel
  allowCommands?: boolean
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
}
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
export interface AgentWorkspace {
  spec: SpecRecord
  sourceError: string | null
  projects: AgentProject[]
  sessions: AgentSummary[]
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
export const startAgent = (value: AgentStart, id = requestId()) =>
  apiRequest<JobRecord>('/api/agent/start', { body: { ...value, requestId: id } })
export const getAgentRun = (id: string, signal?: AbortSignal) =>
  apiRequest<AgentRun>(`/api/agent/${encodeURIComponent(id)}`, { signal })
export const getAgentWorkspace = (specId: string, signal?: AbortSignal) =>
  apiRequest<AgentWorkspace>(`/api/specs/${encodeURIComponent(specId)}/agent-sessions`, { signal })
export const getAgentRounds = (id: string, signal?: AbortSignal) =>
  apiRequest<{ rounds: JobRecord[]; snapshotJobIds?: string[] }>(`/api/agent/${encodeURIComponent(id)}/rounds`, { signal })
export const deleteAgentSession = (id: string, expectedJobId: string) =>
  apiRequest<{ deleted: boolean; runId: string }>(`/api/agent/${encodeURIComponent(id)}`, {
    method: 'DELETE', body: { expectedJobId }
  })
export const getAgentDiff = (id: string, jobId?: string, signal?: AbortSignal) =>
  apiRequest<AgentDiff>(`/api/agent/${encodeURIComponent(id)}/diff${jobId ? `?jobId=${encodeURIComponent(jobId)}` : ''}`, { signal })
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
export const acceptAgentFiles = (id: string, reviewToken: string, files: AgentFile[], jobId?: string, attemptId?: string) =>
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
