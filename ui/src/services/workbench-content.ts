import type { ContentView, QuestionView } from './workbench-model'
export function questionStateLabel(item: QuestionView): string {
  if (item.state === 'deferred' && item.response?.reviewWhen.trim()) return '已延期，需核对条件'
  if (item.state === 'open') return item.question.kind === 'research' ? '待补充' : '待你决定'
  return {
    pendingWriteback: '待回写',
    resolved: '已解决',
    deferred: '已延期',
    overdue: '复查到期',
    needsReview: '问题有变，待复查'
  }[item.state]
}
export function canCopyQuestionResponse(item: QuestionView): boolean {
  return (
    !!item.response &&
    !!item.digest &&
    item.response.questionDigest === item.digest &&
    (item.state === 'pendingWriteback' || item.state === 'resolved' || item.state === 'deferred')
  )
}
export function formatQuestionReplies(
  sourcePath: string,
  items: QuestionView[],
  projectRoot?: string
): string {
  const replies = items.filter(canCopyQuestionResponse).flatMap((item) => {
    const response = item.response
    if (!response) return []
    const lines = [
      `问题 ID：${item.question.id}`,
      `问题：${item.question.title}`,
      `问题摘要：${item.digest}`,
      `答复 ID：${response.id}`,
      `状态：${questionStateLabel(item)}`
    ]
    if (response.kind === 'answer') {
      lines.push(
        `${item.question.kind === 'research' ? '已保存补充依据' : '已保存答复'}：\n${response.answer}`
      )
    } else {
      lines.push(
        '已保存延期安排：',
        `原因：${response.reason}`,
        `影响：${response.impact}`,
        `本次排除范围：${response.excludedScope}`
      )
      if (response.reviewAt) {
        const offset = response.reviewUtcOffsetMinutes
        const zone = `UTC${offset < 0 ? '-' : '+'}${String(Math.floor(Math.abs(offset) / 60)).padStart(2, '0')}:${String(Math.abs(offset) % 60).padStart(2, '0')}`
        lines.push(`复查日期：${response.reviewAt}（${zone}）`)
      }
      if (response.reviewWhen) lines.push(`复查触发条件：${response.reviewWhen}`)
    }
    return [lines.join('\n')]
  })
  if (!replies.length) return ''
  return [
    '请根据以下已保存的答复与延期安排，继续处理主规格及关联产物。',
    ...(projectRoot ? [`项目根目录：${projectRoot}`] : []),
    `主规格：${sourcePath}`,
    '',
    replies.join('\n\n'),
    '',
    '处理前核对问题 ID 与摘要；若问题已变化，先重新核对答复。请将答复落实到相关规格、验收条件、交互说明与原型，核验通过后才将问题 status 写为 resolved；已回写内容不重复实施。延期不算已解决，应保留原因、影响、排除范围与复查日期或条件，将问题 status 写为 deferred，不得写为 resolved；复查条件是否已触发须另行核对。答复与延期均须在 resolution 中记录非空的回写结论，并以 appliedDecisionId 引用对应答复 ID。不得自动确认整份规格。'
  ].join('\n')
}
export function matchesContentReminder(item: QuestionView, kind: string): boolean {
  if (kind === 'decision' || kind === 'research')
    return (
      (item.state === 'open' || item.state === 'needsReview') &&
      item.question.kind === (kind === 'decision' ? 'decision' : 'research')
    )
  if (kind === 'overdue')
    return (
      item.state === 'overdue' || (item.state === 'deferred' && !!item.response?.reviewWhen.trim())
    )
  if (kind === 'writeback') return item.state === 'pendingWriteback'
  return kind !== 'ready'
}
export function contentReminders(content: ContentView | undefined) {
  const questions = content?.questions ?? []
  const unanswered = (q: QuestionView) => q.state === 'open' || q.state === 'needsReview'
  return [
    {
      id: 'decision',
      label: '待你决定',
      count: questions.filter((q) => unanswered(q) && q.question.kind === 'decision').length
    },
    {
      id: 'research',
      label: '待补充',
      count: questions.filter((q) => unanswered(q) && q.question.kind === 'research').length
    },
    {
      id: 'overdue',
      label: '需要复查',
      count: questions.filter(
        (q) => q.state === 'overdue' || (q.state === 'deferred' && !!q.response?.reviewWhen.trim())
      ).length
    },
    {
      id: 'writeback',
      label: '待回写',
      count: questions.filter((q) => q.state === 'pendingWriteback').length
    },
    { id: 'ready', label: '可确认', count: content?.status === 'ready' ? 1 : 0 }
  ]
}
