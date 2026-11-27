import { computed, onScopeDispose, shallowReactive, shallowRef, type Ref } from 'vue'
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

type FirstText = { jobId: string; rowId: number; eventId: number; source: 'snapshot' | 'stream' }

export function isConversationTextDelta(event: JobEvent) {
  return event.kind === 'item/agentMessage/delta' || event.kind === 'item/piReasoning/delta'
}

function hasLiveText(row: JobActivity) {
  return (row.kind === 'message' && !row.status) || (row.kind === 'reasoning' && row.status === 'inProgress')
}

export function useJobConversation(jobId: Readonly<Ref<string>>, trackFirstText?: () => boolean) {
  const page = shallowRef<ConversationPage>()
  const items = shallowRef<JobActivity[]>([])
  const hasOlder = shallowRef(false)
  const historyError = shallowRef('')
  const loadingHistory = shallowRef(false)
  const firstText = shallowRef<FirstText & { receivedAt: number }>()
  const toolMotion = shallowRef<ReadonlyMap<number, 'enter' | 'settled'>>()
  const rows = new Map<number, JobActivity>()
  const keys = new Map<string, number>()
  // Canonical snapshots handle event order. Only a live row's displayed body is reactive.
  const displayed = new Map<number, { source: JobActivity; row: JobActivity }>()
  const pendingText = new Map<number, JobActivity>()
  const trackStructure = new URLSearchParams(location.search).get('piTimings') === '1' ||
    Reflect.get(globalThis, '__YSS_PI_TIMINGS__') === true
  let structureChanged = false
  let pendingToolMotion = new Map<number, 'enter' | 'settled'>()
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

  function remember(row: JobActivity, textOnly = false) {
    const key = row.itemId ? activityKey(row.kind, row.itemId) : undefined
    const knownId = key ? keys.get(key) : undefined
    const id = Math.min(knownId ?? row.id, row.id)
    const previous = rows.get(knownId ?? id)
    if (knownId !== undefined && knownId !== id) {
      rows.delete(knownId)
      displayed.delete(knownId)
    }
    const current = previous && previous.updatedEventId > row.updatedEventId ? previous : row
    const entry = current.id === id ? current : { ...current, id }
    if (entry === previous) return id
    rows.set(id, entry)
    if (key) keys.set(key, id)
    if (textOnly && !structureChanged && displayed.has(id) && hasLiveText(entry) &&
      previous?.status === entry.status && previous?.durationMs === entry.durationMs) {
      pendingText.set(id, entry)
    } else {
      structureChanged = true
    }
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
    for (const [id, source] of pendingText) {
      const view = displayed.get(id)
      if (view && rows.get(id) === source) {
        view.row.text = source.text
        view.row.updatedEventId = source.updatedEventId
        view.source = source
      }
    }
    pendingText.clear()
    if (structureChanged) {
      structureChanged = false
      items.value = [...rows.values()].sort((a, b) => a.id - b.id).map((source) => {
        if (!hasLiveText(source)) {
          displayed.delete(source.id)
          return source
        }
        const view = displayed.get(source.id)
        if (view?.source === source) return view.row
        const row = shallowReactive({ ...source })
        displayed.set(source.id, { source, row })
        return row
      })
      if (trackStructure) performance.mark('yss.pi.conversation.structure', { detail: { jobId: jobId.value } })
    }
    if (pendingToolMotion.size) {
      toolMotion.value = pendingToolMotion
      pendingToolMotion = new Map()
    }
  }

  function schedulePublish() {
    if (frame !== undefined || timer !== undefined) return
    if (document.hidden) timer = setTimeout(publish, 32)
    else frame = requestAnimationFrame(publish)
  }

  function markFirstText(row: JobActivity, rowId: number, source: FirstText['source']) {
    if (!trackFirstText?.() || firstText.value || row.kind !== 'message' || !row.text.trim()) return
    const detail = { jobId: jobId.value, rowId, eventId: row.updatedEventId, source }
    const mark = performance.mark('yss.pi.first-text.received', { detail })
    firstText.value = { ...detail, receivedAt: mark.startTime }
  }

  function ingest(event: JobEvent, animate = false) {
    if (cursor === undefined || event.id <= cursor) return
    cursor = event.id
    const row = conversationEvent(event, (key) => rows.get(keys.get(key) ?? -1))
    if (row) {
      const previous = rows.get(row.id)
      const rowId = remember(row, isConversationTextDelta(event))
      if (animate && ['command', 'tool', 'files'].includes(row.kind)) {
        if (!previous) pendingToolMotion.set(rowId, 'enter')
        else if (previous.status === 'inProgress' && row.status !== previous.status && !pendingToolMotion.has(rowId))
          pendingToolMotion.set(rowId, 'settled')
      }
      markFirstText(row, rowId, 'stream')
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
        markFirstText(row, rowId, 'snapshot')
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
    structureChanged = true
    publish()
  }

  function reset() {
    history?.abort()
    cancelPublish()
    rows.clear()
    keys.clear()
    displayed.clear()
    pendingText.clear()
    structureChanged = false
    buffered = undefined
    cursor = undefined
    page.value = undefined
    items.value = []
    hasOlder.value = false
    historyError.value = ''
    loadingHistory.value = false
    firstText.value = undefined
    pendingToolMotion.clear()
    toolMotion.value = undefined
  }

  return {
    page,
    items,
    hasOlder,
    historyError,
    loadingHistory,
    firstText,
    toolMotion,
    flushPending() {
      if (frame !== undefined || timer !== undefined) publish()
    },
    pending: computed(() => items.value.find((row) => row.kind === 'question' && row.status === 'pending')),
    getCursor: () => cursor,
    receive(event: JobEvent, animate = false) {
      if (buffered) buffered.push(event)
      else ingest(event, animate)
    },
    accepted,
    open,
    older,
    reset
  }
}
