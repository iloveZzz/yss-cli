import { ApiError } from '@/services/workbench'
import type {
  MergeRequestIssue,
  MergeRequestOrder,
  MergeRequestPlatform,
  MergeRequestRepository,
  MergeRequestStatus,
  MergeRequestSummary
} from '@/services/workbench-merge-requests'

export type MergeRequestTab = Exclude<MergeRequestStatus, 'unknown'> | 'all'

export interface MergeRequestRow {
  repository: MergeRequestRepository
  request: MergeRequestSummary
}

export const tabs: readonly { value: MergeRequestTab; label: string }[] = [
  { value: 'open', label: '开放中' },
  { value: 'merged', label: '已合并' },
  { value: 'closed', label: '已关闭' },
  { value: 'all', label: '全部' }
]

export const platformLabels: Record<MergeRequestPlatform, string> = { github: 'GitHub', gitlab: 'GitLab' }

export function repositoryHost(repository: MergeRequestRepository) {
  try {
    return new URL(repository.url).host || repository.origin
  } catch {
    return repository.origin
  }
}

export function repositoryLabel(repository: MergeRequestRepository) {
  return `${platformLabels[repository.platform]} · ${repositoryHost(repository)} · ${repository.slug}`
}

export function requestNumber(platform: MergeRequestPlatform, number: number) {
  return `${platform === 'gitlab' ? '!' : '#'}${number}`
}

export function requestKey(row: MergeRequestRow) {
  return `${row.repository.id}/${row.request.number}`
}

const dateFormat = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23'
})

export function dateLabel(value?: string | null) {
  const stamp = value ? Date.parse(value) : NaN
  return Number.isFinite(stamp) ? dateFormat.format(stamp) : '不可用'
}

export function sortTimestamp(request: MergeRequestSummary, order: MergeRequestOrder) {
  const stamp = Date.parse((order === 'updated' ? request.updatedAt : request.createdAt) ?? '')
  return Number.isFinite(stamp) ? stamp : -Infinity
}

export function compareRows(left: MergeRequestRow, right: MergeRequestRow, order: MergeRequestOrder) {
  const leftTime = sortTimestamp(left.request, order)
  const rightTime = sortTimestamp(right.request, order)
  if (leftTime !== rightTime) return leftTime > rightTime ? -1 : 1
  const leftId = left.repository.id
  const rightId = right.repository.id
  if (leftId !== rightId) return leftId < rightId ? -1 : 1
  return right.request.number - left.request.number
}

export function matchesQuery(row: MergeRequestRow, query: string) {
  const number = /^([#!]?)(\d+)$/u.exec(query)
  if (!number) return !query || (row.request.title ?? '').toLocaleLowerCase().includes(query)
  if (number[1] === '#' && row.repository.platform !== 'github') return false
  if (number[1] === '!' && row.repository.platform !== 'gitlab') return false
  return row.request.number === Number(number[2])
}

export function safeHttpUrl(value?: string | null, sameOriginAs?: string) {
  if (!value) return undefined
  try {
    const parsed = new URL(value)
    if (!['https:', 'http:'].includes(parsed.protocol) || parsed.username || parsed.password) return undefined
    if (sameOriginAs && parsed.origin !== new URL(sameOriginAs).origin) return undefined
    return parsed.href
  } catch {
    return undefined
  }
}

export function repositoryRequestsUrl(repository: MergeRequestRepository) {
  const base = safeHttpUrl(repository.url)
  if (!base) return undefined
  const url = new URL(base)
  const path = repository.platform === 'github' ? '/pulls' : '/-/merge_requests'
  url.pathname = url.pathname.replace(/\/+$/u, '') + path
  url.search = ''
  url.hash = ''
  return url.href
}

const issueLabels: Record<string, string> = {
  authentication: '需要认证',
  unauthorized: '需要认证',
  permission: '无读取权限',
  forbidden: '无读取权限',
  toolmissing: '缺少本机工具',
  missingtool: '缺少本机工具',
  unavailable: '暂不可读',
  network: '网络读取失败',
  ratelimited: '平台限流',
  ratelimit: '平台限流',
  limit: '平台读取范围受限',
  limited: '平台读取范围受限',
  rangelimited: '平台读取范围受限',
  platformlimit: '平台读取范围受限',
  identitychanged: '连接身份已变化',
  notfound: '找不到请求或仓库',
  invalidsource: '仓库来源已变化',
  invalidrepository: '仓库来源已变化',
  readfailed: '读取失败'
}

function issueKey(issue: MergeRequestIssue) {
  return issue.code.replace(/[-_]/g, '').toLowerCase()
}

export function issueLabel(issue: MergeRequestIssue) {
  return issueLabels[issueKey(issue)] ?? '读取失败'
}

export function needsConnection(issue: MergeRequestIssue) {
  return ['authentication', 'unauthorized', 'permission', 'forbidden', 'toolmissing', 'missingtool'].includes(issueKey(issue))
}

export function isTemporary(issue: MergeRequestIssue) {
  return ['network', 'ratelimit', 'ratelimited'].includes(issueKey(issue))
}

export function losesAccess(issue: MergeRequestIssue) {
  return needsConnection(issue) || ['identitychanged', 'invalidsource', 'invalidrepository'].includes(issueKey(issue))
}

export function transportIssue(cause: unknown): MergeRequestIssue {
  if (cause instanceof ApiError) {
    if (cause.status === 403) return { code: 'permission', message: '检查本机连接是否有权访问仓库。' }
    if (cause.status === 404 || cause.status === 409) {
      return { code: 'invalidSource', message: '刷新仓库来源后重新读取。' }
    }
    if (cause.status === 429) return { code: 'rateLimited', message: '稍后重试，或在原站继续查找。' }
  }
  return { code: 'network', message: '无法完成本次读取，可以重试。' }
}
