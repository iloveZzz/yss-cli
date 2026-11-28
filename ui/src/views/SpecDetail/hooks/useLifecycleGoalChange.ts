import { computed, onScopeDispose, shallowRef } from 'vue'
import { useRoute } from 'vue-router'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  acknowledgeRequest,
  apiRequest,
  ApiError,
  conflictedRequests,
  errorMessage,
  lifecycleGoalRequest,
  pendingRequests,
  resolvePendingRequest,
  retainRequestConflict,
  type LifecycleGoalChange,
  type LifecycleGoalRequest,
  type MutationResult
} from '@/services/workbench'

const endpoint = '/api/commands'

export function useLifecycleGoalChange(specId: string) {
  const route = useRoute()
  const { refresh, saving, snapshot } = useWorkbench()
  const busy = shallowRef(false)
  const error = shallowRef('')
  const notice = shallowRef('')
  const conflict = shallowRef(false)
  let disposed = false
  onScopeDispose(() => { disposed = true })
  const isCurrentSpec = () => !disposed && route.params.specId === specId

  const pending = computed(() => pendingRequests.value.flatMap(({ requestId, status }) => {
    const body = lifecycleGoalRequest(requestId)
    return body?.action.input.specId === specId ? [{ body, status }] : []
  }))
  const rejections = computed(() => conflictedRequests.value.flatMap(({ requestId, message }) => {
    const body = lifecycleGoalRequest(requestId)
    return body?.action.input.specId === specId ? [{ body, message }] : []
  }))
  const rejected = computed(() => rejections.value.at(-1))

  function reset(needsReview = false) {
    error.value = needsReview && rejected.value ? `${rejected.value.message} 本次输入已保留。` : ''
    conflict.value = needsReview
  }

  async function send(body: LifecycleGoalRequest) {
    return apiRequest<MutationResult>(endpoint, {
      body,
      persistInBrowser: true,
      retryUncertain: false,
      retainOnConflict: true,
      signal: AbortSignal.timeout(20000)
    })
  }

  async function accept(body: LifecycleGoalRequest, result: MutationResult) {
    for (const { body: rejectedBody } of rejections.value) {
      if (rejectedBody.action.kind === body.action.kind &&
          rejectedBody.action.input.goalVersion === body.action.input.goalVersion) {
        resolvePendingRequest(rejectedBody.requestId)
      }
    }
    if (!isCurrentSpec()) return false
    let latest = await refresh()
    if (latest && isCurrentSpec() && (snapshot.value?.revision ?? -1) < result.revision) latest = await refresh()
    if (!isCurrentSpec()) return false
    const current = snapshot.value?.progress?.[specId]?.goalVersion
    const version = result.goalVersion ?? body.action.input.goalVersion
    latest &&= (snapshot.value?.revision ?? -1) >= result.revision
    const message = body.action.kind === 'cancelLifecycleGoal' ? '已取消本轮目标。' : '已开启新一轮。'
    notice.value = latest
      ? current && current !== version
        ? `${message} 操作对应 ${version}，当前轮次为 ${current}。`
        : message
      : `${message} 读取当前轮次失败，请刷新详情。`
    return true
  }

  async function perform(body: LifecycleGoalRequest, recovery?: 'receipt' | 'retry') {
    if (busy.value || saving.value || !isCurrentSpec()) return false
    busy.value = true
    reset()
    notice.value = ''
    try {
      if (recovery) {
        const receipt = await apiRequest<{ found: boolean; matches?: boolean; result?: MutationResult }>(
          `/api/receipts/${encodeURIComponent(body.requestId)}/match`,
          { body, trackIntent: false, signal: AbortSignal.timeout(12000) }
        )
        if (receipt.found) {
          if (receipt.matches === false) {
            retainRequestConflict(body.requestId, '该请求 ID 对应其他提交。')
            if (isCurrentSpec()) {
              reset(true)
              await refresh()
            }
            return false
          }
          const result = receipt.result
          if (!receipt.matches || result?.specId !== specId || !result.goalVersion) {
            if (isCurrentSpec()) error.value = '回执与原操作无法核对，结果仍待核对。'
            return false
          }
          resolvePendingRequest(body.requestId)
          void acknowledgeRequest(body.requestId)
          return await accept(body, result)
        }
        if (!isCurrentSpec()) return false
        if (recovery === 'receipt') {
          error.value = '尚未找到保存回执，结果待核对。可按原请求重试。'
          return false
        }
      }
      return await accept(body, await send(body))
    } catch (cause) {
      if (!isCurrentSpec()) return false
      const message = errorMessage(cause)
      conflict.value = cause instanceof ApiError && cause.status === 409 && cause.context.uncertain !== true
      error.value = conflict.value ? `${message} 本次输入已保留。` : message
      if (conflict.value) await refresh()
      return false
    } finally {
      busy.value = false
    }
  }

  function submit(action: LifecycleGoalChange, expectedRevision: number) {
    if (pending.value.length || conflict.value) return Promise.resolve(false)
    return perform({ action, requestId: crypto.randomUUID(), expectedRevision })
  }

  return {
    pending, rejected, busy, error, notice, conflict, reset, submit,
    check: (body: LifecycleGoalRequest) => perform(body, 'receipt'),
    retry: (body: LifecycleGoalRequest) => perform(body, 'retry'),
    discard: (requestId: string) => resolvePendingRequest(requestId)
  }
}
