import { apiRequest } from './workbench'

export type MergeRequestPlatform = 'github' | 'gitlab'
export type MergeRequestOrder = 'updated' | 'created'
export type MergeRequestStatus = 'open' | 'merged' | 'closed' | 'unknown'

export interface MergeRequestRepository {
  id: string
  platform: MergeRequestPlatform
  origin: string
  slug: string
  url: string
  included: boolean
}

export interface MergeRequestSourceSelection {
  projectId: string
  repositoryId: string
}

export interface MergeRequestSourceCandidate {
  repositoryId: string
  remote: string
  basis: 'home' | 'origin' | 'manual'
  selected: boolean
  default: boolean
}

export interface MergeRequestSources {
  repositories: MergeRequestRepository[]
  projects: {
    projectId: string
    name: string
    issue?: string | null
    candidates: MergeRequestSourceCandidate[]
  }[]
  revision: number
}

export interface MergeRequestSummary {
  number: number
  title: string | null
  author: string | null
  createdAt: string | null
  updatedAt: string | null
  status: MergeRequestStatus
  draft: boolean | null
  url: string | null
}

export interface MergeRequestIssue {
  code: string
  message: string
}

export interface MergeRequestFailure {
  issue: MergeRequestIssue
  identity?: string
}

export interface MergeRequestPage {
  identity: string
  items: MergeRequestSummary[]
  nextPage: number | null
  readAt: string | null
  continuationIssue?: MergeRequestIssue
}

export interface MergeRequestSignal {
  status: string
  message?: string | null
  commit?: string | null
  at?: string | null
}

export interface MergeRequestDetail extends MergeRequestSummary {
  identity: string
  description: string | null
  descriptionAvailable: boolean
  sourceBranch?: string | null
  targetBranch?: string | null
  sourceRepository?: string | null
  targetRepository?: string | null
  mergedAt?: string | null
  closedAt?: string | null
  review: MergeRequestSignal
  ci: MergeRequestSignal
  historicalCi: MergeRequestSignal[]
}

const sourcesPath = '/api/merge-requests/sources'

export function getMergeRequestSources(signal: AbortSignal) {
  return apiRequest<MergeRequestSources>(sourcesPath, { signal })
}

export function saveMergeRequestSources(
  revision: number,
  selected: MergeRequestSourceSelection[],
  signal: AbortSignal
) {
  return apiRequest<MergeRequestSources>(sourcesPath, { method: 'PUT', body: { revision, selected }, signal })
}

export function getMergeRequestPage(repositoryId: string, page: number, order: MergeRequestOrder, signal: AbortSignal) {
  return apiRequest<MergeRequestPage | MergeRequestFailure>('/api/merge-requests/page', { body: { repositoryId, page, order }, signal })
}

export function getMergeRequestDetail(repositoryId: string, number: number, signal: AbortSignal) {
  return apiRequest<MergeRequestDetail | MergeRequestFailure>('/api/merge-requests/detail', {
    body: { repositoryId, number },
    signal
  })
}
