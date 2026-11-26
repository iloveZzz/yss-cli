import { objectValue, type JobActivity, type JobEvent } from '@/services/workbench-platform'

const text = (value: unknown) => (typeof value === 'string' ? value : '')

export function isPiQuestion(event: JobEvent) {
  return objectValue(objectValue(event.payload).request).protocolMethod === 'pi/askSpecQuestion'
}

export function activityKey(kind: JobActivity['kind'], itemId: string) {
  return `${kind}:${itemId}`
}

export function conversationEvent(
  event: JobEvent,
  lookup: (key: string) => JobActivity | undefined
): JobActivity | undefined {
  const payload = objectValue(event.payload)
  const item = objectValue(payload.item)
  const row = (kind: JobActivity['kind'], body: string, status: string): JobActivity => ({
    id: event.id,
    updatedEventId: event.id,
    createdAt: event.createdAt,
    kind,
    text: body,
    status,
    exitCode: null
  })
  const find = (kind: JobActivity['kind'], id: string) => lookup(activityKey(kind, id))
  const update = (previous: JobActivity, changes: Partial<JobActivity>) =>
    event.id > previous.updatedEventId
      ? { ...previous, ...changes, updatedEventId: event.id }
      : undefined

  if (event.kind === 'inputRequested' && isPiQuestion(event)) {
    const params = objectValue(objectValue(payload.request).params)
    const detail = objectValue(Array.isArray(params.questions) ? params.questions[0] : null)
    const requestId = text(payload.requestId)
    const questionId = text(detail.id)
    if (!requestId || !questionId || !text(detail.question)) return
    const options = Array.isArray(detail.options)
      ? detail.options.map((option: unknown) => text(objectValue(option).label)).filter(Boolean)
      : []
    return {
      ...row('question', text(detail.question), 'pending'),
      itemId: requestId,
      question: {
        requestId,
        questionId,
        reason: text(params.reason),
        options,
        answer: null,
        responseId: null,
        error: null
      }
    }
  }
  if (event.kind === 'inputResolved' || event.kind === 'piQuestionResult') {
    const previous = find('question', text(payload.requestId))
    if (!previous?.question) return
    if (event.kind === 'inputResolved') return update(previous, { status: 'saving' })
    const responseId = text(payload.responseId) || null
    const answer = text(payload.answer) || null
    return update(previous, {
      status: responseId && answer ? 'saved' : 'failed',
      question: { ...previous.question, responseId, answer, error: text(payload.error) || null }
    })
  }
  if (event.kind === 'piTool') {
    return {
      ...row('tool', text(payload.message), text(payload.status) || 'completed'),
      itemId: text(payload.id) || undefined,
      operation: text(payload.operation) || undefined,
      projectId: text(payload.projectId) || undefined,
      path: text(payload.reviewPath) || text(payload.path) || undefined,
      displayPath: text(payload.displayPath) || undefined,
      error: text(payload.error) || undefined,
      errorCode: text(payload.errorCode) || undefined,
      hint: text(payload.hint) || undefined
    }
  }
  const itemId = text(item.id) || text(payload.itemId)
  if (itemId && event.kind === 'item/commandExecution/outputDelta') {
    const previous = find('command', itemId)
    return previous ? update(previous, {}) : undefined
  }
  if (itemId && (item.type === 'piReasoning' || event.kind === 'item/piReasoning/delta')) {
    const previous = find('reasoning', itemId)
    const delta = event.kind === 'item/piReasoning/delta'
    const status = text(item.status) || (event.kind === 'item/completed' ? 'completed' : 'inProgress')
    const body = typeof item.text === 'string' ? item.text : (previous?.text ?? '') + (delta ? text(payload.delta) : '')
    const durationMs = typeof item.durationMs === 'number' ? item.durationMs : previous?.durationMs
    return previous
      ? update(previous, { text: body, status, durationMs })
      : { ...row('reasoning', body, status), itemId, durationMs }
  }
  if (itemId && item.type === 'piCompaction') {
    const kind = 'tool'
    const previous = find(kind, itemId)
    const status = text(item.status) || (event.kind === 'item/started' ? 'inProgress' : 'completed')
    const body = text(item.text)
    const details = { operation: text(item.operation) || undefined }
    return previous
      ? update(previous, { text: body, status, ...details })
      : { ...row(kind, body, status), itemId, ...details }
  }
  if (itemId && (event.kind === 'item/agentMessage/delta' || item.type === 'agentMessage')) {
    const previous = find('message', itemId)
    const delta = event.kind === 'item/agentMessage/delta'
    const body = delta ? (previous?.text ?? '') + text(payload.delta) : text(item.text)
    const status = delta ? '' : 'completed'
    return previous
      ? update(previous, { text: body, status })
      : { ...row('message', body, status), itemId }
  }
  if (item.type === 'commandExecution' && itemId) {
    const status = text(item.status) || (event.kind === 'item/started' ? 'inProgress' : 'completed')
    const previous = find('command', itemId)
    const command = text(item.command)
    const exitCode = typeof item.exitCode === 'number' ? item.exitCode : null
    return previous
      ? update(previous, { status, exitCode })
      : { ...row('command', command, status), itemId, exitCode }
  }
  if (item.type === 'fileChange' && Array.isArray(item.changes)) {
    const paths = item.changes.map((change: unknown) => text(objectValue(change).path)).filter(Boolean)
    return {
      ...row('files', paths.join('\n'), text(item.status) || 'completed'),
      itemId,
      projectId: text(item.projectId) || undefined,
      path: text(item.reviewPath) || undefined,
      operation: text(item.operation) || undefined
    }
  }
  const error = text(payload.message) || text(objectValue(payload.error).message)
  if (['error', 'failed', 'progressError', 'unsupportedRequest'].includes(event.kind)) {
    return row('error', error || '运行遇到问题', text(payload.status))
  }
  if (error && ['status', 'stateChanged', 'finished'].includes(event.kind)) {
    return row('error', error, text(payload.status))
  }
}
