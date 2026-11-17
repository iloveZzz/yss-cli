import { shallowRef } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  errorMessage,
  type WorkItem,
  type Stage,
  type Status,
  type Progress
} from '@/services/workbench'
export function useWorkActions(progressBasis: () => 'effective' | 'personal') {
  const { submit, saving } = useWorkbench()
  const open = shallowRef(false)
  const blocker = shallowRef('')
  const error = shallowRef('')
  const notice = shallowRef('')
  const overrideOpen = shallowRef(false)
  const correction = shallowRef<{
    item: WorkItem
    stage: Stage
    progress: Progress
  }>()
  let pending: { item: WorkItem; revision: number } | undefined
  async function move(
    item: WorkItem,
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
      pending = { item, revision: expectedRevision }
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
        expectedRevision
      )
      notice.value = '已移动，结果已刷新。'
    } catch (cause) {
      error.value = errorMessage(cause)
    }
  }
  async function confirm() {
    if (!pending || saving.value) return
    if (!blocker.value.trim()) {
      error.value = '请填写阻塞说明'
      return
    }
    try {
      await submit(
        {
          kind: 'setWorkStatus',
          entity: pending.item.entity,
          id: pending.item.id,
          status: 'blocked',
          blocker: blocker.value
        },
        pending.revision
      )
      open.value = false
      pending = undefined
      notice.value = '已记录阻塞，结果已刷新。'
    } catch (cause) {
      error.value = errorMessage(cause)
    }
  }
  function cancel() {
    open.value = false
    pending = undefined
    error.value = ''
  }
  function corrected() {
    notice.value = '已保存人工更正，自动进度已重新汇总。'
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
    corrected
  }
}
