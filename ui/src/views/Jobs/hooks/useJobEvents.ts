import { computed, onBeforeUnmount, shallowRef, watch, type Ref } from 'vue'
import { errorMessage } from '@/services/workbench'
import { objectValue, streamJob, terminalJob, type JobEvent } from '@/services/workbench-platform'
export function useJobEvents(
  jobId: Readonly<Ref<string>>,
  terminal: Readonly<Ref<boolean>>,
  updated: () => void
) {
  const events = shallowRef<JobEvent[]>([])
  const error = shallowRef('')
  const connected = shallowRef(false)
  const complete = shallowRef(false)
  const terminalSeen = shallowRef(false)
  const inputs = shallowRef<JobEvent[]>([])
  const runId = shallowRef('')
  const dropped = shallowRef(0)
  let controller: AbortController | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  let cursor = 0
  let generation = 0
  let endedCleanly = false
  let flushTimer: ReturnType<typeof setTimeout> | undefined
  let recent: { event: JobEvent; size: number }[] = []
  let recentSize = 0
  function publish() {
    clearTimeout(flushTimer)
    flushTimer = undefined
    events.value = recent.map(({ event }) => event)
  }
  const statusLabel = computed(() => {
    if (complete.value) return '已读取日志'
    if (connected.value)
      return terminal.value || terminalSeen.value ? '正在读取历史日志' : '实时连接'
    return '正在重连'
  })
  const pendingInputs = computed(() => (terminal.value || terminalSeen.value ? [] : inputs.value))
  function resolved(id: string) {
    inputs.value = inputs.value.filter(
      (event) => String(objectValue(event.payload).requestId ?? event.id) !== id
    )
  }
  function finishReading() {
    clearTimeout(timer)
    timer = undefined
    complete.value = true
  }
  async function connect(id: string, own: number) {
    if (!id || own !== generation || complete.value) return
    const connection = new AbortController()
    controller = connection
    connected.value = true
    endedCleanly = false
    try {
      await streamJob(id, cursor, connection.signal, (event) => {
        if (own !== generation || event.id <= cursor) return
        cursor = event.id
        error.value = ''
        const size = JSON.stringify(event).length * 2
        recent.push({ event, size })
        recentSize += size
        while (recent.length > 1 && (recent.length > 500 || recentSize > 4 * 1024 * 1024)) {
          recentSize -= recent.shift()!.size
          dropped.value++
        }
        if (!flushTimer) flushTimer = setTimeout(publish, 80)
        if (event.kind === 'run') runId.value = String(objectValue(event.payload).runId ?? '')
        if (event.kind === 'inputRequested') {
          inputs.value = [...inputs.value, event]
          publish()
        }
        if (
          event.kind === 'finished' ||
          (['status', 'stateChanged'].includes(event.kind) &&
            terminalJob(String(objectValue(event.payload).status ?? '')))
        ) {
          terminalSeen.value = true
          publish()
        }
        if (event.kind === 'inputResolved')
          resolved(String(objectValue(event.payload).requestId ?? ''))
        if (
          [
            'completed',
            'failed',
            'status',
            'stateChanged',
            'canceled',
            'inputRequested',
            'finished',
            'run'
          ].includes(event.kind)
        )
          updated()
      })
      if (own !== generation || connection.signal.aborted) return
      endedCleanly = true
      publish()
      error.value = ''
      if (terminal.value || terminalSeen.value) finishReading()
    } catch (cause) {
      if (!connection.signal.aborted && own === generation) error.value = errorMessage(cause)
    } finally {
      if (own === generation && !connection.signal.aborted) {
        connected.value = false
        if (!complete.value) timer = setTimeout(() => void connect(id, own), 1500)
      }
    }
  }
  watch(
    jobId,
    (id) => {
      controller?.abort()
      clearTimeout(timer)
      timer = undefined
      events.value = []
      inputs.value = []
      runId.value = ''
      dropped.value = 0
      clearTimeout(flushTimer)
      flushTimer = undefined
      recent = []
      recentSize = 0
      cursor = 0
      endedCleanly = false
      terminalSeen.value = false
      complete.value = false
      connected.value = false
      error.value = ''
      void connect(id, ++generation)
    },
    { immediate: true }
  )
  watch(terminal, (value) => {
    if (value && endedCleanly && !connected.value) finishReading()
  })
  onBeforeUnmount(() => {
    generation++
    controller?.abort()
    clearTimeout(timer)
    clearTimeout(flushTimer)
  })
  return {
    events,
    error,
    connected,
    complete,
    statusLabel,
    pendingInputs,
    runId,
    dropped,
    resolved
  }
}
