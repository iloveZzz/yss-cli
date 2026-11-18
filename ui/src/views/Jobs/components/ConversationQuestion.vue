<script setup lang="ts">
import { computed, nextTick, shallowRef, useTemplateRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
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
  terminal: boolean
  submit: (requestId: string, value: unknown) => Promise<string | void>
}>()

const card = useTemplateRef<HTMLElement>('card')
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
        <label v-if="custom" class="question-input">
          答复
          <textarea v-model="draft" :disabled="busy" rows="3" required />
        </label>
      </QuestionnaireItem>
      <div>
        <Button type="submit" :disabled="busy || !answer">{{ busy ? '正在提交…' : '提交答复' }}</Button>
      </div>
      <p v-if="formError" role="alert" class="question-error">{{ formError }}</p>
    </Questionnaire>
    <template v-else>
      <h3 class="question-title">{{ row.text }}</h3>
      <p v-if="question.reason" class="muted preserve-lines">{{ question.reason }}</p>
      <template v-if="row.status === 'saved'">
        <p class="question-state" role="status">答复已保存</p>
        <p class="preserve-lines">{{ question.answer }}</p>
      </template>
      <p v-else-if="row.status === 'failed'" role="alert" class="question-error">
        {{ question.error || '答复保存失败' }}
      </p>
      <p v-else-if="terminal" class="question-state muted">本轮已结束，未留存答复</p>
      <p v-else class="question-state muted" role="status">正在保存…</p>
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
.question-input {
  display: grid;
  gap: 4px;
}
.question-input textarea {
  width: 100%;
  min-width: 0;
}
.question-state, .question-error {
  margin-top: 8px;
  font-size: 12px;
}
.question-error {
  color: var(--brand-color-error-active);
}
</style>
