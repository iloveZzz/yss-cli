<script setup lang="ts">
import { Alert } from '@/components/ui/alert'
import { Textarea } from '@/components/ui/textarea'
import { computed, nextTick, onBeforeUnmount, useId, useTemplateRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import ModalDialog from '@/components/ModalDialog.vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import DraftNotice from '@/components/DraftNotice.vue'
import ConflictResolver from '@/components/ConflictResolver.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { currentQuestionResponse, questionHistory, reviewDateLabel } from '@/services/workbench-content'
import type { QuestionView } from '@/services/workbench'
import { useQuestionResponseEditor } from '../hooks/useQuestionResponseEditor'
import QuestionHistory from './QuestionHistory.vue'

const props = defineProps<{
  specId: string
  item: QuestionView | null
  confirmLeave: (required: boolean) => boolean | Promise<boolean>
}>()
const emit = defineEmits<{ close: []; saved: [id?: string | null]; dirty: [value: boolean] }>()
const { saving } = useWorkbench()
const id = useId()
const answerField = useTemplateRef<InstanceType<typeof Textarea>>('answerField')
const {
  form, basis, question, busy, dirty, error, notice, review, collision, pending, drafts,
  recoverable, initialize, acceptReview, merge, save
} = useQuestionResponseEditor(props, (responseId) => emit('saved', responseId), () => emit('close'))
const reading = computed(() => busy.value && !basis.value)
const submitting = computed(() => busy.value && !!basis.value && !review.value)
const history = computed(() => question.value ? questionHistory(question.value) : [])
const current = computed(() => question.value && currentQuestionResponse(question.value))
const oldAnswer = computed(() => !current.value && history.value.find((entry) => entry.response.kind === 'answer')?.response)
const open = computed({
  get: () => !!props.item,
  set: (value) => { if (!value) emit('close') }
})
watch(dirty, (value) => emit('dirty', value), { immediate: true })
onBeforeUnmount(() => emit('dirty', false))

async function canClose() {
  await drafts.flush()
  return props.confirmLeave(dirty.value)
}
async function cancel() {
  if (await canClose()) emit('close')
}
async function reuseAnswer() {
  const response = oldAnswer.value
  if (!response) return
  form.answer = response.answer
  error.value = ''
  notice.value = `已填入 ${response.id} 的原答复，可修改后保存。`
  await nextTick()
  answerField.value?.focus()
}
async function submitForm() {
  await save()
  if (error.value && form.responseKind === 'answer' && !form.answer.trim())
    answerField.value?.focus()
}
</script>
<template>
  <ModalDialog v-model="open" title="答复与延期" :busy="(busy && !reading) || saving" :before-close="canClose">
    <form v-if="question" class="form-stack" aria-label="答复与延期" novalidate @submit.prevent="submitForm">
      <header class="question-heading">
        <p class="muted">{{ question.question.id }} · 当前问题摘要 {{ question.digest || '未知' }}</p>
        <h3 class="preserve-lines">{{ question.question.title }}</h3>
        <p class="preserve-lines">涉及范围 · {{ question.question.affectedScope || '未说明' }}</p>
        <p v-if="question.question.impact" class="muted preserve-lines">影响 · {{ question.question.impact }}</p>
      </header>
      <QuestionHistory :entries="history" />
      <LoadingSkeleton v-if="reading" variant="form" :rows="3" label="正在核对当前问题…" />
      <template v-if="basis">
        <DraftNotice
          :status="drafts.status.value"
          :error="drafts.error.value"
          :drafts="recoverable"
          :conflict="drafts.recoveryConflict.value"
          @merge="drafts.mergeRecovered"
          @cancel-recovery="drafts.cancelRecovery"
          @restore="drafts.restore"
          @discard="drafts.discard"
        />
        <ConflictResolver
          v-if="collision"
          :base="{ ...basis.values }"
          :local="{ ...form }"
          :current="{ ...collision.values }"
          @resolve="merge"
          @close="collision = undefined"
        />
        <RadioGroup
          :model-value="form.responseKind"
          aria-label="处理方式"
          class="response-mode"
          @update:model-value="form.responseKind = $event === 'defer' ? 'defer' : 'answer'"
        >
          <label :for="`${id}-answer-mode`" :class="{ selected: form.responseKind === 'answer' }">
            <RadioGroupItem :id="`${id}-answer-mode`" value="answer" />
            <span>{{ question.question.kind === 'research' ? '补充依据' : '答复问题' }}</span>
          </label>
          <label :for="`${id}-defer-mode`" :class="{ selected: form.responseKind === 'defer' }">
            <RadioGroupItem :id="`${id}-defer-mode`" value="defer" />
            <span>延期处理</span>
          </label>
        </RadioGroup>
        <template v-if="form.responseKind === 'answer'">
          <div class="answer-field">
            <div class="answer-label">
              <label :for="`${id}-answer`">{{ question.question.kind === 'research' ? '补充依据与来源' : '本次答复' }}</label>
              <Button v-if="oldAnswer && !review" type="button" variant="outline" :title="`沿用 ${oldAnswer.id}`" @click="reuseAnswer">沿用原答复</Button>
            </div>
            <Textarea :id="`${id}-answer`" ref="answerField" v-model="form.answer" name="answer" rows="3"
              placeholder="填写适用于当前问题的答复" required :aria-invalid="!!error && !form.answer.trim()"
              :aria-describedby="`${id}-hint`" />
          </div>
          <Button v-if="question.question.recommendation" type="button" variant="outline" class="recommendation"
            @click="form.answer = question.question.recommendation">采用推荐答案</Button>
        </template>
        <template v-else>
          <label :for="`${id}-reason`">
            延期原因
            <Textarea :id="`${id}-reason`" v-model="form.reason" name="reason" rows="2" required />
          </label>
          <label :for="`${id}-impact`">
            影响范围
            <Textarea :id="`${id}-impact`" v-model="form.impact" name="impact" rows="2" required />
          </label>
          <label :for="`${id}-excluded`">
            从本次确认中排除的范围
            <Textarea :id="`${id}-excluded`" v-model="form.excludedScope" name="excludedScope" rows="2" required />
          </label>
          <div class="form-grid">
            <label :for="`${id}-date`">复查日期
              <Input :id="`${id}-date`" v-model="form.reviewAt" name="reviewAt" type="date" :aria-describedby="`${id}-review-hint`" />
            </label>
            <label :for="`${id}-condition`">或复查触发条件
              <Input :id="`${id}-condition`" v-model="form.reviewWhen" name="reviewWhen" :aria-describedby="`${id}-review-hint`" />
            </label>
          </div>
          <p :id="`${id}-review-hint`" class="muted">
            日期与条件至少填写一项。新选日期按本地时区保存，条件是否触发须另行核对。
            <template v-if="current?.kind === 'defer' && current.reviewAt">保留 {{ reviewDateLabel(current) }} 时沿用其时区。</template>
          </p>
        </template>
        <p :id="`${id}-hint`" class="muted">保存后进入待回写，落实到相关规格与产物后更新结果。</p>
      </template>
      <Alert v-if="error" role="alert" class="notice error">{{ error }}</Alert>
      <p v-if="notice" role="status">{{ notice }}</p>
      <div v-if="review">
        <Button type="button" variant="outline" :disabled="busy" @click="acceptReview">
          <LoadingIndicator :active="busy" />
          {{ busy ? '正在核对…' : '核对新版并保留输入' }}
        </Button>
      </div>
      <div class="form-actions">
        <Button type="button" variant="outline" :disabled="(busy && !reading) || saving" @click="cancel">取消</Button>
        <Button v-if="!basis && !busy" type="button" @click="initialize">重新读取问题</Button>
        <Button v-else type="submit" :disabled="busy || saving || !basis || !!review || !!collision">
          <LoadingIndicator :active="submitting" />
          {{ reading ? '正在读取…' : submitting ? '处理中…' : pending ? (pending.requestId ? '核对并重试原请求' : '重读保存结果') : '保存答复' }}
        </Button>
      </div>
    </form>
  </ModalDialog>
</template>
<style scoped>
.question-heading { overflow-wrap: anywhere; }
.question-heading h3 { margin: 4px 0; font-size: 15px; }
.question-heading .muted { font-size: 12px; }
.answer-field { display: grid; gap: 6px; }
.answer-label { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.answer-label label { font-weight: 550; }
.recommendation { align-self: flex-start; }
.response-mode {
  display: flex;
  flex-wrap: wrap;
  align-self: flex-start;
  gap: 4px;
  padding: 3px;
  border-radius: 8px;
  background: var(--yss-colors-surface-muted, #f0f2f5);
}
.response-mode label {
  flex-direction: row;
  align-items: center;
  min-height: var(--yss-control-min-height);
  padding: 3px 10px;
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
}
.response-mode label.selected {
  color: var(--brand-color-text);
  background: #fff;
}
@media (width < 720px) {
  .response-mode label { min-height: 44px; }
}
@media (width < 560px) { .form-grid { grid-template-columns: minmax(0, 1fr); } }
</style>
