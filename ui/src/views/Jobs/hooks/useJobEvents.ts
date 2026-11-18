import { computed, shallowRef, watch, type Ref } from 'vue'
import { ApiError, errorMessage } from '@/services/workbench'
import { getJobActivities, streamJob, type ActivityPage } from '@/services/workbench-platform'
import { useJobConversation } from './useJobConversation'

// Read a consistent snapshot, then subscribe strictly after its persisted event cursor.
// Reconnection repeats that handshake, including pending inputs and already-started commands.
export function useJobEvents(jobId: Readonly<Ref<string>>, updated: () => void) {
  const latest = shallowRef<ActivityPage>()
  const error = shallowRef('')
  const statusLabel = shallowRef('')
  const retryKey = shallowRef(0)
  const conversation = useJobConversation()
  watch(
    [jobId, retryKey],
    ([id], previous, onCleanup) => {
      if (previous?.[0] !== id) {
        latest.value = undefined
        conversation.reset()
      }
      error.value = ''
      if (!id) return
      const connection = new AbortController()
      const { signal } = connection
      let timer: ReturnType<typeof setTimeout> | undefined
      let syncing: Promise<void> | undefined
      let observed = 0
      onCleanup(() => {
        connection.abort()
        clearTimeout(timer)
      })

      async function sync() {
        const page = await getJobActivities(id, undefined, signal)
        if (signal.aborted) return
        const prior = latest.value
        latest.value = page
        if (prior?.runId !== page.runId || prior?.terminal !== page.terminal) updated()
        if (page.host === 'pi') await conversation.open(id, signal)
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
          try {
            await flush()
          } catch (cause) {
            if (!signal.aborted) {
              error.value = errorMessage(cause)
              schedule(1500)
            }
            return
          }
          if (observed > (latest.value?.cursor ?? 0)) schedule()
        }, delay)
      }
      async function connect() {
        try {
          statusLabel.value = latest.value ? '正在重连' : '正在读取记录'
          await flush()
          if (signal.aborted) return
          const after = conversation.getCursor() ?? latest.value?.cursor ?? 0
          if (!latest.value?.terminal || after < latest.value.cursor) {
            statusLabel.value = '实时连接'
            await streamJob(id, after, signal, (event) => {
              if (signal.aborted) return
              observed = Math.max(observed, event.id)
              conversation.receive(event)
              schedule()
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
    resolved,
    retry: () => retryKey.value++,
    pendingInputs: computed(() => latest.value?.pendingInputs ?? []),
    conversation
  }
}
