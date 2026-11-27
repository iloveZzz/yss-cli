import { apiRequest } from './workbench'
import { requestId, type JsonObject, type JobRecord } from './workbench-platform'
export interface EngineeringField {
  name: string
  label: string
  type: string
  required: boolean
  options?: { value: string; label: string }[] | string[]
}
export interface EngineeringOperation {
  id: string
  label: string
  mutating: boolean
  fields: EngineeringField[]
}
export interface EngineeringCatalog {
  operations: EngineeringOperation[]
  scaffold: {
    platforms: {
      id: string
      springBootVersion: string
      javaVersion: number
      recipes: {
        id: string
        parent: { group_id: string; artifact_id: string; version: string }
        bom: { group_id: string; artifact_id: string; version: string }
        evidenceStatus: string
        blockers: string[]
      }[]
    }[]
    defaultRecipeId?: string
  }
  bundleDigest: string
  processVersion: string
}
export interface EngineeringRequest {
  operation: string
  root: string
  profile?: string
  installation?: string
  variables: Record<string, string>
  skills: string[]
  projectName?: string
  backend?: JsonObject
  upgrade?: JsonObject
}
export interface EngineeringPreview {
  id: string
  request: EngineeringRequest
  report: unknown
  fingerprint: string
  bundleDigest: string
  createdAt: string
}
export interface EngineeringInspection {
  root: string
  doctor: unknown
  doctorError?: string
  skills: unknown
  skillsError?: string
  tracker: unknown
  tool?: unknown
  toolError?: string
  processVersion: string
}
export interface HomeRow {
  id: string
  responsibility: string
  git: string
  documents: string
  relations: string
  hints: string
}
export interface HomeImport {
  id: string
  root: string
  sourcePath: string
  sourceDigest: string
  rows: HomeRow[]
  warnings: string[]
  requiresReview: boolean
  bindings?: { entryId: string; rootPath: string; projectId?: string }[]
  stale?: boolean
  sourceError?: { message: string; context?: Record<string, unknown> } | null
}
export interface ExternalLink {
  id: string
  provider: 'github' | 'gitlab'
  host: string
  project: string
  kind: 'issue' | 'pullRequest' | 'pipeline'
  number: number
  projectId?: string
  specId?: string
  status: string
  lastAttemptAt?: string | null
  lastSuccessAt?: string | null
  lastError?: string | null
  data?: JsonObject | null
}
export const getEngineeringCatalog = () =>
  apiRequest<EngineeringCatalog>('/api/engineering/catalog')
export const inspectEngineering = (root: string) =>
  apiRequest<EngineeringInspection>('/api/engineering/inspect', { body: { root } })
export const previewEngineering = (request: EngineeringRequest) =>
  apiRequest<EngineeringPreview>('/api/engineering/preview', { body: request })
export const executeEngineering = (previewId: string) =>
  apiRequest<JobRecord>('/api/engineering/execute', { body: { previewId, requestId: requestId() } })
export const importHome = (
  root: string,
  sourcePath?: string,
  columnMapping?: Record<string, string>,
  rows?: HomeRow[]
) =>
  apiRequest<HomeImport>('/api/home/import', {
    body: { root, sourcePath: sourcePath || undefined, columnMapping, rows }
  })
export const getHomes = (signal?: AbortSignal) => apiRequest<{ homes: HomeImport[] }>('/api/homes', { signal })
export const bindHome = (
  homeId: string,
  sourceDigest: string,
  bindings: { entryId: string; rootPath: string }[]
) => apiRequest<HomeImport>('/api/home/bind', { body: { homeId, sourceDigest, bindings } })
export const getExternalLinks = (signal?: AbortSignal) => apiRequest<ExternalLink[]>('/api/external', { signal })
export const addExternalLink = (value: Omit<ExternalLink, 'id' | 'status'>) =>
  apiRequest<ExternalLink>('/api/external', { body: value })
export const refreshExternalLink = (id: string) =>
  apiRequest<JobRecord>('/api/external/refresh', { body: { id, requestId: requestId() } })
