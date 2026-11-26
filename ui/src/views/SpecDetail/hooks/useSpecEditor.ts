import { computed, onBeforeUnmount, ref, shallowRef, watch, toRaw, type Ref } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute } from 'vue-router'
import { useEventListener } from '@vueuse/core'
import type { useWorkbench } from '@/hooks/useWorkbench'
import { useServerDraft } from '@/hooks/useServerDraft'
import {
  editFields,
  contentEqual,
  errorMessage,
  ApiError,
  type EditSpec
} from '@/services/workbench'
import { getTarget, type DraftRecord, type EditableTarget, type JsonObject } from '@/services/workbench-platform'
export function useSpecEditor(
  { snapshot, submit, saving }: Pick<ReturnType<typeof useWorkbench>, 'snapshot' | 'submit' | 'saving'>,
  active: Readonly<Ref<boolean>>,
  externalDirty: Readonly<Ref<boolean>>
) {
  const route = useRoute()
  const spec = computed(() =>
    snapshot.value?.specs.find((item) => item.record.id === route.params.specId)
  )
  const editingSpecId = shallowRef('')
  const draft = ref<EditSpec>()
  const saved = shallowRef('')
  const error = shallowRef('')
  const notice = shallowRef('')
  const initializing = shallowRef(false)
  const restoring = shallowRef(false)
  const submitting = shallowRef(false)
  const conflict = shallowRef<{
    base: JsonObject
    current: JsonObject
    token: string
  }>()
  let revision = 0
  let generation = 0
  let expectedTarget = ''
  let restoreTarget: EditableTarget<EditSpec> | undefined
  const dirty = computed(
    () => !!draft.value && !contentEqual(draft.value, JSON.parse(saved.value || 'null'))
  )
  const drafts = useServerDraft<EditSpec>({
    targetType: 'spec',
    targetId: () => editingSpecId.value,
    payload: draft,
    restore: (value) => {
      if (restoreTarget) {
        saved.value = JSON.stringify(restoreTarget.values)
        expectedTarget = restoreTarget.token
        revision = snapshot.value?.revision ?? revision
        drafts.setBase(restoreTarget.values, restoreTarget.token)
        restoreTarget = undefined
      }
      draft.value = value
    },
    refreshBase: async () => {
      const own = generation
      const target = await getTarget<EditSpec>({
        kind: 'editSpec',
        specId: editingSpecId.value,
        ...(draft.value as EditSpec)
      })
      if (own !== generation) throw new Error('当前 Spec 已切换。')
      restoreTarget = target
      return target
    }
  })
  async function reset() {
    if (!spec.value || initializing.value || restoring.value || submitting.value || drafts.recoveryConflict.value) return
    const own = generation
    const specId = spec.value.record.id
    const values = editFields(spec.value.record)
    editingSpecId.value = specId
    initializing.value = true
    error.value = ''
    try {
      const target = await getTarget<EditSpec>({ kind: 'editSpec', specId, ...values })
      if (own !== generation) return
      draft.value = target.values
      saved.value = JSON.stringify(target.values)
      revision = snapshot.value?.revision ?? 0
      expectedTarget = target.token
      drafts.setBase(target.values, target.token)
      conflict.value = undefined
    } catch (cause) {
      if (own === generation) error.value = errorMessage(cause)
    } finally {
      if (own === generation) initializing.value = false
    }
  }
  function refreshIdle() {
    const record = spec.value?.record
    if (active.value && record && !dirty.value && (!draft.value || !contentEqual(editFields(record), draft.value)))
      return reset()
  }
  async function restoreDraft(record: DraftRecord<EditSpec>) {
    if (initializing.value || restoring.value || submitting.value || saving.value || drafts.recoveryConflict.value) return
    const own = generation
    restoring.value = true
    try {
      await drafts.restore(record)
      if (own === generation && drafts.recoveryConflict.value && expectedTarget && saved.value)
        drafts.setBase(JSON.parse(saved.value), expectedTarget)
    } finally {
      if (own === generation) restoring.value = false
    }
  }
  function cancelRecovery() {
    drafts.cancelRecovery()
    restoreTarget = undefined
    if (expectedTarget && saved.value) drafts.setBase(JSON.parse(saved.value), expectedTarget)
  }
  watch(
    () => route.params.specId,
    () => {
      generation++
      editingSpecId.value = ''
      initializing.value = false
      restoring.value = false
      submitting.value = false
      expectedTarget = ''
      restoreTarget = undefined
      draft.value = undefined
      saved.value = ''
      error.value = ''
      notice.value = ''
      conflict.value = undefined
    },
    { flush: 'sync' }
  )
  onBeforeUnmount(() => { generation++ })
  watch([active, () => spec.value?.record.id], refreshIdle, { immediate: true })
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
  async function allowLeave(required = dirty.value || externalDirty.value): Promise<boolean> {
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
    if (dirty.value || externalDirty.value) {
      event.preventDefault()
      event.returnValue = ''
    }
  })
  async function save() {
    if (!draft.value || !spec.value || initializing.value || restoring.value || submitting.value ||
        conflict.value || drafts.recoveryConflict.value || !expectedTarget) return
    if (draft.value.status === 'blocked' && !draft.value.blocker.trim()) {
      error.value = '请填写阻塞说明'
      return
    }
    error.value = ''
    notice.value = ''
    const submitted = structuredClone(toRaw(draft.value))
    const own = generation
    const specId = spec.value.record.id
    submitting.value = true
    let committed = false
    try {
      await drafts.flush()
      if (own !== generation) return
      const result = await submit(
        { kind: 'editSpec', specId, ...submitted },
        revision,
        expectedTarget
      )
      if (own !== generation) return
      committed = true
      revision = result.revision
      const target = await getTarget<EditSpec>({ kind: 'editSpec', specId, ...submitted })
      if (own !== generation) return
      const unchangedInput = contentEqual(draft.value, submitted)
      const changesSinceCommit = !contentEqual(target.values, submitted)
      const canAdopt = unchangedInput || !changesSinceCommit
      if (canAdopt) {
        saved.value = JSON.stringify(target.values)
        expectedTarget = target.token
        if (unchangedInput && changesSinceCommit) draft.value = target.values
        await drafts.accepted(target.values, target.values, target.token)
      } else {
        await drafts.flush()
      }
      if (own !== generation) return
      if (!canAdopt && draft.value) {
        error.value = '个人标记已保存。读取到更新的编辑内容，请合并本次新增输入。'
        conflict.value = {
          base: { ...submitted },
          current: { ...target.values },
          token: target.token
        }
        return
      }
      notice.value = dirty.value ? '提交时的标记已保存，新增输入已保留为草稿。' : '个人标记已保存。'
      conflict.value = undefined
    } catch (cause) {
      if (own !== generation) return
      error.value = committed ? `个人标记已保存，重新读取编辑基线失败。${errorMessage(cause)}` : errorMessage(cause)
      if (!committed && cause instanceof ApiError && cause.status === 409) {
        try {
          const target = await getTarget<EditSpec>({ kind: 'editSpec', specId, ...submitted })
          if (own !== generation || !draft.value) return
          conflict.value = {
            base: JSON.parse(saved.value || '{}'),
            current: { ...target.values },
            token: target.token
          }
        } catch (readError) {
          if (own === generation) error.value = errorMessage(readError)
        }
      }
    } finally {
      if (own === generation) submitting.value = false
    }
  }
  function resolveConflict(value: JsonObject) {
    if (!conflict.value) return
    saved.value = JSON.stringify(conflict.value.current)
    expectedTarget = conflict.value.token
    drafts.setBase(conflict.value.current as EditSpec, conflict.value.token)
    draft.value = JSON.parse(JSON.stringify(value)) as EditSpec
    revision = snapshot.value?.revision ?? revision
    conflict.value = undefined
    error.value = ''
    notice.value = '已合并，请核对后保存。'
  }
  async function reload() {
    if (await allowLeave(dirty.value)) {
      await reset()
      if (!error.value) notice.value = ''
    }
  }
  return {
    spec,
    draft,
    dirty,
    error,
    notice,
    initializing: computed(() => initializing.value || restoring.value),
    saving,
    save,
    reload,
    refreshIdle,
    confirmOpen,
    decide,
    allowLeave,
    drafts,
    restoreDraft,
    cancelRecovery,
    conflict,
    resolveConflict
  }
}
