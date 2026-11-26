import { shallowRef } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  ApiError,
  errorMessage,
  type Stage,
  type Status,
  type Progress
} from '@/services/workbench'
import type { CompactWorkItem } from '@/services/workbench-read'
export function useWorkActions(
  progressBasis: () => 'effective' | 'personal',
  refreshAfter: (revision?: number) => Promise<unknown>
) {
  const { submit, saving } = useWorkbench()
  const open = shallowRef(false)
  const blocker = shallowRef('')
  const error = shallowRef('')
  const notice = shallowRef('')
  const overrideOpen = shallowRef(false)
  const correction = shallowRef<{
    item: CompactWorkItem
    stage: Stage
    progress: Progress
  }>()
  const pending = shallowRef<{ item: CompactWorkItem; revision: number }>()
  const stale = shallowRef(false)
  async function move(
    item: CompactWorkItem,
    dimension: 'status' | 'stage',
    value: Status | Stage,
    expectedRevision: number
  ) {
    error.value = ''
    notice.value = ''
    if (item.entity !== 'spec' || item[dimension] === value || saving.value)
      return
    if (progressBasis() === 'effective') {
      const status = dimension === 'status' ? (value as Status) : item.status
      const progress: Progress = status === 'canceled' ? 'skipped' : status
      const stage = dimension === 'stage' ? (value as Stage) : item.stage
      correction.value = {
        item,
        stage: stage === 'unmarked' ? 'research' : stage,
        progress
      }
      overrideOpen.value = true
      return
    }
    if (dimension === 'status' && value === 'blocked') {
      pending.value = { item, revision: expectedRevision }
      stale.value = false
      blocker.value = item.blocker
      open.value = true
      return
    }
    try {
      await submit(
        dimension === 'status'
          ? {
              kind: 'setWorkStatus',
              entity: item.entity,
              id: item.id,
              status: value as Status,
              blocker: item.blocker
            }
          : {
              kind: 'setWorkStage',
              entity: item.entity,
              id: item.id,
              stage: value as Stage
            },
        expectedRevision,
        undefined,
        refreshAfter
      )
      notice.value = '已保存变更，规格查询会随核对结果更新。'
    } catch (cause) {
      error.value = errorMessage(cause)
    }
  }
  async function confirm() {
    const edit = pending.value
    if (!edit || saving.value || stale.value) return
    if (!blocker.value.trim()) {
      error.value = '请填写阻塞说明'
      return
    }
    try {
      await submit(
        {
          kind: 'setWorkStatus',
          entity: edit.item.entity,
          id: edit.item.id,
          status: 'blocked',
          blocker: blocker.value
        },
        edit.revision,
        undefined,
        refreshAfter
      )
      open.value = false
      pending.value = undefined
      notice.value = '已记录阻塞，规格查询会随核对结果更新。'
    } catch (cause) {
      error.value = errorMessage(cause)
      stale.value = cause instanceof ApiError && cause.status === 409
    }
  }
  function cancel() {
    open.value = false
    pending.value = undefined
    stale.value = false
    error.value = ''
  }
  function corrected() {
    notice.value = '已保存人工更正，自动进度会随核对结果更新。'
  }
  function reloadBlocker(item: CompactWorkItem, revision: number) {
    if (item.id !== pending.value?.item.id) return
    pending.value = { item, revision }
    stale.value = false
    error.value = ''
  }
  return {
    move,
    confirm,
    cancel,
    open,
    blocker,
    error,
    notice,
    saving,
    overrideOpen,
    correction,
    corrected,
    pending,
    stale,
    reloadBlocker
  }
}
