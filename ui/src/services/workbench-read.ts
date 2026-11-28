import { apiRequest } from './workbench'
import type {
  ChangeEvent,
  MaterialChange,
  ProjectRecord,
  ProjectView,
  SavedView,
  Snapshot,
  SpecRecord,
  WorkItem,
  WorkQuery
} from './workbench-model'
import type { DeliveryTarget, ProgressSummary, StageSummary } from './workbench-progress'
import type { JobRecord } from './workbench-platform'

export type Verification = 'pending' | 'current' | 'stale' | 'failed'

export interface ProjectionState {
  changeSequence?: number
  projectionVersion?: string | null
  verification: Verification
  verificationError?: string | null
}

export interface WorkbenchCatalog {
  revision: number
  readAt: string
  projects: ProjectRecord[]
  specs: CatalogSpec[]
  savedViews: SavedView[]
}

export interface SpecDetailRead {
  snapshot: Snapshot
  specVersion: string
  observationId: number
}

export interface ProjectSummary {
  revision: number
  readAt: string
  project: ProjectView
}

export interface ProjectDirectoryStatus {
  rootPath: string
  available: boolean
  message: string | null
}

export type CatalogSpec = Pick<
  SpecRecord,
  'id' | 'projectId' | 'title' | 'sourcePath' | 'active' | 'stage' | 'status' | 'updatedAt' | 'lastVisitedAt'
>

export type CompactProgress = Pick<
  ProgressSummary,
  'goalVersion' | 'primaryStage' | 'status' | 'validity' | 'attention' | 'summary' | 'blocker' |
  'nextAction' | 'lastVerifiedAt' | 'evidenceDigest' | 'hasOverride' | 'materialReview' | 'activeStages' | 'syncEvidence'
> & {
  target?: DeliveryTarget | null
  diagnosisStage?: StageSummary['stage'] | null
  cancellation?: ProgressSummary['cancellation']
  lastDelivery?: Pick<NonNullable<ProgressSummary['lastDelivery']>, 'recordedAt'>
  stages: Pick<StageSummary, 'stage' | 'progress'>[]
}

export type CompactWorkItem = Omit<WorkItem, 'progress'> & { progress?: CompactProgress }

export function withSavedContentConfirmation(item: CompactWorkItem): CompactWorkItem {
  if (item.entity !== 'spec' || item.contentStatus === 'confirmed') return item
  return {
    ...item,
    contentStatus: 'confirmed',
    needsReconfirmation: false,
    reminderCounts: { ...item.reminderCounts, ready: 0 }
  }
}

export type WorkbenchJob = Pick<JobRecord, 'id' | 'title' | 'roots' | 'status' | 'updatedAt'>

export interface ReminderItem {
  specId: string
  title: string
  sourcePath: string
  projectName: string
  count: number
  needsReconfirmation?: boolean
  materialChange?: Pick<MaterialChange, 'target' | 'reason'> | null
  questions: { id: string; title: string; blocking: boolean }[]
}

interface Group<T> {
  total: number
  items: T[]
}

export interface WorkbenchOverview extends ProjectionState {
  revision: number
  readAt: string
  nextReviewAt?: string | null
  projectCount: number
  activeSpecCount: number
  groups: Record<'attention' | 'blocked' | 'doing' | 'source', Group<CompactWorkItem>>
  reminders: Record<string, Group<ReminderItem> & { specTotal: number }>
  recent: SpecRecord[]
  activity: ChangeEvent[]
}

export interface CompactWorkPage extends ProjectionState {
  revision: number
  readAt: string
  total: number
  items: CompactWorkItem[]
  queryVersion?: string | null
  nextReviewAt?: string | null
  coverage?: { total: number; inactive: number; closed: number }
}

export const loadWorkbenchJobs = (signal?: AbortSignal) =>
  apiRequest<Group<WorkbenchJob> & { version: string }>('/api/workbench/jobs', { signal })

export const loadWorkbenchCatalog = (signal?: AbortSignal) =>
  apiRequest<WorkbenchCatalog>('/api/workbench/catalog', { signal })

export const loadWorkbenchOverview = (signal?: AbortSignal) =>
  apiRequest<WorkbenchOverview>('/api/workbench/overview', { signal })

export const loadProjectSummary = (id: string, signal: AbortSignal) =>
  apiRequest<ProjectSummary>(`/api/projects/${encodeURIComponent(id)}/summary`, { signal })

export const loadProjectDirectoryStatus = (id: string, signal: AbortSignal) =>
  apiRequest<ProjectDirectoryStatus>(`/api/projects/${encodeURIComponent(id)}/directory-status`, { signal, priority: 'low' })

export const loadSpecDetail = (id: string, fresh: boolean, signal: AbortSignal) =>
  apiRequest<SpecDetailRead>(
    `/api/specs/${encodeURIComponent(id)}/detail${fresh ? '?fresh=1' : ''}`,
    { signal }
  )

export const loadCompactWorkPage = (
  query: WorkQuery,
  page: {
    offset?: number
    limit?: number
    expectedProjectionVersion?: string
    expectedQueryVersion?: string
  },
  signal?: AbortSignal
) => apiRequest<CompactWorkPage>('/api/work-items', { body: { query, compact: true, ...page }, signal })

export const requestWorkbenchReadRefresh = (signal?: AbortSignal) =>
  apiRequest<{ requested: boolean; minimumObservationId: number }>('/api/workbench/refresh', { body: {}, signal })
