import { computed, onBeforeUnmount, ref, shallowRef, watch, type Ref } from 'vue'
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
import { getTarget, objectValue, type DraftRecord, type EditableTarget, type JsonObject } from '@/services/workbench-platform'

const editableKeys = ['sourcePath', 'title', 'resources', 'active'] as const
type SpecDraft = Pick<EditSpec, typeof editableKeys[number]>

function isSpecDraft(value: unknown): value is SpecDraft {
  const { sourcePath, title, resources, active } = objectValue(value)
  return typeof sourcePath === 'string' && typeof title === 'string' && typeof active === 'boolean' &&
    Array.isArray(resources) && resources.every((resource: unknown) => {
      const { kind, label, targetType, target } = objectValue(resource)
      return (kind === 'other' || kind === 'supplement' || kind === 'prototype' || kind === 'interaction') &&
        (targetType === 'local' || targetType === 'web') &&
        typeof label === 'string' && typeof target === 'string'
    })
}

function editableFields({ sourcePath, title, resources, active }: SpecDraft): SpecDraft {
  return { sourcePath, title, resources: resources.map((resource) => ({ ...resource })), active }
}

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
  const draft = ref<SpecDraft>()
  const baseline = shallowRef<EditableTarget<EditSpec>>()
  const error = shallowRef('')
  const notice = shallowRef('')
  const initializing = shallowRef(false)
  const restoring = shallowRef(false)
  const submitting = shallowRef(false)
  const conflict = shallowRef<{
    base: SpecDraft
    target: EditableTarget<EditSpec>
  }>()
  let revision = 0
  let generation = 0
  let restoreTarget: EditableTarget<EditSpec> | undefined
  // The edit API still accepts the full record. Carry the retired fields through from its baseline.
  const payload = computed(() =>
    draft.value && baseline.value
      ? { ...baseline.value.values, ...editableFields(draft.value) }
      : undefined
  )
  const dirty = computed(
    () => !!draft.value && !!baseline.value &&
      !contentEqual(draft.value, editableFields(baseline.value.values))
  )
  const drafts = useServerDraft<SpecDraft>({
    targetType: 'spec',
    targetId: () => editingSpecId.value,
    payload,
    restore: (value) => {
      if (restoreTarget) {
        revision = snapshot.value?.revision ?? revision
        setBaseline(restoreTarget)
        restoreTarget = undefined
      }
      draft.value = editableFields(value)
    },
    refreshBase: async (record) => {
      const own = generation
      if (!payload.value) throw new Error('请先读取 Spec 设置。')
      const target = await getTarget<EditSpec>({
        kind: 'editSpec',
        specId: editingSpecId.value,
        ...payload.value
      })
      if (own !== generation) throw new Error('当前 Spec 已切换。')
      restoreTarget = target
      // Compare only editable fields during recovery, then adopt the complete server baseline.
      return { ...target, values: { ...record.base, ...editableFields(target.values) } }
    }
  })
  const recoverableDrafts = computed(() => {
    const current = baseline.value && editableFields(baseline.value.values)
    return drafts.recoverable.value.filter(({ base, payload }) => {
      if (!isSpecDraft(base) || !isSpecDraft(payload)) return false
      return editableKeys.some((key) =>
        !contentEqual(base[key], payload[key]) && !contentEqual(current?.[key], payload[key])
      )
    })
  })
  const recoveryConflict = computed(() => {
    const recovery = drafts.recoveryConflict.value
    return recovery && {
      base: editableFields(recovery.base),
      local: editableFields(recovery.local),
      current: editableFields(recovery.current)
    }
  })
  function setBaseline(target: EditableTarget<EditSpec>) {
    baseline.value = target
    drafts.setBase(target.values, target.token)
  }
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
      draft.value = editableFields(target.values)
      revision = snapshot.value?.revision ?? 0
      setBaseline(target)
      conflict.value = undefined
    } catch (cause) {
      if (own === generation) error.value = errorMessage(cause)
    } finally {
      if (own === generation) initializing.value = false
    }
  }
  function refreshIdle() {
    const record = spec.value?.record
    if (active.value && record && !dirty.value &&
        (!draft.value || !contentEqual(editFields(record), baseline.value?.values)))
      return reset()
  }
  async function restoreDraft(record: DraftRecord<SpecDraft>) {
    if (initializing.value || restoring.value || submitting.value || saving.value || drafts.recoveryConflict.value) return
    if (!baseline.value) return
    const payload = { ...record.base, ...editableFields(record.payload) }
    const preserveOriginal = !contentEqual(payload, record.payload)
    const own = generation
    restoring.value = true
    try {
      await drafts.restore({ ...record, payload }, { preserveOriginal })
      if (own === generation && drafts.recoveryConflict.value)
        drafts.setBase(baseline.value.values, baseline.value.token)
    } finally {
      if (own === generation) restoring.value = false
    }
  }
  function cancelRecovery() {
    drafts.cancelRecovery()
    restoreTarget = undefined
    if (baseline.value) drafts.setBase(baseline.value.values, baseline.value.token)
  }
  watch(
    () => route.params.specId,
    () => {
      generation++
      editingSpecId.value = ''
      initializing.value = false
      restoring.value = false
      submitting.value = false
      restoreTarget = undefined
      draft.value = undefined
      baseline.value = undefined
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
    if (!payload.value || !baseline.value || !spec.value || initializing.value || restoring.value ||
        submitting.value || conflict.value || drafts.recoveryConflict.value) return
    error.value = ''
    notice.value = ''
    const submitted = payload.value
    const { token, values } = baseline.value
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
        token
      )
      if (own !== generation) return
      committed = true
      revision = result.revision
      const target = await getTarget<EditSpec>({ kind: 'editSpec', specId, ...submitted })
      if (own !== generation) return
      const unchangedInput = contentEqual(payload.value, submitted)
      const changesSinceCommit = !contentEqual(editableFields(target.values), editableFields(submitted))
      const canAdopt = unchangedInput || !changesSinceCommit
      if (canAdopt) {
        setBaseline(target)
        if (unchangedInput && changesSinceCommit) draft.value = editableFields(target.values)
        await drafts.accepted(target.values, target.values, target.token)
      } else {
        await drafts.flush()
      }
      if (own !== generation) return
      if (!canAdopt && draft.value) {
        error.value = '修改已保存。读取到更新的编辑内容，请合并本次新增输入。'
        conflict.value = {
          base: editableFields(submitted),
          target
        }
        return
      }
      notice.value = dirty.value ? '修改已保存，新增输入已保留为草稿。' : '修改已保存。'
      conflict.value = undefined
    } catch (cause) {
      if (own !== generation) return
      error.value = committed ? `修改已保存，重新读取编辑基线失败。${errorMessage(cause)}` : errorMessage(cause)
      if (!committed && cause instanceof ApiError && cause.status === 409) {
        try {
          const target = await getTarget<EditSpec>({ kind: 'editSpec', specId, ...submitted })
          if (own !== generation || !draft.value) return
          conflict.value = {
            base: editableFields(values),
            target
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
    setBaseline(conflict.value.target)
    draft.value = editableFields(value as SpecDraft)
    revision = snapshot.value?.revision ?? revision
    conflict.value = undefined
    error.value = ''
    notice.value = '已合并，请核对后保存。'
  }
  function clearConflict() {
    conflict.value = undefined
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
    recoverableDrafts,
    recoveryConflict,
    restoreDraft,
    cancelRecovery,
    conflict: computed(() => conflict.value && {
      base: conflict.value.base,
      current: editableFields(conflict.value.target.values)
    }),
    clearConflict,
    resolveConflict
  }
}
