import { computed, onBeforeUnmount, shallowRef, watch, toRaw, type Ref } from 'vue'
import {
  listDrafts,
  putDraft,
  removeDraft,
  type DraftRecord,
  type EditableTarget
} from '@/services/workbench-platform'
import { contentEqual, errorMessage } from '@/services/workbench'
const editorSessionId = crypto.randomUUID()
export function useServerDraft<T extends object>(options: {
  targetType: string
  targetId: () => string
  targetVersion?: () => string
  payload: Readonly<Ref<T | undefined>>
  restore: (value: T) => void
  refreshBase?: (record: DraftRecord<T>) => Promise<EditableTarget<T>>
}) {
  const id = shallowRef<string>(crypto.randomUUID())
  const revision = shallowRef(0)
  const base = shallowRef<T>()
  const baseToken = shallowRef('')
  const savedPayload = shallowRef('')
  const status = shallowRef('')
  const error = shallowRef('')
  const recoverable = shallowRef<DraftRecord<T>[]>([])
  const saving = shallowRef(false)
  const adopted = shallowRef<DraftRecord<T>[]>([])
  const recoveryConflict = shallowRef<{ base: T; local: T; current: T }>()
  let recovering: DraftRecord<T> | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  let inflight: Promise<void> | undefined
  let generation = 0
  let accepting = false
  const changed = computed(
    () =>
      !!options.payload.value && !!base.value && !contentEqual(options.payload.value, base.value)
  )
  watch(
    [options.targetId, () => options.targetVersion?.()],
    async ([targetId]) => {
      const own = ++generation
      clearTimeout(timer)
      id.value = crypto.randomUUID()
      revision.value = 0
      base.value = undefined
      baseToken.value = ''
      savedPayload.value = ''
      recoverable.value = []
      adopted.value = []
      recoveryConflict.value = undefined
      recovering = undefined
      error.value = ''
      status.value = ''
      saving.value = false
      if (!targetId) return
      try {
        const records = await listDrafts<T>(options.targetType, targetId)
        if (own === generation) recoverable.value = records
      } catch (cause) {
        if (own === generation) error.value = errorMessage(cause)
      }
    },
    { immediate: true, flush: options.targetVersion ? 'sync' : 'pre' }
  )
  function setBase(value: T, token: string) {
    base.value = structuredClone(toRaw(value))
    baseToken.value = token
  }
  async function flush(): Promise<void> {
    clearTimeout(timer)
    if (accepting) return
    if (inflight) {
      const previous = inflight
      await previous
      if (inflight === previous) inflight = undefined
      return flush()
    }
    if (!options.payload.value || !base.value || !options.targetId() || !changed.value) return
    const encoded = JSON.stringify(options.payload.value)
    if (encoded === savedPayload.value) return
    const own = generation
    const draftId = id.value
    const targetId = options.targetId()
    const payload = structuredClone(toRaw(options.payload.value))
    const initial = structuredClone(toRaw(base.value))
    const expectedRevision = revision.value
    saving.value = true
    status.value = '正在保存草稿…'
    error.value = ''
    const operation = (async () => {
      try {
        const record = await putDraft(
          draftId,
          {
            editorSessionId,
            targetType: options.targetType,
            targetId,
            baseToken: baseToken.value,
            base: initial,
            payload
          },
          expectedRevision
        )
        if (own === generation) {
          revision.value = record.revision
          savedPayload.value = encoded
          status.value = '草稿已保存到本机'
        }
      } catch (cause) {
        if (own === generation) {
          error.value = errorMessage(cause)
          status.value = '草稿保存失败，输入仍保留'
        }
      } finally {
        if (own === generation) saving.value = false
      }
    })()
    inflight = operation
    try {
      await operation
    } finally {
      if (inflight === operation) inflight = undefined
    }
  }
  watch(
    [() => (options.payload.value ? JSON.stringify(options.payload.value) : ''), baseToken],
    () => {
      clearTimeout(timer)
      if (changed.value) timer = setTimeout(() => void flush(), 650)
    }
  )
  async function restore(record: DraftRecord<T>) {
    const own = generation
    try {
      await flush()
      if (own !== generation) return
      if (changed.value && revision.value) {
        const records = await listDrafts<T>(options.targetType, options.targetId())
        if (own !== generation) return
        recoverable.value = records
        id.value = crypto.randomUUID()
        revision.value = 0
        savedPayload.value = ''
      }
      if (options.refreshBase) {
        const target = await options.refreshBase(record)
        if (own !== generation) return
        setBase(target.values, target.token)
      }
      if (own !== generation) return
      recovering = record
      if (base.value && record.baseToken !== baseToken.value && !contentEqual(record.base, base.value)) {
        recoveryConflict.value = { base: record.base, local: record.payload, current: base.value }
        status.value = '草稿基线已变化，请核对合并内容'
        return
      }
      await mergeRecovered(record.payload)
    } catch (cause) {
      if (own === generation) error.value = errorMessage(cause)
    }
  }
  async function mergeRecovered(value: T) {
    options.restore(structuredClone(toRaw(value)))
    if (recovering)
      adopted.value = [...adopted.value.filter((item) => item.id !== recovering?.id), recovering]
    recovering = undefined
    recoveryConflict.value = undefined
    status.value = '已恢复草稿，请核对后保存'
    await flush()
  }
  function cancelRecovery() {
    recovering = undefined
    recoveryConflict.value = undefined
  }

  async function discard(record: DraftRecord<T>) {
    const own = generation
    try {
      await removeDraft(record.id, record.revision)
      if (own !== generation) return
      recoverable.value = recoverable.value.filter((item) => item.id !== record.id)
      adopted.value = adopted.value.filter((item) => item.id !== record.id)
    } catch (cause) {
      if (own === generation) error.value = errorMessage(cause)
    }
  }
  async function accepted(submitted: T, currentBase: T, token: string) {
    const own = generation
    clearTimeout(timer)
    accepting = true
    try {
      if (inflight) await inflight
      if (own !== generation) return
      const previousId = id.value
      const previousRevision = revision.value
      setBase(currentBase, token)
      const payload = options.payload.value
      // A saved target may normalize the submitted fields.
      savedPayload.value = contentEqual(payload, submitted) ? JSON.stringify(payload) : ''
      if (savedPayload.value) {
        try {
          if (previousRevision) await removeDraft(previousId, previousRevision)
          const restored = adopted.value
          if (own !== generation) return
          for (const record of restored) await removeDraft(record.id, record.revision)
          if (own !== generation) return
          recoverable.value = recoverable.value.filter(
            (record) => !adopted.value.some((item) => item.id === record.id)
          )
          adopted.value = []
        } catch (cause) {
          if (own !== generation) return
          error.value = errorMessage(cause)
        }
        id.value = crypto.randomUUID()
        revision.value = 0
      }
    } finally {
      accepting = false
    }
    if (changed.value && JSON.stringify(options.payload.value) !== savedPayload.value) await flush()
    else status.value = '已保存'
  }

  onBeforeUnmount(() => {
    clearTimeout(timer)
    void flush()
  })
  return {
    id,
    status,
    error,
    saving,
    recoverable,
    recoveryConflict,
    mergeRecovered,
    cancelRecovery,
    changed,
    baseToken,
    setBase,
    flush,
    restore,
    discard,
    accepted
  }
}
