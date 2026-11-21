import { computed, onBeforeUnmount, reactive, shallowRef, watch } from 'vue'
import { useServerDraft } from '@/hooks/useServerDraft'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  acknowledgeRequest, ApiError, contentEqual, errorMessage, reconcilePendingMutation,
  resolvePendingRequest, type MutationResult, type QuestionView, type RespondQuestionAction
} from '@/services/workbench'
import { currentQuestionResponse, dateAtOffset, questionHistory, questionState } from '@/services/workbench-content'
import { getReceipt, getSpecContent, getTarget, type DraftRecord, type JsonObject } from '@/services/workbench-platform'

type QuestionDraft = Omit<RespondQuestionAction, 'kind' | 'specId' | 'questionId' | 'reviewUtcOffsetMinutes'>
interface QuestionTarget {
  specId: string
  token: string
  question: QuestionView
  values: QuestionDraft
}
interface SubmittedSave {
  submitted: QuestionDraft
  requestId?: string
  responseId?: string | null
}

function fields(item?: QuestionView): QuestionDraft {
  const response = item && currentQuestionResponse(item)
  const defer = response?.kind === 'defer' ? response : null
  return {
    questionDigest: item?.digest ?? '',
    responseKind: defer ? 'defer' : 'answer',
    answer: response?.kind === 'answer' ? response.answer : '',
    reason: defer?.reason ?? '',
    impact: defer?.impact ?? '',
    excludedScope: defer?.excludedScope ?? item?.question.affectedScope ?? '',
    reviewAt: defer?.reviewAt ?? '',
    reviewWhen: defer?.reviewWhen ?? ''
  }
}
function dateOffset(date: string, item: QuestionView): number {
  if (!date) return 0
  const saved = item.response
  if (saved?.kind === 'defer' && saved.reviewAt === date && typeof saved.reviewUtcOffsetMinutes === 'number')
    return saved.reviewUtcOffsetMinutes
  return -new Date(`${date}T00:00:00`).getTimezoneOffset() || 0
}
function action(specId: string, item: QuestionView, draft: QuestionDraft): RespondQuestionAction {
  const defer = draft.responseKind === 'defer'
  return {
    kind: 'respondQuestion', specId, questionId: item.question.id, questionDigest: draft.questionDigest,
    responseKind: draft.responseKind, answer: defer ? '' : draft.answer,
    reason: defer ? draft.reason : '', impact: defer ? draft.impact : '',
    excludedScope: defer ? draft.excludedScope : '', reviewAt: defer ? draft.reviewAt : '',
    reviewWhen: defer ? draft.reviewWhen : '',
    reviewUtcOffsetMinutes: defer ? dateOffset(draft.reviewAt, item) : 0
  }
}
function validationMessage(draft: QuestionDraft, item: QuestionView): string {
  if (draft.responseKind === 'answer')
    return draft.answer.trim() ? '' : '请填写本次答复或补充依据。'
  if (!draft.reason.trim() || !draft.impact.trim() || !draft.excludedScope.trim() ||
    (!draft.reviewAt && !draft.reviewWhen.trim()))
    return '延期须填写原因、影响、排除范围，以及复查日期或触发条件。'
  if (item.question.blocking && draft.excludedScope.trim() !== item.question.affectedScope.trim())
    return '关键问题延期须完整排除当前问题的受影响范围。'
  if (draft.reviewAt && draft.reviewAt <= dateAtOffset(dateOffset(draft.reviewAt, item)))
    return '复查日期须晚于今天。'
  return ''
}
async function readTarget(specId: string, item: QuestionView): Promise<QuestionTarget> {
  const target = await getTarget<QuestionDraft>(action(specId, item, fields(item)))
  const question = target.question ??
    (await getSpecContent(specId)).questions.find((candidate) => candidate.question.id === item.question.id)
  if (!question || question.question.id !== item.question.id) throw new Error('问题已移除，请重新查看主规格。')
  const checked = { ...question, state: questionState(question), history: questionHistory(question), response: currentQuestionResponse(question) }
  return { specId, question: checked, token: target.token, values: fields(checked) }
}
function matchesDraft(record: DraftRecord<QuestionDraft>, target: QuestionTarget): boolean {
  if (!record.base || !record.payload || typeof record.payload !== 'object' || typeof record.base !== 'object') return false
  if (record.base.questionDigest || record.payload.questionDigest)
    return record.base.questionDigest === target.question.digest &&
      record.payload.questionDigest === target.question.digest
  // Older workbench drafts only carry a token bound to the digest and saved response ID.
  return !!target.token && record.baseToken === target.token
}

export function useQuestionResponseEditor(
  props: { specId: string; item: QuestionView | null },
  onSaved: (id?: string | null) => void,
  onClose: () => void
) {
  const { snapshot, submit, refresh } = useWorkbench()
  const form = reactive<QuestionDraft>(fields())
  const basis = shallowRef<QuestionTarget>()
  const review = shallowRef<QuestionTarget>()
  const collision = shallowRef<QuestionTarget>()
  const pending = shallowRef<SubmittedSave>()
  const busy = shallowRef(false)
  const error = shallowRef('')
  const notice = shallowRef('')
  const question = computed(() => review.value?.question ?? basis.value?.question ?? props.item)
  const dirty = computed(() => !!basis.value && !contentEqual(form, basis.value.values))
  let generation = 0

  function needsReview(target: QuestionTarget): boolean {
    return !!basis.value && (basis.value.question.digest !== target.question.digest ||
      (!!basis.value.question.response && !target.question.response &&
        (target.question.state === 'overdue' || target.question.state === 'needsReview')))
  }
  function showReview(target: QuestionTarget) {
    collision.value = undefined
    review.value = target
    drafts.cancelRecovery()
    error.value = '问题或延期有效性已更新，本次输入已保留。请核对当前问题与范围后再决定。'
  }
  function restoreFields(value: Partial<QuestionDraft>) {
    const target = basis.value
    if (!target) return
    form.responseKind = value.responseKind === 'defer' ? 'defer' : 'answer'
    for (const key of ['answer', 'reason', 'impact', 'excludedScope', 'reviewAt', 'reviewWhen'] as const) {
      form[key] = typeof value[key] === 'string' ? value[key] : target.values[key]
    }
    form.questionDigest = target.question.digest
  }
  const drafts = useServerDraft<QuestionDraft>({
    targetType: 'question',
    targetId: () => basis.value ? `${basis.value.specId}:${basis.value.question.question.id}` : '',
    targetVersion: () => basis.value?.question.digest ?? '',
    payload: computed(() => basis.value ? form : undefined),
    restore: restoreFields,
    refreshBase: async (record) => {
      const own = generation
      const target = await readTarget(props.specId, props.item!)
      if (own !== generation) throw new Error('当前问题已切换。')
      if (needsReview(target)) {
        showReview(target)
        throw new Error('原问题版本的草稿不能恢复到新版。')
      }
      if (!matchesDraft(record, target)) throw new Error('草稿所属的问题版本无法核实。')
      basis.value = target
      return target
    }
  })
  const recoverable = computed(() => {
    const target = basis.value
    return target && !review.value ? drafts.recoverable.value.filter((draft) => matchesDraft(draft, target)) : []
  })
  function useTarget(target: QuestionTarget) {
    basis.value = target
    form.questionDigest = target.question.digest
    drafts.setBase(target.values, target.token)
    collision.value = undefined
    review.value = undefined
    error.value = ''
  }
  async function initialize() {
    const own = ++generation
    const item = props.item
    void drafts.flush()
    basis.value = review.value = collision.value = undefined
    pending.value = undefined
    error.value = notice.value = ''
    busy.value = !!item
    Object.assign(form, fields())
    if (!item) return
    try {
      const target = await readTarget(props.specId, item)
      if (own !== generation) return
      Object.assign(form, target.values)
      useTarget(target)
    } catch (cause) {
      if (own === generation) error.value = errorMessage(cause)
    } finally {
      if (own === generation) busy.value = false
    }
  }
  watch([() => props.specId, () => props.item], initialize, { immediate: true })
  onBeforeUnmount(() => { generation++ })

  function compare(target: QuestionTarget): boolean {
    if (needsReview(target)) {
      showReview(target)
      return false
    }
    if (target.token !== basis.value?.token) {
      collision.value = target
      error.value = '这份答复已在其他位置更新，请合并后再次保存。'
      return false
    }
    return true
  }
  async function acceptReview() {
    const target = review.value
    if (!target || busy.value) return
    const own = generation
    busy.value = true
    try {
      await drafts.flush()
      const latest = await readTarget(target.specId, target.question)
      if (own !== generation) return
      if (latest.token !== target.token || latest.question.digest !== target.question.digest) {
        showReview(latest)
        return
      }
      useTarget(latest)
      notice.value = '已切换到当前问题版本。本次输入已保留，请核对后保存。'
    } catch (cause) {
      if (own === generation) error.value = errorMessage(cause)
    } finally {
      if (own === generation) busy.value = false
    }
  }
  function merge(value: JsonObject) {
    if (!collision.value) return
    const target = collision.value
    restoreFields(value)
    useTarget(target)
    notice.value = '已合并，请核对后保存。'
  }

  async function finishSave(saved: SubmittedSave, own: number) {
    pending.value = saved
    const original = basis.value!
    const target = await readTarget(original.specId, original.question)
    if (own !== generation) return
    const changed = !contentEqual(form, saved.submitted)
    // A receipt proves this write. A concurrent reply can already have replaced it.
    const replaced = !!saved.responseId && saved.responseId !== target.question.response?.id
    const versionChanged = needsReview(target)
    await drafts.accepted(saved.submitted, versionChanged ? saved.submitted : target.values, target.token)
    if (own !== generation) return
    pending.value = undefined
    onSaved(saved.responseId)
    if (!changed && contentEqual(form, saved.submitted)) {
      onClose()
    } else if (versionChanged) {
      showReview(target)
    } else if (replaced) {
      collision.value = target
      error.value = '本次答复已保存，随后出现了新的记录。请核对并合并新增输入。'
    } else {
      useTarget(target)
      notice.value = '提交时的答复已保存，新增输入已保留为草稿。'
    }
  }
  async function recoverSave(saved: SubmittedSave, retry: boolean, own: number) {
    if (saved.requestId) {
      const receipt = await getReceipt(saved.requestId)
      if (own !== generation) return
      let result: unknown = receipt.result
      if (receipt.found) {
        resolvePendingRequest(saved.requestId)
        void acknowledgeRequest(saved.requestId)
      } else if (retry) {
        result = await reconcilePendingMutation(saved.requestId)
      } else {
        error.value = '尚未取得保存回执。本次输入已保留，可核对并重试原请求。'
        return
      }
      if (own !== generation) return
      saved = { submitted: saved.submitted, responseId: (result as MutationResult | undefined)?.responseId }
      pending.value = saved
      await refresh()
      if (own !== generation) return
    }
    await finishSave(saved, own)
  }
  async function handleFailure(cause: unknown, own: number) {
    if (own !== generation) return
    error.value = errorMessage(cause)
    if (pending.value?.requestId && cause instanceof ApiError &&
        cause.context.requestId === pending.value.requestId && cause.context.uncertain === false)
      pending.value = undefined
    if (pending.value && !pending.value.requestId) {
      error.value = `已取得保存回执，读取最新答复失败。请重读结果。${error.value}`
    } else if (cause instanceof ApiError && cause.status === 409) {
      pending.value = undefined
      const target = await readTarget(props.specId, props.item!)
      if (own === generation) compare(target)
    }
  }
  async function save() {
    const target = basis.value
    if (!target || busy.value || review.value || collision.value) return
    const own = generation
    busy.value = true
    error.value = notice.value = ''
    try {
      if (pending.value) {
        await recoverSave(pending.value, true, own)
        return
      }
      const submitted = { ...form }
      await drafts.flush()
      const current = await readTarget(target.specId, target.question)
      if (own !== generation || !compare(current)) return
      error.value = validationMessage(submitted, current.question)
      if (error.value) return
      let result: MutationResult
      try {
        result = await submit(action(target.specId, current.question, submitted),
          snapshot.value?.revision, target.token)
      } catch (cause) {
        if (own !== generation) return
        if (cause instanceof ApiError && cause.context.uncertain && typeof cause.context.requestId === 'string') {
          const saved = { submitted, requestId: cause.context.requestId }
          pending.value = saved
          await recoverSave(saved, false, own)
          return
        }
        throw cause
      }
      if (own === generation) await finishSave({ submitted, responseId: result.responseId }, own)
    } catch (cause) {
      try { await handleFailure(cause, own) }
      catch (readError) { if (own === generation) error.value = errorMessage(readError) }
    } finally {
      if (own === generation) busy.value = false
    }
  }
  return { form, basis, question, busy, dirty, error, notice, review, collision, pending, drafts,
    recoverable, initialize, acceptReview, merge, save }
}
