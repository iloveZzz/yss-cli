import { computed, shallowRef, watch, type Ref } from 'vue'
import { ApiError, errorMessage } from '@/services/workbench'
import { getJobActivities, objectValue, piPhaseLabels, streamJob, type ActivityPage, type PiPhase } from '@/services/workbench-platform'
import { isConversationTextDelta, useJobConversation } from './useJobConversation'

// Read a consistent snapshot, then subscribe strictly after its persisted event cursor.
// Reconnection repeats that handshake, including pending inputs and already-started commands.
export function useJobEvents(
  jobId: Readonly<Ref<string>>, updated: () => void,
  options: { piRound?: boolean; knownPiJob?: Readonly<Ref<boolean>>; trackFirstText?: () => boolean } = {}
) {
  const latest = shallowRef<ActivityPage>()
  const error = shallowRef('')
  const statusLabel = shallowRef('')
  const retryKey = shallowRef(0)
  const livePlanEventId = shallowRef<number>()
  const livePhase = shallowRef<{ id: number; phase: PiPhase; at?: string }>()
  const conversation = useJobConversation(jobId, options.trackFirstText)
  watch(
    // Parent refreshes may replace a job record without changing the subscribed job.
    [() => jobId.value, () => retryKey.value, () => options.knownPiJob?.value],
    ([id, , knownPiJob], previous, onCleanup) => {
      if (previous?.[0] !== id) {
        latest.value = undefined
        livePlanEventId.value = undefined
        livePhase.value = undefined
        conversation.reset()
      }
      error.value = ''
      if (!id) return
      const connection = new AbortController()
      const { signal } = connection
      let timer: ReturnType<typeof setTimeout> | undefined
      let syncing: Promise<void> | undefined
      let observed = 0
      let overviewObserved = 0
      onCleanup(() => {
        connection.abort()
        clearTimeout(timer)
      })

      async function readOverview() {
        const page = await getJobActivities(id, undefined, signal)
        if (signal.aborted) return
        const prior = latest.value
        if (!prior || page.cursor >= prior.cursor) {
          latest.value = page
          if (prior?.runId !== page.runId || prior?.terminal !== page.terminal) updated()
        }
        return page.host
      }
      async function sync() {
        if (options.piRound || knownPiJob) {
          // The Pi round already knows its host. Each snapshot keeps its own cursor.
          const results = await Promise.allSettled([readOverview(), conversation.open(id, signal)])
          for (const result of results) if (result.status === 'rejected') throw result.reason
        } else if ((await readOverview()) === 'pi' && !signal.aborted) {
          await conversation.open(id, signal)
        }
        if (!signal.aborted) error.value = ''
      }
      function flush() {
        if (!syncing)
          syncing = sync().finally(() => {
            syncing = undefined
          })
        return syncing
      }
      function schedule(delay = 350) {
        if (timer || signal.aborted) return
        timer = setTimeout(async () => {
          timer = undefined
          if (overviewObserved <= (latest.value?.cursor ?? 0)) return
          try {
            await flush()
          } catch (cause) {
            if (!signal.aborted) {
              error.value = errorMessage(cause)
              schedule(1500)
            }
            return
          }
          if (overviewObserved > (latest.value?.cursor ?? 0)) schedule()
        }, delay)
      }
      async function connect() {
        try {
          livePlanEventId.value = undefined
          statusLabel.value = latest.value ? '正在重连' : '正在读取记录'
          await flush()
          if (signal.aborted) return
          const after = conversation.getCursor() ?? latest.value?.cursor ?? 0
          if (!latest.value?.terminal || after < latest.value.cursor) {
            // Any older events delivered after this handshake are a snapshot backfill.
            const snapshotCursor = Math.max(after, latest.value?.cursor ?? 0)
            statusLabel.value = '实时连接'
            await streamJob(id, after, signal, (event) => {
              if (signal.aborted) return
              observed = Math.max(observed, event.id)
              const animate = !!options.piRound && event.id > snapshotCursor
              if (animate && event.kind === 'piPlan') livePlanEventId.value = event.id
              if (event.kind === 'piPhase' && event.id > Math.max(latest.value?.cursor ?? 0, livePhase.value?.id ?? 0)) {
                const { phase, at } = objectValue(event.payload)
                if (typeof phase === 'string' && Object.hasOwn(piPhaseLabels, phase)) {
                  livePhase.value = { id: event.id, phase: phase as PiPhase, at: typeof at === 'string' ? at : undefined }
                  updated()
                }
              }
              conversation.receive(event, animate)
              if (!options.piRound || !isConversationTextDelta(event)) {
                overviewObserved = Math.max(overviewObserved, event.id)
                if (overviewObserved > (latest.value?.cursor ?? 0)) schedule()
              }
              if (
                [
                  'status',
                  'stateChanged',
                  'finished',
                  'inputRequested',
                  'inputResolved',
                  'piQuestionResult',
                  'run'
                ].includes(event.kind)
              )
                updated()
            }).finally(() => {
              if (!signal.aborted) conversation.flushPending()
            })
            await flush()
            // A scheduled read could have started before the last event was committed.
            if (!signal.aborted && observed > (latest.value?.cursor ?? 0)) await flush()
          }
          if (signal.aborted) return
          if (latest.value?.terminal && (conversation.getCursor() ?? latest.value.cursor) >= latest.value.cursor) {
            statusLabel.value = '已读取记录'
            return
          }
        } catch (cause) {
          if (signal.aborted) return
          error.value = errorMessage(cause)
          if (cause instanceof ApiError && [401, 403, 404].includes(cause.status)) {
            statusLabel.value = '读取失败'
            return
          }
        }
        if (!signal.aborted) {
          statusLabel.value = '正在重连'
          reconnect = setTimeout(() => void connect(), 1500)
        }
      }
      let reconnect: ReturnType<typeof setTimeout> | undefined
      onCleanup(() => clearTimeout(reconnect))
      void connect()
    },
    { immediate: true }
  )
  function resolved(requestId: string) {
    conversation.accepted(requestId)
    const page = latest.value
    if (page)
      latest.value = {
        ...page,
        pendingInputs: page.pendingInputs.filter(
          (e) => String((e.payload as { requestId?: string })?.requestId ?? e.id) !== requestId
        )
      }
  }
  return {
    latest,
    error,
    statusLabel,
    livePlanEventId,
    piPhase: computed(() => {
      const page = latest.value
      const live = livePhase.value
      return live && live.id > (page?.cursor ?? 0)
        ? { phase: live.phase, at: live.at }
        : page?.piPhase ? { phase: page.piPhase, at: page.piPhaseAt } : undefined
    }),
    resolved,
    retry: () => retryKey.value++,
    pendingInputs: computed(() => latest.value?.pendingInputs ?? []),
    conversation
  }
}
