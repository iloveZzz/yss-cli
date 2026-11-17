import {
  stages,
  statuses,
  contentStatuses,
  stageProgressLabels,
  type ChangeEvent
} from './workbench'
export const historyFieldLabels: Record<string, string> = {
  name: '工程名称',
  rootPath: '工程路径',
  active: '启用状态',
  title: '标题',
  sourcePath: '主规格路径',
  stage: '关注阶段',
  status: '状态',
  nextAction: '下一步',
  blocker: '阻塞说明',
  notes: '备注',
  resources: '关联资源',
  inferredResources: '识别到的关联资源',
  sourceResourceTargets: '主规格资源归属',
  progress: '阶段进度',
  note: '阶段说明',
  id: '记录标识',
  projectId: '所属工程',
  specId: '主规格标识',
  taskId: '任务标识',
  createdAt: '创建时间',
  updatedAt: '更新时间',
  lastVisitedAt: '访问时间',
  questionId: '问题标识',
  questionDigest: '问题摘要',
  appliedDecisionId: '采用的答复',
  resolution: '回写结论',
  kind: '记录类型',
  responseKind: '处理方式',
  answer: '答复',
  reason: '延期原因',
  impact: '影响范围',
  excludedScope: '排除范围',
  reviewAt: '复查日期',
  reviewWhen: '复查条件',
  reviewUtcOffsetMinutes: '复查时区',
  scope: '确认范围',
  artifactDigest: '内容版本',
  contentStatus: '内容状态',
  label: '名称',
  target: '目标路径',
  targetType: '资源位置',
  sourceDirectories: '来源目录',
  schemaVersion: '数据结构版本'
}
export function historyFieldLabel(field: string): string {
  return historyFieldLabels[field] ?? '其他记录'
}
export function eventDescription(event: ChangeEvent): string {
  const fields = Object.keys(event.changes)
  if (event.lifecycleStage) return `更新${stages[event.lifecycleStage]}阶段记录`
  if (fields.includes('artifactDigest') && fields.includes('scope')) return '确认内容版本与范围'
  if (fields.includes('questionId'))
    return event.changes.kind?.after === 'defer' ? '保存问题延期安排' : '保存问题答复'
  if (
    fields.includes('lastVisitedAt') &&
    fields.every((field) => ['lastVisitedAt', 'updatedAt'].includes(field))
  )
    return '访问规格'
  if (event.specId && fields.includes('sourcePath') && event.changes.sourcePath?.before == null)
    return '关联主规格'
  if (!event.specId && fields.includes('rootPath') && event.changes.rootPath?.before == null)
    return '登记工程'
  if (fields.includes('sourceResourceTargets') || fields.includes('inferredResources'))
    return '刷新主规格关联内容'
  if (fields.includes('active') && fields.every((field) => ['active', 'updatedAt'].includes(field)))
    return event.changes.active?.after ? '恢复启用记录' : '停用记录'
  const meaningful = fields.filter(
    (field) => !['id', 'projectId', 'specId', 'taskId', 'createdAt', 'updatedAt'].includes(field)
  )
  const labels = [...new Set(meaningful.map(historyFieldLabel))]
  return labels.length ? `更新${labels.join('、')}` : '更新记录'
}
export function historyValue(field: string, value: unknown): string {
  if (value == null) return '未设置'
  if (field === 'progress' && typeof value === 'string')
    return stageProgressLabels[value as keyof typeof stageProgressLabels] ?? value
  if (field === 'stage' && typeof value === 'string')
    return stages[value as keyof typeof stages] ?? value
  if (field === 'status' && typeof value === 'string')
    return (
      statuses[value as keyof typeof statuses] ??
      contentStatuses[value as keyof typeof contentStatuses] ??
      value
    )
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'string') return value || '空'
  if (Array.isArray(value))
    return value.length
      ? value
          .map((item) => {
            if (typeof item === 'string') return item
            if (item && typeof item === 'object') {
              const resource = item as Record<string, unknown>
              return [resource.label, resource.target].filter(Boolean).join(' · ') || '关联记录'
            }
            return String(item)
          })
          .join('\n')
      : '无'
  return JSON.stringify(value)
}
