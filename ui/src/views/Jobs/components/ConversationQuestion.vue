<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { computed, nextTick, shallowRef, useId, useTemplateRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Field, FieldLabel } from '@/components/ui/field'
import { Textarea } from '@/components/ui/textarea'
import {
  Questionnaire,
  QuestionnaireChoice,
  QuestionnaireChoices,
  QuestionnaireDescription,
  QuestionnaireItem,
  QuestionnaireTitle
} from '@/components/ui/questionnaire'
import type { JobActivity, JobQuestion } from '@/services/workbench-platform'

const props = defineProps<{
  row: JobActivity
  question: JobQuestion
  busy: boolean
  submitting: boolean
  terminal: boolean
  submit: (requestId: string, value: unknown) => Promise<string | void>
}>()

const card = useTemplateRef<HTMLElement>('card')
const answerId = useId()
const selected = shallowRef('')
const draft = shallowRef('')
const formError = shallowRef('')
const awaiting = computed(() => !props.terminal && props.row.status === 'pending')
const custom = computed(() => !props.question.options.length || selected.value === 'custom')
const answer = computed(() => {
  if (custom.value) return draft.value.trim()
  const i = props.question.options.findIndex((_, index) => selected.value === `choice-${index}`)
  return props.question.options[i] ?? ''
})

watch(awaiting, async (pending) => {
  if (pending || !card.value?.contains(document.activeElement)) return
  await nextTick()
  card.value?.focus({ preventScroll: true })
})

async function respond(event: Event) {
  event.preventDefault()
  if (!awaiting.value || props.busy || !answer.value) return
  formError.value = ''
  const error = await props.submit(props.question.requestId, {
    answers: { [props.question.questionId]: { answers: [answer.value] } }
  })
  if (error) formError.value = error
}
</script>

<template>
  <article
    :id="`pi-question-${row.id}`"
    ref="card"
    class="conversation-question"
    aria-label="业务问题"
    tabindex="-1"
  >
    <Questionnaire
      v-if="awaiting"
      aria-label="提交业务答复"
      :aria-busy="submitting"
      class="gap-3"
      @submit="respond"
    >
      <QuestionnaireItem :name="question.questionId" :required="question.options.length > 0" class="gap-2">
        <QuestionnaireTitle class="question-title">{{ row.text }}</QuestionnaireTitle>
        <QuestionnaireDescription v-if="question.reason" class="preserve-lines text-sm">
          {{ question.reason }}
        </QuestionnaireDescription>
        <QuestionnaireChoices v-if="question.options.length" class="gap-2">
          <QuestionnaireChoice
            v-for="(option, index) in question.options"
            :key="index"
            :value="`choice-${index}`"
            :checked="selected === `choice-${index}`"
            :disabled="busy"
            class="min-h-10 py-2.5"
            @update:checked="(checked) => { if (checked) selected = `choice-${index}` }"
          >{{ option }}</QuestionnaireChoice>
          <QuestionnaireChoice
            value="custom"
            :checked="selected === 'custom'"
            :disabled="busy"
            class="min-h-10 py-2.5"
            @update:checked="(checked) => { if (checked) selected = 'custom' }"
          >自填答复</QuestionnaireChoice>
        </QuestionnaireChoices>
        <Field v-if="custom" class="gap-1">
          <FieldLabel :for="answerId">答复</FieldLabel>
          <Textarea :id="answerId" v-model="draft" :disabled="busy" rows="3" required />
        </Field>
      </QuestionnaireItem>
      <div>
        <Button type="submit" :disabled="busy || !answer">
          <LoadingIndicator :active="submitting" />
          {{ submitting ? '正在提交…' : '提交答复' }}
        </Button>
      </div>
      <Alert v-if="formError" variant="destructive" class="question-error py-2">
        <AlertDescription class="text-current">{{ formError }}</AlertDescription>
      </Alert>
    </Questionnaire>
    <template v-else>
      <h3 class="question-title">{{ row.text }}</h3>
      <p v-if="question.reason" class="muted preserve-lines">{{ question.reason }}</p>
      <template v-if="row.status === 'saved'">
        <Badge class="question-state" variant="secondary" role="status">答复已保存</Badge>
        <p class="preserve-lines">{{ question.answer }}</p>
      </template>
      <Alert v-else-if="row.status === 'failed'" variant="destructive" class="question-error py-2">
        <AlertDescription class="text-current">{{ question.error || '答复保存失败' }}</AlertDescription>
      </Alert>
      <p v-else-if="terminal" class="question-state muted">本轮已结束，未留存答复</p>
      <p v-else class="question-state muted" role="status">
        <LoadingIndicator :active="row.status === 'saving'" />
        正在保存…
      </p>
    </template>
  </article>
</template>

<style scoped>
.conversation-question {
  max-width: 760px;
  padding: 14px 16px;
  border-radius: 12px;
  border: 1px solid var(--yss-colors-border-control);
  background: var(--brand-color-bg-container);
  overflow-wrap: anywhere;
}
.conversation-question:focus-visible {
  outline: 2px solid var(--brand-color-primary);
  outline-offset: 2px;
}
.question-title {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
}
.question-state, .question-error {
  margin-top: 8px;
  font-size: 12px;
}
.question-error {
  color: var(--brand-color-error-active);
}
</style>
