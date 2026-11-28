import { apiRequest } from './workbench'

export type IssuePlatform = 'github' | 'gitlab'
export type IssueStatus = 'open' | 'closed'
export type IssueState = IssueStatus | 'all'

export interface IssueSource {
  id: string
  projectId: string
  projectName: string
  platform: IssuePlatform
  origin: string
  slug: string
  url: string
  basis: 'tracker' | 'remote'
}

export interface IssueSources {
  sources: IssueSource[]
  warnings: { projectId: string; projectName: string; message: string }[]
}

export interface IssueSummary {
  number: number
  title: string | null
  status: IssueStatus
  labels: string[]
  updatedAt: string | null
  url: string | null
}

export interface IssueDetail extends IssueSummary {
  identity: string
  body: string | null
}

export interface IssuePage {
  identity: string
  items: IssueSummary[]
  nextPage: number | null
  incomplete: boolean
}

export interface IssueProblem {
  code: string
  message: string
  scope?: 'source' | 'item'
}

interface IssueFailure {
  issue: IssueProblem
  identity?: string
}

export interface IssueQuery {
  sourceId: string
  state: IssueState
  query: string
  page: number
}

export function getIssueSources(signal: AbortSignal) {
  return apiRequest<IssueSources>('/api/issues/sources', { signal })
}

export function getIssuePage(query: IssueQuery, signal: AbortSignal) {
  return apiRequest<IssuePage | IssueFailure>('/api/issues/page', { body: query, signal })
}

export function getIssueDetail(sourceId: string, number: number, signal: AbortSignal) {
  return apiRequest<IssueDetail | IssueFailure>('/api/issues/detail', { body: { sourceId, number }, signal })
}
