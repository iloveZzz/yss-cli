import { computed, ref, shallowRef, watch, toRaw, type Ref } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute } from 'vue-router'
import { useEventListener } from '@vueuse/core'
import { useWorkbench } from '@/hooks/useWorkbench'
import { useServerDraft } from '@/hooks/useServerDraft'
import {
  editFields,
  contentEqual,
  errorMessage,
  ApiError,
  type EditSpec
} from '@/services/workbench'
import { getTarget, type JsonObject } from '@/services/workbench-platform'
export function useSpecEditor(externalDirty?: Readonly<Ref<boolean>>) {
  const route = useRoute()
  const { snapshot, submit, saving } = useWorkbench()
  const spec = computed(() =>
    snapshot.value?.specs.find((item) => item.record.id === route.params.specId)
  )
  const draft = ref<EditSpec>()
  const saved = shallowRef('')
  const error = shallowRef('')
  const notice = shallowRef('')
  const initializing = shallowRef(false)
  const conflict = shallowRef<{
    base: JsonObject
    local: JsonObject
    current: JsonObject
    token: string
  }>()
  let revision = 0
  let generation = 0
  const dirty = computed(
    () => !!draft.value && !contentEqual(draft.value, JSON.parse(saved.value || 'null'))
  )
  const drafts = useServerDraft<EditSpec>({
    targetType: 'spec',
    targetId: () => String(route.params.specId ?? ''),
    payload: draft,
    restore: (value) => {
      draft.value = value
    },
    refreshBase: () =>
      getTarget<EditSpec>({
        kind: 'editSpec',
        specId: String(route.params.specId),
        ...(draft.value as EditSpec)
      })
  })
  async function reset() {
    if (!spec.value) return
    const specId = spec.value.record.id
    draft.value = editFields(spec.value.record)
    saved.value = JSON.stringify(draft.value)
    revision = snapshot.value?.revision ?? 0
    error.value = ''
    conflict.value = undefined
    try {
      const target = await getTarget<EditSpec>({ kind: 'editSpec', specId, ...draft.value })
      if (spec.value?.record.id === specId) drafts.setBase(target.values, target.token)
    } catch (cause) {
      error.value = errorMessage(cause)
    }
  }
  watch(
    () => route.params.specId,
    async (id) => {
      const own = ++generation
      initializing.value = true
      draft.value = undefined
      error.value = ''
      notice.value = ''
      try {
        if (typeof id === 'string' && spec.value) await submit({ kind: 'visitSpec', specId: id })
      } catch (cause) {
        if (own === generation) error.value = `访问记录未更新：${errorMessage(cause)}`
      } finally {
        if (own === generation) {
          await reset()
          initializing.value = false
        }
      }
    },
    { immediate: true }
  )
  const confirmOpen = shallowRef(false)
  let resolveConfirmation: ((value: boolean) => void) | undefined
  function decide(value: boolean) {
    const resolve = resolveConfirmation
    resolveConfirmation = undefined
    confirmOpen.value = false
    resolve?.(value)
  }
  watch(confirmOpen, (value) => {
    if (!value) decide(false)
  })
  async function allowLeave(required = dirty.value || !!externalDirty?.value): Promise<boolean> {
    if (!required) return true
    await drafts.flush()
    decide(false)
    confirmOpen.value = true
    return new Promise((resolve) => {
      resolveConfirmation = resolve
    })
  }
  onBeforeRouteLeave(() => allowLeave())
  onBeforeRouteUpdate((to, from) => to.params.specId === from.params.specId || allowLeave())
  useEventListener(window, 'beforeunload', (event) => {
    if (dirty.value || externalDirty?.value) {
      event.preventDefault()
      event.returnValue = ''
    }
  })
  async function save() {
    if (!draft.value || !spec.value) return
    if (draft.value.status === 'blocked' && !draft.value.blocker.trim()) {
      error.value = '请填写阻塞说明'
      return
    }
    error.value = ''
    notice.value = ''
    const submitted = structuredClone(toRaw(draft.value))
    const own = generation
    const specId = spec.value.record.id
    await drafts.flush()
    try {
      const result = await submit(
        { kind: 'editSpec', specId, ...submitted },
        revision,
        drafts.baseToken.value || undefined
      )
      if (own !== generation) return
      saved.value = JSON.stringify(submitted)
      revision = result.revision
      const target = await getTarget<EditSpec>({ kind: 'editSpec', specId, ...submitted })
      await drafts.accepted(submitted, target.values, target.token)
      notice.value = dirty.value ? '提交时的标记已保存，新增输入已保留为草稿。' : '个人标记已保存。'
      conflict.value = undefined
    } catch (cause) {
      if (own !== generation) return
      error.value = errorMessage(cause)
      if (cause instanceof ApiError && cause.status === 409) {
        try {
          const target = await getTarget<EditSpec>({ kind: 'editSpec', specId, ...submitted })
          conflict.value = {
            base: JSON.parse(saved.value || '{}'),
            local: { ...toRaw(draft.value) },
            current: { ...target.values },
            token: target.token
          }
        } catch (readError) {
          error.value = errorMessage(readError)
        }
      }
    }
  }
  function resolveConflict(value: JsonObject) {
    if (!conflict.value) return
    saved.value = JSON.stringify(conflict.value.current)
    drafts.setBase(conflict.value.current as EditSpec, conflict.value.token)
    draft.value = value as EditSpec
    revision = snapshot.value?.revision ?? revision
    conflict.value = undefined
    error.value = ''
    notice.value = '已合并，请核对后保存。'
  }
  async function reload() {
    if (await allowLeave(dirty.value)) {
      await reset()
      notice.value = ''
    }
  }
  return {
    spec,
    draft,
    dirty,
    error,
    notice,
    initializing,
    saving,
    save,
    reload,
    confirmOpen,
    decide,
    allowLeave,
    reset,
    drafts,
    conflict,
    resolveConflict
  }
}
