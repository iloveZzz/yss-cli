import { shallowRef } from 'vue'
import { desktopStorageEnabled, flushDesktopDrafts, writeDesktopStorage } from './desktop-storage'
import { sorted } from '@/lib/collections'
export * from './workbench-model'
export * from './workbench-progress'
import type {
  MutationRequest,
  MutationResult,
  DiscoverResult,
  SourceDocument,
  Snapshot,
  Status,
  SpecView,
  SpecRecord,
  EditSpec,
  WorkQuery,
  QueryResult,
  LifecycleView,
  ProjectDocument
} from './workbench-model'
export const expired = shallowRef(false)
let token = sessionStorage.getItem('workbench.token') ?? ''
function consumeToken() {
  if (!location.hash.startsWith('#token=')) return false
  const incoming = location.hash.slice(7)
  if (/^[0-9a-f]{64}$/.test(incoming)) {
    token = incoming
    sessionStorage.setItem('workbench.token', token)
    expired.value = false
  }
  history.replaceState(null, '', location.pathname + location.search + '#/workbench')
  return true
}
consumeToken()
if (!token) expired.value = true
// Reopening the launch link in an existing tab is a hash navigation, not a page load.
const reopenSession = () => {
  if (consumeToken()) location.reload()
}
window.addEventListener('popstate', reopenSession)
window.addEventListener('hashchange', reopenSession)
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public context: Record<string, unknown> = {}
  ) {
    super(message)
  }
}
export function sessionHeaders(): Record<string, string> {
  if (expired.value) throw new ApiError('会话无效，请从终端重新打开完整工作台链接。', 401)
  return { Authorization: `Bearer ${token}` }
}
interface PendingIntent {
  requestId: string
  path: string
  method: string
  body: Record<string, unknown>
  createdAt: string
  status: 'sending' | 'uncertain'
}
const intents = new Map<string, PendingIntent>()
const desktopIntentsKey = 'yss:pi:pending-intents:1'
export const pendingRequests = shallowRef<Omit<PendingIntent, 'body'>[]>([])
function publishPending() {
  pendingRequests.value = Array.from(intents.values()).map((intent) => ({
    requestId: intent.requestId,
    path: intent.path,
    method: intent.method,
    createdAt: intent.createdAt,
    status: intent.status
  }))
  if (desktopStorageEnabled) {
    try {
      writeDesktopStorage(desktopIntentsKey, intents.size ? JSON.stringify(Array.from(intents)) : undefined)
    } catch {
      // Keep the pending request in memory; the desktop flush will fail if storage is unavailable.
    }
  }
}
export function restoreDesktopPendingRequests() {
  if (!desktopStorageEnabled) return
  try {
    const saved: unknown = JSON.parse(sessionStorage.getItem(desktopIntentsKey) ?? '[]')
    if (!Array.isArray(saved)) throw new Error('待核对请求格式无效')
    for (const entry of saved) {
      if (!Array.isArray(entry) || typeof entry[0] !== 'string' || !entry[1] || typeof entry[1] !== 'object')
        throw new Error('待核对请求格式无效')
      const intent = entry[1] as PendingIntent
      if (typeof intent.requestId !== 'string' || typeof intent.path !== 'string' ||
          !intent.path.startsWith('/api/') || typeof intent.method !== 'string' ||
          !intent.body || typeof intent.body !== 'object' || Array.isArray(intent.body))
        throw new Error('待核对请求格式无效')
      intents.set(entry[0], { ...intent, status: 'uncertain' })
    }
    publishPending()
  } catch {
    throw new Error('无法恢复待核对请求，请保留原工作台数据并核对。')
  }
}
function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical)
  if (value !== null && typeof value === 'object')
    return Object.fromEntries(
      sorted(Object.entries(value), ([left], [right]) => left.localeCompare(right))
        .map(([key, entry]) => [key, canonical(entry)])
    )
  return value
}
export function contentEqual(left: unknown, right: unknown): boolean {
  return JSON.stringify(canonical(left)) === JSON.stringify(canonical(right))
}
export async function acknowledgeRequest(requestId: string): Promise<void> {
  try {
    await fetch(`/api/receipts/${encodeURIComponent(requestId)}/ack`, {
      method: 'POST',
      headers: { ...sessionHeaders(), 'Content-Type': 'application/json' },
      body: '{}'
    })
  } catch {
    /* The committed result remains available for later reconciliation. */
  }
}
export function resolvePendingRequest(requestId: string) {
  for (const [key, intent] of intents) {
    if (intent.requestId === requestId) intents.delete(key)
  }
  publishPending()
}
export async function reconcilePendingMutation(requestId: string): Promise<unknown> {
  const intent = Array.from(intents.values()).find((pending) => pending.requestId === requestId)
  if (!intent || intent.path !== '/api/commands' || intent.method !== 'POST')
    throw new Error('当前会话缺少原保存请求，请回到原操作入口核对。')
  if (desktopStorageEnabled) {
    const receipt = await apiRequest<{ found: boolean; result?: unknown }>(
      `/api/receipts/${encodeURIComponent(requestId)}`
    )
    if (!receipt.found) throw new Error('尚未找到保存回执；原请求未重放，请核对后再处理。')
    resolvePendingRequest(requestId)
    void acknowledgeRequest(requestId)
    return receipt.result
  }
  return apiRequest(intent.path, { method: intent.method, body: intent.body })
}
export async function apiRequest<T>(
  path: string,
  options: { method?: string; body?: unknown; signal?: AbortSignal; priority?: RequestPriority; retryUncertain?: boolean } = {}
): Promise<T> {
  const authHeaders = sessionHeaders()
  const method = options.method ?? (options.body === undefined ? 'GET' : 'POST')
  const input =
    options.body !== null && typeof options.body === 'object' && !Array.isArray(options.body)
      ? (options.body as Record<string, unknown>)
      : undefined
  let intent: PendingIntent | undefined
  let intentKey = ''
  if (typeof input?.requestId === 'string') {
    const logical = Object.fromEntries(
      Object.entries(input).filter(
        ([key]) =>
          key !== 'requestId' &&
          !(input.action && ['expectedRevision', 'expectedTarget'].includes(key))
      )
    )
    intentKey = `${method} ${path} ${JSON.stringify(canonical(options.retryUncertain === false ? [input.requestId, logical] : logical))}`
    intent = intents.get(intentKey)
    if (!intent) {
      intent = {
        requestId: input.requestId,
        path,
        method,
        body: JSON.parse(JSON.stringify(input)),
        createdAt: new Date().toISOString(),
        status: 'sending'
      }
      intents.set(intentKey, intent)
    }
    intent.status = 'sending'
    publishPending()
    if (desktopStorageEnabled && !await flushDesktopDrafts()) {
      intent.status = 'uncertain'
      publishPending()
      throw new ApiError('未能保存待核对请求，本次操作未发送。', 0)
    }
  }
  const body = intent?.body ?? options.body
  const attempts = intent && !desktopStorageEnabled && options.retryUncertain !== false ? 2 : 1
  for (let attempt = 0; attempt < attempts; attempt++) {
    try {
      const response = await fetch(path, {
        method,
        headers: {
          ...authHeaders,
          ...(body === undefined ? {} : { 'Content-Type': 'application/json' })
        },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: options.signal,
        priority: options.priority
      })
      const value = response.status === 204 ? undefined : await response.json()
      if (!response.ok) {
        if (response.status === 401) {
          expired.value = true
          sessionStorage.removeItem('workbench.token')
        }
        const recovery =
          String(value?.error?.code ?? '')
            .replace(/[_-]/g, '')
            .toLowerCase() === 'recoveryrequired'
        const definitive =
          response.status >= 400 && response.status < 500 && response.status !== 408 && !recovery
        if (intent && definitive) {
          intents.delete(intentKey)
          publishPending()
          void acknowledgeRequest(intent.requestId)
        }
        throw new ApiError(
          value?.error?.message ?? `请求失败 (${response.status})`,
          response.status,
          {
            ...(value?.error?.context ?? {}),
            ...(intent ? { requestId: intent.requestId, uncertain: !definitive } : {})
          }
        )
      }
      if (intent) {
        intents.delete(intentKey)
        publishPending()
        void acknowledgeRequest(intent.requestId)
      }
      return value as T
    } catch (cause) {
      if (cause instanceof ApiError) {
        if (intent && (cause.status >= 500 || cause.context.uncertain === true)) {
          intent.status = 'uncertain'
          publishPending()
        }
        throw cause
      }
      if (!intent) throw cause
      if (attempt + 1 < attempts && !options.signal?.aborted) continue
      intent.status = 'uncertain'
      publishPending()
      throw new ApiError(desktopStorageEnabled
        ? '连接中断，操作结果待核对。请先核对回执。'
        : '连接中断，操作结果待核对。重试会核对同一次操作。', 0, {
        requestId: intent.requestId,
        uncertain: true
      })
    }
  }
  throw new Error('请求未完成')
}

async function request<T>(path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  return apiRequest(path, { body, signal })
}
export async function loadSnapshot(signal?: AbortSignal) {
  const value = await request<Snapshot>('/api/snapshot?compact=1', undefined, signal)
  if (
    !Number.isSafeInteger(value.revision) ||
    !Array.isArray(value.projects) ||
    !Array.isArray(value.specs) ||
    !Array.isArray(value.history) ||
    !Array.isArray(value.tasks) ||
    !Array.isArray(value.stageRecords) ||
    !Array.isArray(value.savedViews)
  )
    throw new Error('工作台快照格式不合法')
  return value
}
export const executeCommand = (requestBody: MutationRequest) =>
  request<MutationResult>('/api/commands', requestBody)
export const discoverSpecs = (
  body: { projectId: string; directory?: string },
  signal?: AbortSignal
) => request<DiscoverResult>('/api/discover', body, signal)
export const loadSource = (id: string, signal?: AbortSignal) =>
  request<SourceDocument>(`/api/specs/${encodeURIComponent(id)}/source`, undefined, signal)
export const errorMessage = (error: unknown) =>
  error instanceof Error ? error.message : '操作失败，请重试'
export const dateLabel = (value: string | null) =>
  value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
export const isClosed = (status: Status) => status === 'done' || status === 'canceled'
export const hasSourceProblem = (spec: SpecView) =>
  spec.sourceHealth !== 'available' ||
  spec.resources.some((r) => r.health !== 'available' && r.health !== 'external')
export function editFields(s: SpecRecord): EditSpec {
  return {
    sourcePath: s.sourcePath,
    title: s.title,
    stage: s.stage,
    status: s.status,
    nextAction: s.nextAction,
    blocker: s.blocker,
    notes: s.notes,
    resources: structuredClone(s.resources),
    active: s.active
  }
}

export async function queryWorkItems(query: WorkQuery, signal?: AbortSignal): Promise<QueryResult> {
  const result = await request<QueryResult>('/api/query', query, signal)
  if (!Number.isSafeInteger(result.revision) || !Array.isArray(result.items))
    throw new Error('查询响应格式不合法')
  return result
}
export async function loadLifecycle(specId: string, signal?: AbortSignal): Promise<LifecycleView> {
  const result = await request<LifecycleView>(
    '/api/specs/' + encodeURIComponent(specId) + '/lifecycle',
    undefined,
    signal
  )
  if (
    !Number.isSafeInteger(result.revision) ||
    !Array.isArray(result.stages) ||
    !Array.isArray(result.tasks) ||
    !Array.isArray(result.events)
  )
    throw new Error('生命周期响应格式不合法')
  return result
}

export const loadDocument = (specId: string, path: string, signal?: AbortSignal) =>
  request<ProjectDocument>('/api/document', { specId, path }, signal)
export const preparePreview = (specId: string, target: string, signal?: AbortSignal) =>
  request<{ url: string; message: string }>(
    `/api/specs/${encodeURIComponent(specId)}/preview`,
    { target },
    signal
  )
