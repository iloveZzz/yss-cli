import { shallowRef } from 'vue'
export * from './workbench-model'
import type { MutationRequest, MutationResult, DiscoverResult, SourceDocument, Snapshot, Status, SpecView, SpecRecord, EditSpec, WorkQuery, QueryResult, LifecycleView, ProjectDocument, AgentRequest } from './workbench-model'
export const expired = shallowRef(false)
let token = sessionStorage.getItem('workbench.token') ?? ''
function consumeToken() {
  if (!location.hash.startsWith('#token=')) return false
  const incoming = location.hash.slice(7)
  if (/^[0-9a-f]{64}$/.test(incoming)) { token = incoming; sessionStorage.setItem('workbench.token', token); expired.value = false }
  history.replaceState(null, '', location.pathname + '#/workbench')
  return true
}
consumeToken()
if (!token) expired.value = true
// Reopening the launch link in an existing tab is a hash navigation, not a page load.
const reopenSession = () => { if (consumeToken()) location.reload() }
window.addEventListener('popstate', reopenSession)
window.addEventListener('hashchange', reopenSession)
export class ApiError extends Error { constructor(message: string, public status: number) { super(message) } }
async function request<T>(path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  if (expired.value) throw new ApiError('会话无效，请从终端重新打开完整工作台链接。', 401)
  const response = await fetch(path, { method: body === undefined ? 'GET' : 'POST', headers: { Authorization: `Bearer ${token}`, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) }, body: body === undefined ? undefined : JSON.stringify(body), signal })
  const value = await response.json()
  if (!response.ok) {
    if (response.status === 401) { expired.value = true; sessionStorage.removeItem('workbench.token') }
    throw new ApiError(value?.error?.message ?? `请求失败 (${response.status})`, response.status)
  }
  return value as T
}
export async function loadSnapshot(signal?: AbortSignal) {
  const value = await request<Snapshot>('/api/snapshot', undefined, signal)
  if (!Number.isSafeInteger(value.revision) || !Array.isArray(value.projects) || !Array.isArray(value.specs) || !Array.isArray(value.history) || !Array.isArray(value.tasks) || !Array.isArray(value.stageRecords) || !Array.isArray(value.savedViews)) throw new Error('工作台快照格式不合法')
  return value
}
export const executeCommand = (requestBody: MutationRequest) => request<MutationResult>('/api/commands', requestBody)
export const discoverSpecs = (body: { projectId: string; directory: string }, signal?: AbortSignal) => request<DiscoverResult>('/api/discover', body, signal)
export const loadSource = (id: string, signal?: AbortSignal) => request<SourceDocument>(`/api/specs/${encodeURIComponent(id)}/source`, undefined, signal)
export const errorMessage = (error: unknown) => error instanceof Error ? error.message : '操作失败，请重试'
export const dateLabel = (value: string | null) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
export const isClosed = (status: Status) => status === 'done' || status === 'canceled'
export const hasSourceProblem = (spec: SpecView) => spec.sourceHealth !== 'available' || spec.resources.some(r => r.health !== 'available' && r.health !== 'external')
export function editFields(s: SpecRecord): EditSpec { return { sourcePath: s.sourcePath, title: s.title, stage: s.stage, status: s.status, nextAction: s.nextAction, blocker: s.blocker, notes: s.notes, resources: structuredClone(s.resources), active: s.active } }

export async function queryWorkItems(query:WorkQuery, signal?:AbortSignal):Promise<QueryResult> {
 const result=await request<QueryResult>('/api/query',query,signal)
 if(!Number.isSafeInteger(result.revision)||!Array.isArray(result.items)) throw new Error('查询响应格式不合法')
 return result
}
export async function loadLifecycle(specId:string, signal?:AbortSignal):Promise<LifecycleView> {
 const result=await request<LifecycleView>('/api/specs/'+encodeURIComponent(specId)+'/lifecycle',undefined,signal)
 if(!Number.isSafeInteger(result.revision)||!Array.isArray(result.stages)||!Array.isArray(result.tasks)||!Array.isArray(result.events)) throw new Error('生命周期响应格式不合法')
 return result
}

export const loadDocument = (specId: string, path: string, signal?: AbortSignal) => request<ProjectDocument>('/api/document', { specId, path }, signal)
export const loadAgentRequest = (specId: string, signal?: AbortSignal) => request<AgentRequest>(`/api/specs/${encodeURIComponent(specId)}/request`, undefined, signal)
export const preparePreview = (specId: string, target: string, signal?: AbortSignal) => request<{ url: string; message: string }>(`/api/specs/${encodeURIComponent(specId)}/preview`, { target }, signal)
