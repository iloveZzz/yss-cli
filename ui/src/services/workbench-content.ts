import type { ContentView, QuestionView } from './workbench-model'
export function questionStateLabel(item: QuestionView): string {
  if (item.state === 'deferred' && item.response?.reviewWhen.trim()) return '已延期，需核对条件'
  if (item.state === 'open') return item.question.kind === 'research' ? '待补充' : '待你决定'
  return { pendingWriteback: '待回写', resolved: '已解决', deferred: '已延期', overdue: '复查到期', needsReview: '问题有变，待复查' }[item.state]
}
export function contentReminders(content: ContentView | undefined) {
  const questions = content?.questions ?? []
  const unanswered = (q: QuestionView) => q.state === 'open' || q.state === 'needsReview'
  return [
    { id: 'decision', label: '待你决定', count: questions.filter(q => unanswered(q) && q.question.kind === 'decision').length },
    { id: 'research', label: '待补充', count: questions.filter(q => unanswered(q) && q.question.kind === 'research').length },
    { id: 'overdue', label: '需要复查', count: questions.filter(q => q.state === 'overdue' || (q.state === 'deferred' && !!q.response?.reviewWhen.trim())).length },
    { id: 'writeback', label: '待回写', count: questions.filter(q => q.state === 'pendingWriteback').length },
    { id: 'ready', label: '可确认', count: content?.status === 'ready' ? 1 : 0 },
  ]
}
