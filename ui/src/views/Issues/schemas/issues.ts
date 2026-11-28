import { ApiError } from '@/services/workbench'
import type { IssuePlatform, IssueProblem, IssueSource, IssueState } from '@/services/workbench-issues'

export const issueStates: readonly { value: IssueState; label: string }[] = [
  { value: 'open', label: '打开' },
  { value: 'closed', label: '已关闭' },
  { value: 'all', label: '全部' }
]

export const platformLabels: Record<IssuePlatform, string> = { github: 'GitHub', gitlab: 'GitLab' }

const dateFormat = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23'
})

export function issueDate(value?: string | null) {
  const stamp = Date.parse(value ?? '')
  return Number.isFinite(stamp) ? dateFormat.format(stamp) : '时间不可用'
}

export function webAddress(value?: string | null) {
  if (!value || !/^https?:\/\//iu.test(value)) return undefined
  try {
    const url = new URL(value)
    if (!url.username && !url.password && url.host) return url
  } catch {
    return undefined
  }
}

export function issueUrl(value: string | null, source: IssueSource, number: number) {
  const url = webAddress(value)
  const repository = webAddress(source.url)
  if (!url || !repository || url.origin !== repository.origin || url.origin !== source.origin) return undefined
  // Require the selected repository and issue number as well as the configured instance.
  const base = repository.pathname.replace(/\/+$/u, '').toLowerCase()
  const routes = source.platform === 'github' ? ['/issues/'] : ['/-/issues/', '/-/work_items/']
  return routes.some(route => url.pathname.toLowerCase() === base + route + number) ? url.href : undefined
}

export function sourceHost(source: IssueSource) {
  const address = webAddress(source.origin)
  return address?.protocol === 'https:' ? address.host : source.origin
}

const issueLabels: Record<string, string> = {
  authentication: '需要认证',
  unauthorized: '需要认证',
  permission: '没有权限读取此仓库',
  forbidden: '没有权限读取此仓库',
  toolmissing: '本机平台连接不可用',
  missingtool: '本机平台连接不可用',
  identitychanged: '平台连接身份已变化',
  invalidsource: '仓库来源已变化',
  invalidrepository: '仓库来源已变化',
  ratelimit: '平台查询受限',
  ratelimited: '平台查询受限',
  limit: '平台查询受限',
  querylimit: '平台查询受限',
  invalidquery: '平台无法执行此查询',
  unavailable: '此 Issue 已不可用'
}

export function issueKey(issue: IssueProblem) {
  return issue.code.replace(/[-_]/gu, '').toLowerCase()
}

export function problemTitle(issue: IssueProblem, defaultTitle: string) {
  if (issue.scope === 'item') return '此 Issue 已不可用'
  return issueLabels[issueKey(issue)] ?? defaultTitle
}

export function needsSourceRefresh(issue: IssueProblem) {
  return ['invalidsource', 'invalidrepository'].includes(issueKey(issue))
}

export function retryReadLabel(issue: IssueProblem) {
  if (needsSourceRefresh(issue)) return '重新读取来源'
  const permission = ['authentication', 'unauthorized', 'permission', 'forbidden', 'identitychanged']
    .includes(issueKey(issue))
  return permission && issue.scope === 'source' ? '重新检查权限' : '重试读取'
}

export const sourceChanged: IssueProblem = {
  code: 'invalidSource', scope: 'source', message: '在「产品与工程」核对 Issue 跟踪仓库，或切换到其他来源。'
}

export const identityChanged: IssueProblem = {
  code: 'identityChanged', scope: 'source', message: '旧的读取结果已收起。确认平台账号后，重新读取当前来源。'
}

export function transportIssue(cause: unknown): IssueProblem {
  if (cause instanceof ApiError) {
    if (cause.status === 400) {
      return { code: 'invalidQuery', message: cause.message || '请确认查询条件。搜索内容最多允许 256 个字符。' }
    }
    if (cause.status === 401 || cause.status === 403) {
      return { code: 'permission', scope: 'source', message: '请核对当前平台连接的仓库访问权限，再重新读取。' }
    }
    if (cause.status === 404 || cause.status === 409) return sourceChanged
    if (cause.status === 429) return { code: 'rateLimited', message: '查询次数暂时受限，请稍后重试。' }
  }
  return { code: 'network', message: '无法完成本次读取。连接恢复后可按当前条件重试。' }
}
