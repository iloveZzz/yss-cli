import { apiRequest, type SpecView, type SourceDocument, type Stage } from './workbench'
import { requestId, type JobRecord, type JsonObject } from './workbench-platform'
export interface FileDescriptor {
  sha256: string
  size: number
  mode: number
}
export interface AgentFile {
  projectId: string
  path: string
  workspacePath: string
  initial: FileDescriptor | null
  result: FileDescriptor | null
  current: FileDescriptor | null
  conflict: boolean
  binary: boolean
  initialText?: string
  resultText?: string
  currentText?: string
}
export interface AgentDiff {
  runId: string
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
}
export const startAgent = (value: AgentStart) =>
  apiRequest<JobRecord>('/api/agent/start', { body: { ...value, requestId: requestId() } })
export const getAgentRun = (id: string) =>
  apiRequest<AgentRun>(`/api/agent/${encodeURIComponent(id)}`)
export const getAgentDiff = (id: string) =>
  apiRequest<AgentDiff>(`/api/agent/${encodeURIComponent(id)}/diff`)
export const getAgentBindingPreview = (id: string) =>
  apiRequest<AgentBindingPreview>(`/api/agent/${encodeURIComponent(id)}/rebind-preview`)
export const rebindAgent = (id: string, previewToken: string) =>
  apiRequest<AgentBindingResult>(`/api/agent/${encodeURIComponent(id)}/rebind`, {
    body: { previewToken }
  })
export const acceptAgentFiles = (id: string, reviewToken: string, files: AgentFile[]) =>
  apiRequest<unknown>(`/api/agent/${encodeURIComponent(id)}/accept`, {
    body: {
      reviewToken,
      files: files.map((file) => ({
        projectId: file.projectId,
        path: file.path,
        expectedCurrent: file.current,
        expectedResult: file.result
      }))
    }
  })

export const getAgentSpecView = (runId: string, specId: string) =>
  apiRequest<SpecView>(
    `/api/agent/${encodeURIComponent(runId)}/specs/${encodeURIComponent(specId)}/view`
  )
export const loadAgentSource = (runId: string, specId: string, signal?: AbortSignal) =>
  apiRequest<SourceDocument>(
    `/api/agent/${encodeURIComponent(runId)}/specs/${encodeURIComponent(specId)}/source`,
    { signal }
  )
export const loadAgentDocument = (
  runId: string,
  specId: string,
  path: string,
  signal?: AbortSignal
) =>
  apiRequest<SourceDocument>(`/api/agent/${encodeURIComponent(runId)}/document`, {
    body: { specId, path },
    signal
  })
export const prepareAgentPreview = (
  runId: string,
  specId: string,
  target: string,
  signal?: AbortSignal
) =>
  apiRequest<{ url: string; message: string }>(`/api/agent/${encodeURIComponent(runId)}/preview`, {
    body: { specId, target },
    signal
  })
