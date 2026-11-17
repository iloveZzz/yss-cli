<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, shallowRef, useId, watch, toRaw } from 'vue'
import { Button } from '@/components/ui/button'
import SingleSelect from '@/components/SingleSelect.vue'
import { Input } from '@/components/ui/input'
import ModalDialog from '@/components/ModalDialog.vue'
import DraftNotice from '@/components/DraftNotice.vue'
import ConflictResolver from '@/components/ConflictResolver.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { useServerDraft } from '@/hooks/useServerDraft'
import {
  errorMessage,
  contentEqual,
  ApiError,
  type QuestionView,
  type RespondQuestionAction
} from '@/services/workbench'
import { getTarget, type JsonObject } from '@/services/workbench-platform'
const props = defineProps<{
  specId: string
  item: QuestionView | null
  confirmLeave: (required: boolean) => boolean | Promise<boolean>
}>()
const emit = defineEmits<{ close: []; saved: []; dirty: [value: boolean] }>()
const { snapshot, saving, submit } = useWorkbench()
const id = useId()
const error = shallowRef('')
const baseline = shallowRef('')
const notice = shallowRef('')
let revision = 0
let generation = 0
interface QuestionDraft {
  responseKind: 'answer' | 'defer'
  answer: string
  reason: string
  impact: string
  excludedScope: string
  reviewAt: string
  reviewWhen: string
}
const form = reactive<QuestionDraft>({
  responseKind: 'answer',
  answer: '',
  reason: '',
  impact: '',
  excludedScope: '',
  reviewAt: '',
  reviewWhen: ''
})
const conflict = shallowRef<{
  base: JsonObject
  local: JsonObject
  current: JsonObject
  token: string
}>()
const dirty = computed(
  () => !!props.item && !contentEqual(form, JSON.parse(baseline.value || 'null'))
)
const open = computed({
  get: () => !!props.item,
  set: (value) => {
    if (!value) emit('close')
  }
})
function editableQuestion(value: QuestionDraft): QuestionDraft {
  const hasResponse = !!(value.answer.trim() || value.reason.trim())
  return {
    responseKind: value.responseKind,
    answer: value.answer,
    reason: value.reason,
    impact: hasResponse ? value.impact : (props.item?.question.affectedScope ?? ''),
    excludedScope: hasResponse ? value.excludedScope : (props.item?.question.affectedScope ?? ''),
    reviewAt: value.reviewAt,
    reviewWhen: value.reviewWhen
  }
}
async function currentTarget() {
  const target = await getTarget<QuestionDraft>(action({ ...form }))
  return { token: target.token, values: editableQuestion(target.values) }
}
const drafts = useServerDraft<QuestionDraft>({
  targetType: 'question',
  targetId: () => (props.item ? `${props.specId}:${props.item.question.id}` : ''),
  payload: computed(() => (props.item ? form : undefined)),
  restore: (value) => Object.assign(form, value),
  refreshBase: currentTarget
})
function action(value: QuestionDraft): RespondQuestionAction {
  return {
    kind: 'respondQuestion',
    specId: props.specId,
    questionId: props.item?.question.id ?? '',
    questionDigest: props.item?.digest ?? '',
    ...value,
    reviewUtcOffsetMinutes: value.reviewAt
      ? -new Date(`${value.reviewAt}T00:00:00`).getTimezoneOffset()
      : 0
  }
}
watch(
  () => props.item,
  async (item) => {
    generation++
    if (!item) return
    const own = generation
    const response = item.response
    Object.assign(form, {
      responseKind: response?.kind ?? 'answer',
      answer: response?.answer ?? '',
      reason: response?.reason ?? '',
      impact: response?.impact ?? item.question.affectedScope,
      excludedScope: response?.excludedScope ?? item.question.affectedScope,
      reviewAt: response?.reviewAt ?? '',
      reviewWhen: response?.reviewWhen ?? ''
    })
    baseline.value = JSON.stringify(form)
    error.value = ''
    notice.value = ''
    conflict.value = undefined
    revision = snapshot.value?.revision ?? 0
    try {
      const target = await getTarget<QuestionDraft>(action({ ...form }))
      if (own === generation) drafts.setBase({ ...form }, target.token)
    } catch (cause) {
      if (own === generation) error.value = errorMessage(cause)
    }
  }
)
watch(dirty, (value) => emit('dirty', value), { immediate: true })
onBeforeUnmount(() => emit('dirty', false))
async function close() {
  await drafts.flush()
  if (await props.confirmLeave(dirty.value)) emit('close')
}
async function save() {
  if (!props.item) return
  error.value = ''
  if (form.responseKind === 'answer' && !form.answer.trim()) {
    error.value = '请填写答复或补充依据。'
    return
  }
  if (
    form.responseKind === 'defer' &&
    (!form.reason.trim() ||
      !form.impact.trim() ||
      !form.excludedScope.trim() ||
      (!form.reviewAt && !form.reviewWhen.trim()))
  ) {
    error.value = '延期须填写原因、影响、排除范围，以及复查日期或触发条件。'
    return
  }
  const submitted = structuredClone(toRaw(form))
  const own = generation
  await drafts.flush()
  try {
    await submit(action(submitted), revision, drafts.baseToken.value || undefined)
    if (own !== generation) return
    baseline.value = JSON.stringify(submitted)
    revision = snapshot.value?.revision ?? revision
    const target = await getTarget<QuestionDraft>(action(submitted))
    await drafts.accepted(submitted, submitted, target.token)
    emit('saved')
    if (!dirty.value) emit('close')
    else notice.value = '提交时的答复已保存，新增输入已保留。'
  } catch (cause) {
    if (own !== generation) return
    error.value = errorMessage(cause)
    if (cause instanceof ApiError && cause.status === 409) {
      try {
        const target = await getTarget<QuestionDraft>(action(submitted))
        conflict.value = {
          base: JSON.parse(baseline.value),
          local: { ...form },
          current: { ...editableQuestion(target.values) },
          token: target.token
        }
      } catch (readError) {
        error.value = errorMessage(readError)
      }
    }
  }
}
function merge(value: JsonObject) {
  if (!conflict.value) return
  baseline.value = JSON.stringify(conflict.value.current)
  drafts.setBase(conflict.value.current as unknown as QuestionDraft, conflict.value.token)
  Object.assign(form, value)
  revision = snapshot.value?.revision ?? revision
  conflict.value = undefined
  error.value = ''
  notice.value = '已合并，请核对后保存。'
}
</script>
<template>
  <ModalDialog
    v-model="open"
    title="答复与延期"
    :busy="saving"
    :before-close="() => confirmLeave(dirty)"
  >
    <form v-if="item" class="form-stack" aria-label="答复与延期" @submit.prevent="save">
      <p class="preserve-lines">{{ item.question.title }}</p>
      <DraftNotice
        :status="drafts.status.value"
        :error="drafts.error.value"
        :drafts="drafts.recoverable.value"
        :conflict="drafts.recoveryConflict.value"
        @merge="drafts.mergeRecovered"
        @cancel-recovery="drafts.cancelRecovery"
        @restore="drafts.restore"
        @discard="drafts.discard"
      />
      <ConflictResolver
        v-if="conflict"
        :base="conflict.base"
        :local="conflict.local"
        :current="conflict.current"
        @resolve="merge"
        @close="conflict = undefined"
      />
      <label :for="`${id}-kind`">
        处理方式
        <SingleSelect
          :id="`${id}-kind`"
          :model-value="form.responseKind"
          :options="[
            { value: 'answer', label: item.question.kind === 'research' ? '补充依据' : '答复问题' },
            { value: 'defer', label: '延期处理' }
          ]"
          aria-label="处理方式"
          @update:model-value="form.responseKind = $event as 'answer' | 'defer'"
        />
      </label>
      <template v-if="form.responseKind === 'answer'">
        <label :for="`${id}-answer`">
          {{ item.question.kind === 'research' ? '补充依据与来源' : '我的答复' }}
          <textarea
            :id="`${id}-answer`"
            v-model="form.answer"
            name="answer"
            rows="5"
            required
            :aria-describedby="`${id}-hint`"
          />
        </label>
        <Button
          v-if="item.question.recommendation"
          type="button"
          variant="outline"
          @click="form.answer = item.question.recommendation"
        >
          采用推荐答案
        </Button>
      </template>
      <template v-else>
        <label :for="`${id}-reason`">
          延期原因
          <textarea :id="`${id}-reason`" v-model="form.reason" name="reason" rows="2" required />
        </label>
        <label :for="`${id}-impact`">
          影响范围
          <textarea :id="`${id}-impact`" v-model="form.impact" name="impact" rows="2" required />
        </label>
        <label :for="`${id}-excluded`">
          从本次确认中排除的范围
          <textarea
            :id="`${id}-excluded`"
            v-model="form.excludedScope"
            name="excludedScope"
            rows="2"
            required
          />
        </label>
        <div class="form-grid">
          <label :for="`${id}-date`">
            复查日期
            <Input
              :id="`${id}-date`"
              v-model="form.reviewAt"
              name="reviewAt"
              type="date"
              :aria-describedby="`${id}-review-hint`"
            />
          </label>
          <label :for="`${id}-condition`">
            或复查触发条件
            <Input
              :id="`${id}-condition`"
              v-model="form.reviewWhen"
              name="reviewWhen"
              :aria-describedby="`${id}-review-hint`"
            />
          </label>
        </div>
        <p :id="`${id}-review-hint`" class="muted">
          日期与条件至少填写一项，日期按提交时本地时区记录。
        </p>
      </template>
      <p :id="`${id}-hint`" class="muted">保存后进入待回写，落实到相关规格与产物后更新结果。</p>
      <p v-if="error" role="alert" class="notice error">{{ error }} 输入与本机草稿均保留。</p>
      <p v-if="notice" role="status">{{ notice }}</p>
      <div class="form-actions">
        <Button type="button" variant="outline" :disabled="saving" @click="close">关闭</Button>
        <Button :disabled="saving">{{ saving ? '保存中…' : '保存答复' }}</Button>
      </div>
    </form>
  </ModalDialog>
</template>
