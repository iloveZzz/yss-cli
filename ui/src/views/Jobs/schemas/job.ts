import {
  jobStatusLabels,
  objectValue,
  type JobActivity,
  type JobRecord
} from '@/services/workbench-platform'

export const jobKindLabels: Record<string, string> = {
  agent: 'Agent 运行',
  engineering: '工程操作',
  externalRefresh: '外部关联读取',
  backup: '数据备份',
  restore: '数据恢复'
}
// Older job titles contain the Spec request digest as a generated suffix.
export function jobTitle(job: JobRecord): string {
  if (job.kind === 'agent') {
    const parts = /^Spec 续办 · (.+) · [0-9a-f]{8}$/s.exec(job.title)
    if (parts) return `${parts[1]} · Spec 续办`
  }
  return job.title
}
export const activityLabels = {
  message: 'Agent 答复',
  user: '用户消息',
  info: '运行消息',
  command: '执行命令',
  files: '文件操作',
  tool: '文件工具',
  question: '业务提问',
  status: '状态变化',
  error: '执行异常'
}
export function stepResult(row: JobActivity) {
  if (row.exitCode != null) return row.exitCode === 0 ? '已完成' : `退出码 ${row.exitCode}`
  const states: Record<string, string> = {
    inProgress: '执行中',
    declined: '已拒绝',
    pending: '待答复',
    saving: '正在保存',
    saved: '已保存',
    ...jobStatusLabels
  }
  return states[row.status] ?? (row.status || '—')
}
function firstText(...values: unknown[]): string {
  return values.find((value): value is string => typeof value === 'string') ?? ''
}
export function resultFacts(job: JobRecord): { label: string; value: string }[] {
  const result = objectValue(job.result)
  const facts: { label: string; value: string }[] = []
  const fields = {
    root: '目标目录',
    targetPath: '保存位置',
    archivePath: '备份文件',
    targetVersion: '目标版本',
    diskVersion: '磁盘版本'
  }
  for (const [key, label] of Object.entries(fields)) {
    const value = result[key]
    if (typeof value === 'string' && value) facts.push({ label, value })
  }
  if (job.kind === 'backup' && !result.archivePath && typeof result.path === 'string')
    facts.push({ label: '备份文件', value: result.path })
  const transaction = objectValue(result.transaction)
  if (transaction.cleanup === 'pending' || result.cleanup === 'pending')
    facts.push({ label: '后续处理', value: '操作已记录，仍需恢复或清理事务材料。' })
  if (result.restartRequired)
    facts.push({ label: '后续处理', value: '程序已更新，重启工作台后使用新版本。' })
  if (Array.isArray(result.changes))
    facts.push({ label: '文件变化', value: `${result.changes.length} 项` })
  if (typeof result.message === 'string') facts.push({ label: '结果说明', value: result.message })
  if (Array.isArray(result.warnings)) {
    for (const warning of result.warnings) {
      const message = firstText(warning, objectValue(warning).message, objectValue(warning).reason)
      if (message) facts.push({ label: '注意事项', value: message })
    }
  }
  if (job.kind === 'externalRefresh') {
    const data = objectValue(result.data)
    if (typeof data.title === 'string') facts.push({ label: '关联事项', value: data.title })
    const status = [data.state, data.status, data.conclusion].filter(
      (value) => typeof value === 'string'
    )
    if (status.length) facts.push({ label: '平台状态', value: status.join(' · ') })
  }
  return facts
}
