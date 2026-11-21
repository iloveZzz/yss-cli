import type { ContentView, QuestionHistoryEntry, QuestionResponse, QuestionView } from './workbench-model'

function validReviewOffset(offset: QuestionResponse['reviewUtcOffsetMinutes']): offset is number {
  return typeof offset === 'number' && Number.isInteger(offset) && Math.abs(offset) <= 840
}

export function nextReviewDeadline(items: QuestionView[], readAt: string): number | undefined {
  const asOf = Date.parse(readAt)
  let next = Infinity
  for (const { response } of items) {
    if (response?.kind !== 'defer' || !response.reviewAt || !validReviewOffset(response.reviewUtcOffsetMinutes)) continue
    const deadline = Date.parse(`${response.reviewAt}T00:00:00Z`) - response.reviewUtcOffsetMinutes * 60_000
    if (deadline > asOf && deadline < next) next = deadline
  }
  return Number.isFinite(next) ? next : undefined
}

function reviewRequirement(item: QuestionView, response: QuestionResponse): { state: 'needsReview' | 'overdue'; reason: string } | undefined {
  if (!response.id || !response.questionDigest || !item.digest)
    return { state: 'needsReview', reason: '无法核实原答复 ID 或原问题摘要。' }
  if (response.questionDigest !== item.digest) return { state: 'needsReview', reason: '问题内容已变化，请核对当前问题。' }
  if (response.kind === 'defer') {
    if (item.question.blocking && response.excludedScope.trim() !== item.question.affectedScope.trim())
      return { state: 'needsReview', reason: '延期排除范围未覆盖当前关键问题。' }
    if (response.reviewAt) {
      if (!validReviewOffset(response.reviewUtcOffsetMinutes)) return { state: 'needsReview', reason: '原延期的复查时区未知。' }
      if (response.reviewAt <= dateAtOffset(response.reviewUtcOffsetMinutes))
        return { state: 'overdue', reason: '延期已到复查日期，需重新决定。' }
    }
    if (response.reviewWhen && item.question.status === 'open' && item.question.appliedDecisionId === response.id)
      return { state: 'needsReview', reason: '原规格已重新打开这项延期，请核对复查条件。' }
  }
  if (item.state === 'overdue') return { state: 'overdue', reason: '延期已到复查日期，需重新决定。' }
  if (item.state === 'needsReview' || item.state === 'open')
    return { state: 'needsReview', reason: '来源中的问题或延期安排需要复查。' }
}

export function dateAtOffset(minutes: number): string {
  return new Date(Date.now() + minutes * 60_000).toISOString().slice(0, 10)
}
export function reviewDateLabel(response: QuestionResponse): string {
  const offset = response.reviewUtcOffsetMinutes
  if (!validReviewOffset(offset)) return `${response.reviewAt}（时区未知）`
  const hours = String(Math.floor(Math.abs(offset) / 60)).padStart(2, '0')
  const minutes = String(Math.abs(offset) % 60).padStart(2, '0')
  return `${response.reviewAt}（UTC${offset < 0 ? '-' : '+'}${hours}:${minutes}）`
}
export function currentQuestionResponse(item: QuestionView): QuestionResponse | null {
  return item.response && !reviewRequirement(item, item.response) ? item.response : null
}
export function questionState(item: QuestionView): QuestionView['state'] {
  return (item.response && reviewRequirement(item, item.response)?.state) || item.state
}
export function questionHistory(item: QuestionView): QuestionHistoryEntry[] {
  const history = item.history ?? []
  const previous = item.response
  if (!previous) return history
  const reason = reviewRequirement(item, previous)?.reason
  return reason && !history.some((entry) => entry.response.id === previous.id)
    ? [{ response: previous, reason }, ...history]
    : history
}
export function responseText(response: QuestionResponse): string {
  if (response.kind === 'answer') return response.answer
  return [
    `延期原因：${response.reason || '未知'}`,
    `影响：${response.impact || '未知'}`,
    `本次排除范围：${response.excludedScope || '未知'}`,
    ...(response.reviewAt ? [`复查日期：${reviewDateLabel(response)}`] : []),
    ...(response.reviewWhen ? [`复查触发条件：${response.reviewWhen}`] : []),
    ...(!response.reviewAt && !response.reviewWhen ? ['复查日期或条件：未知'] : [])
  ].join('\n')
}
export function questionStateLabel(item: QuestionView): string {
  const state = questionState(item)
  if (state === 'deferred' && item.response?.reviewWhen.trim()) return '已延期，需核对条件'
  if (state === 'open') return item.question.kind === 'research' ? '待补充' : '待你决定'
  if (state === 'needsReview') {
    const previous = item.response ?? item.history?.[0]?.response
    if (previous?.questionDigest && item.digest && previous.questionDigest !== item.digest) return '问题有变，待复查'
    return previous?.kind === 'defer' ? '延期待复查' : '待复查'
  }
  return {
    pendingWriteback: '待回写',
    resolved: '已解决',
    deferred: '已延期',
    overdue: '复查到期'
  }[state]
}
export function canCopyQuestionResponse(item: QuestionView): boolean {
  return !!currentQuestionResponse(item)
}
export function responseUse(item: QuestionView, response: QuestionResponse): string {
  if (item.state === 'resolved' || item.state === 'deferred') return '已回写，引用已有结果'
  return response.kind === 'defer' ? '仅回写延期安排' : '回写业务答复'
}
export function formatQuestionReplies(
  sourcePath: string,
  items: QuestionView[],
  projectRoot?: string,
  context?: { specId: string; dataDir?: string }
): string {
  const replies = items.flatMap((item) => {
    const response = currentQuestionResponse(item)
    if (!response) return []
    const lines = [
      `问题 ID：${item.question.id}`,
      `问题：${item.question.title}`,
      `问题摘要：${item.digest}`,
      `涉及范围：${item.question.affectedScope || '未说明'}`,
      `答复 ID：${response.id}`,
      `状态：${questionStateLabel(item)} · ${responseUse(item, response)}`
    ]
    if (response.kind === 'answer') {
      lines.push(
        `${item.question.kind === 'research' ? '已保存补充依据' : '已保存答复'}：\n${response.answer}`
      )
    } else {
      lines.push(responseText(response))
      if (response.reviewWhen) lines.push('请核对复查条件。已触发时转入复查。')
    }
    if ((item.state === 'resolved' || item.state === 'deferred') && item.question.resolution)
      lines.push(`已回写结果：${item.question.resolution}`)
    return [lines.join('\n')]
  })
  if (!replies.length) return ''
  return [
    '请根据以下已保存的答复与延期安排，继续处理主规格及关联产物。',
    ...(projectRoot ? [`项目根目录：${projectRoot}`] : []),
    `主规格：${sourcePath}`,
    ...(context ? [`Spec ID：${context.specId}`] : []),
    ...(context?.dataDir ? [`后续工作台命令沿用 --data-dir：${context.dataDir}`] : []),
    '',
    replies.join('\n\n'),
    '',
    '先核对当前问题摘要、答复 ID、范围与复查期限。只处理仍待回写的记录。业务答复落实到相关规格、验收、交互说明及原型，核验后写为 resolved。延期仅回写原因、影响、排除范围与复查日期（含时区）或条件，写为 deferred。以 appliedDecisionId 引用实际采用的答复 ID，在 resolution 记录回写结果。已回写项引用已有条款，整份规格由用户另外确认。'
  ].join('\n')
}
export function matchesContentReminder(item: QuestionView, kind: string): boolean {
  const state = questionState(item)
  if (kind === 'decision' || kind === 'research')
    return (state === 'open' || state === 'needsReview') && item.question.kind === kind
  if (kind === 'overdue')
    return state === 'needsReview' || state === 'overdue' || (state === 'deferred' && !!item.response?.reviewWhen.trim())
  if (kind === 'writeback') return state === 'pendingWriteback'
  return kind !== 'ready'
}
export function contentReminders(content: ContentView | undefined) {
  const questions = content?.questions ?? []
  const reminders = [
    { id: 'decision', label: '待你决定' }, { id: 'research', label: '待补充' },
    { id: 'overdue', label: '需要复查' }, { id: 'writeback', label: '待回写' }
  ].map((item) => ({ ...item, count: questions.filter((q) => matchesContentReminder(q, item.id)).length }))
  return [...reminders, { id: 'ready', label: '可确认', count: content?.status === 'ready' ? 1 : 0 }]
}
