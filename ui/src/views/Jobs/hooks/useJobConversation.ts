import { computed, onScopeDispose, shallowRef } from 'vue'
import { useEventListener } from '@vueuse/core'
import { errorMessage } from '@/services/workbench'
import {
  getJobConversation,
  objectValue,
  terminalJob,
  type ConversationPage,
  type JobActivity,
  type JobEvent
} from '@/services/workbench-platform'
import { activityKey, conversationEvent } from '../schemas/conversation'

export function useJobConversation(trackFirstText?: () => boolean) {
  const page = shallowRef<ConversationPage>()
  const items = shallowRef<JobActivity[]>([])
  const hasOlder = shallowRef(false)
  const historyError = shallowRef('')
  const loadingHistory = shallowRef(false)
  const firstText = shallowRef<{ jobId: string; rowId: number; receivedAt: number }>()
  const rows = new Map<number, JobActivity>()
  const keys = new Map<string, number>()
  let history: AbortController | undefined
  let buffered: JobEvent[] | undefined
  let cursor: number | undefined
  let frame: number | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  onScopeDispose(() => {
    history?.abort()
    cancelPublish()
  })
  useEventListener(document, 'visibilitychange', () => {
    if (frame === undefined && timer === undefined) return
    cancelPublish()
    schedulePublish()
  })

  function remember(row: JobActivity) {
    const key = row.itemId ? activityKey(row.kind, row.itemId) : undefined
    const knownId = key ? keys.get(key) : undefined
    const id = Math.min(knownId ?? row.id, row.id)
    const previous = rows.get(knownId ?? id)
    if (knownId !== undefined && knownId !== id) rows.delete(knownId)
    const current = previous && previous.updatedEventId > row.updatedEventId ? previous : row
    rows.set(id, current.id === id ? current : { ...current, id })
    if (key) keys.set(key, id)
    return id
  }

  function cancelPublish() {
    if (frame !== undefined) cancelAnimationFrame(frame)
    clearTimeout(timer)
    frame = undefined
    timer = undefined
  }

  function publish() {
    cancelPublish()
    items.value = [...rows.values()].sort((a, b) => a.id - b.id)
  }

  function schedulePublish() {
    if (frame !== undefined || timer !== undefined) return
    if (document.hidden) timer = setTimeout(publish, 32)
    else frame = requestAnimationFrame(publish)
  }

  function markFirstText(jobId: string, row: JobActivity, rowId: number) {
    if (!trackFirstText?.() || firstText.value || row.kind !== 'message' || !row.text.trim()) return
    const mark = performance.mark('yss.pi.first-text.received', { detail: { jobId } })
    firstText.value = { jobId, rowId, receivedAt: mark.startTime }
  }

  function ingest(event: JobEvent) {
    if (cursor === undefined || event.id <= cursor) return
    cursor = event.id
    const row = conversationEvent(event, (key) => rows.get(keys.get(key) ?? -1))
    if (row) {
      const rowId = remember(row)
      markFirstText(event.jobId, row, rowId)
      schedulePublish()
    }
    if ((frame !== undefined || timer !== undefined) && (event.kind === 'finished' ||
      terminalJob(String(objectValue(event.payload).status ?? '')))) publish()
  }

  async function open(id: string, signal: AbortSignal) {
    if (page.value) return
    const incoming: JobEvent[] = []
    buffered = incoming
    try {
      const snapshot = await getJobConversation(id, undefined, signal)
      if (signal.aborted) return
      for (const row of snapshot.items) {
        const rowId = remember(row)
        if (!snapshot.terminal) markFirstText(id, row, rowId)
      }
      cursor = snapshot.cursor
      page.value = snapshot
      hasOlder.value = snapshot.hasOlder
      for (const event of incoming) ingest(event)
      publish()
    } finally {
      if (buffered === incoming) buffered = undefined
    }
  }

  async function older(id: string) {
    const first = items.value[0]
    if (!first || !hasOlder.value || loadingHistory.value) return
    const request = new AbortController()
    history = request
    loadingHistory.value = true
    historyError.value = ''
    try {
      const snapshot = await getJobConversation(id, first.id, request.signal)
      if (request.signal.aborted) return
      for (const row of snapshot.items) remember(row)
      hasOlder.value = snapshot.hasOlder
      publish()
    } catch (cause) {
      if (!request.signal.aborted) historyError.value = errorMessage(cause)
    } finally {
      if (!request.signal.aborted) loadingHistory.value = false
    }
  }

  function accepted(requestId: string) {
    const id = keys.get(activityKey('question', requestId))
    const row = id === undefined ? undefined : rows.get(id)
    if (row?.status !== 'pending') return
    rows.set(row.id, { ...row, status: 'saving' })
    publish()
  }

  function reset() {
    history?.abort()
    cancelPublish()
    rows.clear()
    keys.clear()
    buffered = undefined
    cursor = undefined
    page.value = undefined
    items.value = []
    hasOlder.value = false
    historyError.value = ''
    loadingHistory.value = false
    firstText.value = undefined
  }

  return {
    page,
    items,
    hasOlder,
    historyError,
    loadingHistory,
    firstText,
    flushPending() {
      if (frame !== undefined || timer !== undefined) publish()
    },
    pending: computed(() => items.value.find((row) => row.kind === 'question' && row.status === 'pending')),
    getCursor: () => cursor,
    receive(event: JobEvent) {
      if (buffered) buffered.push(event)
      else ingest(event)
    },
    accepted,
    open,
    older,
    reset
  }
}
