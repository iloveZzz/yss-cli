import {
  apiRequest,
  sessionHeaders,
  ApiError,
  expired,
  type Action,
  type ChangeEvent,
  type QueryResult,
  type WorkQuery
} from './workbench'

export type JsonObject = Record<string, unknown>
export interface JobRecord {
  id: string
  kind: string
  title: string
  status: string
  requestId: string
  roots: string[]
  createdAt: string
  updatedAt: string
  result: unknown | null
  error: { code: string; message: string; context?: JsonObject } | null
}
export interface JobEvent {
  id: number
  jobId: string
  kind: string
  payload: unknown
  createdAt: string
}
export interface DraftRecord<T = JsonObject> {
  id: string
  editorSessionId: string
  targetType: string
  targetId: string
  baseToken: string
  base: T
  payload: T
  revision: number
  updatedAt: string
}
export interface EditableTarget<T = JsonObject> {
  token: string
  values: T
}
export interface PageResult<T> {
  total: number
  items: T[]
}
export interface SystemInfo {
  version: {
    version: string
    target: string
    bundleDigest: string
    source: { commit: string; state: string; snapshot: string }
  }
  dataDir: string
  executable: string
  storage: { schemaVersion: number; blobBytes: number; recordBytes: number; eventBytes: number }
  databaseBytes: number
  tools: { name: string; available: boolean; version?: string; message?: string }[]
}
export interface ArtifactFile {
  path: string
  sha256: string
  size: number
  kind: string
  mime: string
}
export interface ArtifactChange {
  path: string
  status: 'added' | 'removed' | 'modified'
  before: ArtifactFile | null
  after: ArtifactFile | null
  binary?: boolean
  beforeText?: string
  afterText?: string
  lines?: { kind: 'context' | 'remove' | 'add'; text: string }[]
}
export interface ConfirmationSnapshot {
  id: string
  specId: string
  scope: string
  artifactDigest: string
  createdAt: string
  baselineAvailable?: boolean
}
export interface ConfirmationDiff {
  baselineAvailable: boolean
  baseConfirmationId: string
  baseDigest?: string
  currentDigest?: string
  files: ArtifactChange[]
  message?: string
}
export interface ConfirmationArtifacts {
  baselineAvailable: boolean
  snapshot: {
    confirmationId: string
    specId: string
    artifactDigest: string
    capturedAt: string
    files: ArtifactFile[]
  } | null
}

export const requestId = () => crypto.randomUUID()
export const objectValue = (value: unknown): JsonObject =>
  value !== null && typeof value === 'object' && !Array.isArray(value) ? (value as JsonObject) : {}
export const readableValue = (value: unknown): string =>
  typeof value === 'string' ? value : (JSON.stringify(value, null, 2) ?? '')
export const terminalJob = (status: string) =>
  [
    'completed',
    'succeeded',
    'failed',
    'canceled',
    'cancelled',
    'interrupted',
    'recoveryRequired'
  ].includes(status)
export const jobStatusLabels: Record<string, string> = {
  queued: '排队中',
  preparing: '准备中',
  running: '运行中',
  waitingInput: '等待输入',
  waitingApproval: '等待授权',
  completed: '已完成',
  succeeded: '已完成',
  failed: '失败',
  canceled: '已停止',
  cancelled: '已停止',
  interrupted: '已中断',
  recoveryRequired: '需要恢复'
}
export interface RequestRecord {
  requestId: string
  kind?: string
  status?: string
  jobId?: string
  result?: unknown
  createdAt?: string
  updatedAt?: string
}
export const getRequests = () => apiRequest<{ items: RequestRecord[] }>('/api/requests')
export const getReceipt = (id: string) =>
  apiRequest<{ found: boolean; result?: unknown }>(`/api/receipts/${encodeURIComponent(id)}`)
export const getJobs = (signal?: AbortSignal) => apiRequest<JobRecord[]>('/api/jobs', { signal })
export const getJob = (id: string, signal?: AbortSignal) =>
  apiRequest<JobRecord>(`/api/jobs/${encodeURIComponent(id)}`, { signal })
export const cancelJob = (id: string) =>
  apiRequest<JobRecord>(`/api/jobs/${encodeURIComponent(id)}/cancel`, { body: {} })
export const answerJob = (id: string, requestedId: string, value: unknown) =>
  apiRequest<JobRecord>(`/api/jobs/${encodeURIComponent(id)}/input`, {
    body: { requestId: requestedId, value }
  })
export const restartWorkbench = (toolRoot?: string) =>
  apiRequest<{ restarting: boolean; message: string }>('/api/restart', {
    body: { toolRoot: toolRoot || undefined }
  })
export const getSystem = (signal?: AbortSignal) => apiRequest<SystemInfo>('/api/system', { signal })
export const createBackup = (targetPath: string) =>
  apiRequest<JobRecord>('/api/backups', { body: { targetPath, requestId: requestId() } })
export const restoreBackup = (archivePath: string, targetPath: string) =>
  apiRequest<JobRecord>('/api/restore', {
    body: { archivePath, targetPath, requestId: requestId() }
  })
export const getTarget = <T>(action: Action) =>
  apiRequest<EditableTarget<T>>('/api/targets', { body: { action } })
export const listDrafts = <T>(targetType: string, targetId: string) =>
  apiRequest<DraftRecord<T>[]>(`/api/drafts?${new URLSearchParams({ targetType, targetId })}`)
export const putDraft = <T>(
  id: string,
  value: Omit<DraftRecord<T>, 'id' | 'revision' | 'updatedAt'>,
  expectedRevision: number
) =>
  apiRequest<DraftRecord<T>>(`/api/drafts/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: { ...value, expectedRevision }
  })
export const removeDraft = (id: string, expectedRevision: number) =>
  apiRequest<{ deleted: boolean }>(`/api/drafts/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    body: { expectedRevision }
  })
export const queryPage = (query: WorkQuery, offset: number, limit: number, signal?: AbortSignal) =>
  apiRequest<QueryResult>('/api/work-items', { body: { query, offset, limit }, signal })
export const getHistory = (specId: string, offset: number, limit: number, signal?: AbortSignal) =>
  apiRequest<PageResult<ChangeEvent>>(
    `/api/history?${new URLSearchParams({ specId, offset: String(offset), limit: String(limit) })}`,
    { signal }
  )
export const getConfirmations = (specId: string) =>
  apiRequest<ConfirmationSnapshot[]>(`/api/specs/${encodeURIComponent(specId)}/confirmations`)
export const getConfirmationDiff = (specId: string, baseConfirmationId: string, path?: string) =>
  apiRequest<ConfirmationDiff>(`/api/specs/${encodeURIComponent(specId)}/diff`, {
    body: { baseConfirmationId, path }
  })
export const getConfirmationArtifacts = (id: string) =>
  apiRequest<ConfirmationArtifacts>(`/api/confirmations/${encodeURIComponent(id)}/artifacts`)
export const captureConfirmationSnapshot = (id: string) =>
  apiRequest<unknown>(`/api/confirmations/${encodeURIComponent(id)}/snapshot`, { body: {} })
export const prepareHistoricalPreview = (id: string, target: string, signal?: AbortSignal) =>
  apiRequest<{ url: string; message: string }>(
    `/api/confirmations/${encodeURIComponent(id)}/preview`,
    {
      body: { target },
      signal
    }
  )
export async function historicalFile(
  id: string,
  path: string,
  signal?: AbortSignal
): Promise<Blob> {
  return readBlobFile(
    `/api/confirmations/${encodeURIComponent(id)}/file?${new URLSearchParams({ path })}`,
    '历史文件读取失败',
    signal
  )
}
export function currentSpecFile(
  specId: string,
  path: string,
  sha256: string,
  signal?: AbortSignal
): Promise<Blob> {
  return readBlobFile(
    `/api/specs/${encodeURIComponent(specId)}/file?${new URLSearchParams({ path, sha256 })}`,
    '当前文件读取失败',
    signal
  )
}
async function readBlobFile(path: string, label: string, signal?: AbortSignal): Promise<Blob> {
  const response = await fetch(path, { headers: sessionHeaders(), signal })
  if (!response.ok) {
    if (response.status === 401) expired.value = true
    const payload: unknown = await response.json()
    const detail = objectValue(objectValue(payload).error)
    throw new ApiError(String(detail.message ?? `${label} (${response.status})`), response.status)
  }
  return response.blob()
}

export interface SourceState {
  externalVersion?: string
  progressVersions?: import('./workbench-progress').ProjectVersion[]
  revision: number
  sourceVersions: Record<string, string>
  readAt: string
}
export const getSourceState = () => apiRequest<SourceState>('/api/source-state')
export function streamJob(
  id: string,
  after: number,
  signal: AbortSignal,
  receive: (event: JobEvent) => void
): Promise<void> {
  return readNdjson<JobEvent>(
    `/api/jobs/${encodeURIComponent(id)}/events?${new URLSearchParams({ after: String(after) })}`,
    signal,
    (event) => {
      if (Number.isSafeInteger(event.id) && event.jobId === id) receive(event)
    }
  )
}
export async function readNdjson<T>(
  path: string,
  signal: AbortSignal,
  receive: (event: T) => void
): Promise<void> {
  const response = await fetch(path, { headers: sessionHeaders(), signal })
  if (!response.ok) {
    if (response.status === 401) expired.value = true
    throw new ApiError(`读取更新流失败 (${response.status})`, response.status)
  }
  if (!response.body) throw new Error('浏览器未提供更新流，请刷新页面。')
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let pending = ''
  function consume() {
    const lines = pending.split('\n')
    pending = lines.pop() ?? ''
    for (const line of lines) {
      if (line.trim()) receive(JSON.parse(line) as T)
    }
    if (pending.length > 2 * 1024 * 1024) throw new Error('单条更新内容过大，请刷新页面。')
  }
  try {
    while (!signal.aborted) {
      const { done, value } = await reader.read()
      if (done) break
      pending += decoder.decode(value, { stream: true })
      consume()
    }
    pending += decoder.decode()
    if (pending.trim()) {
      pending += '\n'
      consume()
    }
  } finally {
    reader.releaseLock()
  }
}
