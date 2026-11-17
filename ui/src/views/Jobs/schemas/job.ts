import {
  jobStatusLabels,
  objectValue,
  type JobEvent,
  type JobRecord,
  type JsonObject
} from '@/services/workbench-platform'

export const jobKindLabels: Record<string, string> = {
  agent: 'Codex 运行',
  engineering: '工程操作',
  externalRefresh: '外部关联读取',
  backup: '数据备份',
  restore: '数据恢复'
}
export interface ActivityEntry {
  id: string
  eventId: number
  createdAt: string
  kind: 'message' | 'command' | 'files' | 'status' | 'error'
  title: string
  text: string
  output: string
  status: string
}
function firstText(...values: unknown[]): string {
  return values.find((value): value is string => typeof value === 'string') ?? ''
}
function itemText(item: JsonObject): string {
  if (typeof item.text === 'string') return item.text
  if (!Array.isArray(item.content)) return ''
  return item.content.map((part) => firstText(objectValue(part).text)).join('\n')
}
export function eventText(event: JobEvent): string {
  const payload = objectValue(event.payload)
  const params = objectValue(payload.params)
  return firstText(
    payload.text,
    payload.delta,
    payload.output,
    payload.message,
    params.text,
    params.delta,
    objectValue(payload.delta).text,
    itemText(objectValue(payload.item))
  )
}
export function jobActivity(events: JobEvent[]): ActivityEntry[] {
  const entries: ActivityEntry[] = []
  const items = new Map<string, ActivityEntry>()
  let previousStatus = ''
  for (const event of events) {
    const payload = objectValue(event.payload)
    const item = objectValue(payload.item)
    const itemType = String(item.type ?? '')
    const itemId = String(payload.itemId ?? item.id ?? '')
    const key = `${String(payload.turnId ?? '')}:${itemId}`
    const isMessage = itemType === 'agentMessage' || /agentMessage\/delta$/.test(event.kind)
    const isCommand =
      itemType === 'commandExecution' || /commandExecution\/outputDelta$/.test(event.kind)
    const isFiles = itemType === 'fileChange'
    if (isMessage || isCommand || isFiles) {
      const entryKind = isMessage ? 'message' : isFiles ? 'files' : 'command'
      const id = `${entryKind}:${itemId ? key : event.id}`
      let entry = items.get(id)
      if (!entry) {
        entry = {
          id,
          eventId: event.id,
          createdAt: event.createdAt,
          kind: entryKind,
          title: isMessage ? 'Codex' : isFiles ? '修改文件' : '执行命令',
          text: '',
          output: '',
          status: ''
        }
        items.set(id, entry)
        entries.push(entry)
      }
      entry.eventId = event.id
      const status = String(item.status ?? entry.status)
      const statusLabels: Record<string, string> = {
        inProgress: '执行中',
        completed: '已完成',
        failed: '失败',
        declined: '已拒绝',
        interrupted: '已中断'
      }
      entry.status = statusLabels[status] ?? status
      if (isMessage) {
        const completeText = itemText(item)
        if (completeText) entry.text = completeText
        else entry.text += eventText(event)
      } else if (isFiles) {
        entry.text = Array.isArray(item.changes)
          ? item.changes
              .map((change) => firstText(objectValue(change).path))
              .filter(Boolean)
              .join('\n')
          : ''
      } else {
        const command = item.command
        if (command) entry.text = Array.isArray(command) ? command.join(' ') : String(command)
        if (typeof item.aggregatedOutput === 'string') entry.output = item.aggregatedOutput
        else if (event.kind.endsWith('/outputDelta')) entry.output += eventText(event)
        if (item.exitCode !== undefined && item.exitCode !== null)
          entry.status = Number(item.exitCode) === 0 ? '命令已完成' : `退出码 ${item.exitCode}`
      }
      continue
    }
    if (
      /tokenUsage|turnDiff|\/diff\/|reasoning|thread\/status|thread\/started|turn\/started/.test(
        event.kind
      )
    )
      continue
    let title = ''
    let text = eventText(event)
    let kind: ActivityEntry['kind'] = 'status'
    if (event.kind === 'hostThread') title = '已连接 Codex 会话'
    else if (event.kind === 'hostTurn') title = 'Codex 开始处理'
    else if (event.kind === 'run') title = payload.resumed ? '正在续接原运行' : '准备隔离工作区'
    else if (event.kind === 'inputRequested') title = '等待你的输入或授权'
    else if (event.kind === 'inputResolved') title = '已提交你的答复'
    else if (event.kind === 'plan') title = '已核对操作内容'
    else if (event.kind === 'query') {
      title = '读取外部关联'
      text = [payload.host, payload.project].filter(Boolean).join(' · ')
    } else if (
      event.kind === 'finished' ||
      event.kind === 'status' ||
      event.kind === 'stateChanged'
    ) {
      const status = String(payload.status ?? '')
      if (!status || status === previousStatus) continue
      previousStatus = status
      title = jobStatusLabels[status] ?? status
      text = firstText(objectValue(payload.error).message)
      if (text) kind = 'error'
    } else if (
      event.kind === 'error' ||
      event.kind === 'failed' ||
      event.kind === 'unsupportedRequest'
    ) {
      kind = 'error'
      title = event.kind === 'unsupportedRequest' ? '宿主请求需要处理' : '运行遇到问题'
      text ||= firstText(objectValue(payload.error).message, payload.method)
    } else if (text) title = '运行消息'
    else continue
    entries.push({
      id: `event:${event.id}`,
      eventId: event.id,
      createdAt: event.createdAt,
      kind,
      title,
      text,
      output: '',
      status: ''
    })
  }
  return entries.filter((entry) => entry.kind !== 'message' || !!entry.text)
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
    const status = [data.state, data.status, data.conclusion].filter((value) => typeof value === 'string')
    if (status.length) facts.push({ label: '平台状态', value: status.join(' · ') })
  }
  return facts
}
