import { computed, onScopeDispose, shallowRef } from 'vue'
import { errorMessage } from '@/services/workbench'
import {
  getJobConversation,
  type ConversationPage,
  type JobActivity,
  type JobEvent
} from '@/services/workbench-platform'
import { activityKey, conversationEvent } from '../schemas/conversation'

export function useJobConversation() {
  const page = shallowRef<ConversationPage>()
  const items = shallowRef<JobActivity[]>([])
  const hasOlder = shallowRef(false)
  const historyError = shallowRef('')
  const loadingHistory = shallowRef(false)
  const rows = new Map<number, JobActivity>()
  const keys = new Map<string, number>()
  let history: AbortController | undefined
  let buffered: JobEvent[] | undefined
  let cursor: number | undefined
  let published = true
  onScopeDispose(() => history?.abort())

  function remember(row: JobActivity) {
    const key = row.itemId ? activityKey(row.kind, row.itemId) : undefined
    const knownId = key ? keys.get(key) : undefined
    const id = Math.min(knownId ?? row.id, row.id)
    const previous = rows.get(knownId ?? id)
    if (knownId !== undefined && knownId !== id) rows.delete(knownId)
    const current = previous && previous.updatedEventId > row.updatedEventId ? previous : row
    rows.set(id, current.id === id ? current : { ...current, id })
    if (key) keys.set(key, id)
  }

  function publish() {
    published = true
    items.value = [...rows.values()].sort((a, b) => a.id - b.id)
  }

  function ingest(event: JobEvent) {
    if (cursor === undefined || event.id <= cursor) return
    cursor = event.id
    const row = conversationEvent(event, (key) => rows.get(keys.get(key) ?? -1))
    if (row) {
      remember(row)
      if (published) {
        published = false
        queueMicrotask(publish)
      }
    }
  }

  async function open(id: string, signal: AbortSignal) {
    if (page.value) return
    const incoming: JobEvent[] = []
    buffered = incoming
    try {
      const snapshot = await getJobConversation(id, undefined, signal)
      if (signal.aborted) return
      for (const row of snapshot.items) remember(row)
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
    rows.clear()
    keys.clear()
    buffered = undefined
    cursor = undefined
    page.value = undefined
    items.value = []
    hasOlder.value = false
    historyError.value = ''
    loadingHistory.value = false
  }

  return {
    page,
    items,
    hasOlder,
    historyError,
    loadingHistory,
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
