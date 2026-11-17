<script setup lang="ts">
import { computed, reactive, shallowRef } from 'vue'
import { Button } from '@/components/ui/button'
import { objectValue, readableValue, type JobEvent } from '@/services/workbench-platform'
const props = defineProps<{ event: JobEvent; busy: boolean }>()
const emit = defineEmits<{ answer: [requestId: string, value: unknown] }>()
const payload = computed(() => objectValue(props.event.payload))
const request = computed(() => objectValue(payload.value.request))
const method = computed(() =>
  String(request.value.protocolMethod ?? payload.value.protocolMethod ?? '')
)
const params = computed(() => objectValue(request.value.params ?? payload.value.params))
const questions = computed(() =>
  Array.isArray(params.value.questions) ? params.value.questions.map(objectValue) : []
)
const answers = reactive<Record<string, string>>({})
const custom = shallowRef('')
const error = shallowRef('')
const inputRequestId = computed(() => String(payload.value.requestId ?? props.event.id))
const approval = computed(() => /requestApproval/.test(method.value))
const command = computed(() =>
  Array.isArray(params.value.command)
    ? params.value.command.join(' ')
    : String(params.value.command ?? '')
)
function respond(decision?: string) {
  if (props.busy) return
  error.value = ''
  let value: unknown
  if (questions.value.length) {
    if (questions.value.some((question) => !answers[String(question.id)]?.trim())) {
      error.value = '请填写每个问题的答复。'
      return
    }
    value = {
      answers: Object.fromEntries(
        questions.value.map((question) => [
          String(question.id),
          { answers: [answers[String(question.id)] ?? ''] }
        ])
      )
    }
  } else if (method.value.includes('permissions/'))
    value = {
      permissions: decision === 'accept' ? (params.value.permissions ?? {}) : {},
      scope: 'turn'
    }
  else if (approval.value) value = { decision }
  else {
    try {
      value = JSON.parse(custom.value)
    } catch {
      error.value = '请填写有效的 JSON 答复。'
      return
    }
  }
  emit('answer', inputRequestId.value, value)
}
</script>
<template>
  <section class="notice input-request">
    <h2>{{ approval ? '需要你授权' : '需要你的答复' }}</h2>
    <p v-if="params.reason || params.message" class="preserve-lines">
      {{ String(params.reason ?? params.message) }}
    </p>
    <p v-if="command" class="request-command">{{ command }}</p>
    <p v-if="params.cwd" class="path-text">{{ params.cwd }}</p>
    <form aria-label="回复 Codex" @submit.prevent="respond()">
      <fieldset class="plain-fieldset" :disabled="busy">
        <legend class="sr-only">{{ approval ? '操作授权' : '问题答复' }}</legend>
        <div v-for="question in questions" :key="String(question.id)" class="input-question">
          <label :for="`answer-${event.id}-${question.id}`">
            {{ question.question ?? question.header }}
            <textarea
              :id="`answer-${event.id}-${question.id}`"
              v-model="answers[String(question.id)]"
              :name="String(question.id)"
              rows="2"
              required
            />
          </label>
          <div v-if="Array.isArray(question.options)" class="inline-actions">
            <Button
              v-for="option in question.options.map(objectValue)"
              :key="String(option.label)"
              type="button"
              variant="outline"
              @click="answers[String(question.id)] = String(option.label)"
            >
              <span>{{ option.label }}</span>
              <span v-if="option.description" class="option-description">{{
                option.description
              }}</span>
            </Button>
          </div>
        </div>
        <template v-if="approval">
          <details>
            <summary>查看宿主请求</summary>
            <pre>{{ readableValue(params) }}</pre>
          </details>
          <div class="inline-actions">
            <Button type="button" :disabled="busy" @click="respond('accept')">允许本次操作</Button>
            <Button type="button" variant="outline" :disabled="busy" @click="respond('decline')">
              拒绝
            </Button>
            <Button type="button" variant="ghost" :disabled="busy" @click="respond('cancel')">
              取消请求
            </Button>
          </div>
        </template>
        <template v-else>
          <label v-if="!questions.length">
            JSON 答复
            <textarea v-model="custom" name="response" rows="5" required spellcheck="false" />
          </label>
          <Button :disabled="busy">{{ busy ? '提交中…' : '提交答复' }}</Button>
        </template>
        <p v-if="error" role="alert" class="notice error">{{ error }}</p>
      </fieldset>
    </form>
  </section>
</template>
<style scoped>
.input-request {
  border-color: #d9a944;
  background: #fffbf1;
  padding: 20px;
}
.input-request h2 {
  font-size: 16px;
  margin: 0 0 12px;
}
.request-command {
  font-family: ui-monospace, SFMono-Regular, monospace;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  padding: 10px 12px;
  background: #fff;
  border: 1px solid #e9dfc8;
  border-radius: 6px;
}
.input-request fieldset {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.input-question {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.input-question textarea {
  margin-top: 8px;
}
.input-question .inline-actions {
  align-items: stretch;
}
.input-question .inline-actions button {
  height: auto;
  min-height: 36px;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 3px;
  padding: 8px 12px;
  white-space: normal;
  text-align: left;
}
.option-description {
  color: var(--brand-color-text-secondary);
  font-size: 12px;
  font-weight: 400;
}
@media (pointer: coarse) {
  .input-question .inline-actions button {
    min-height: 44px;
  }
}
@media (width < 720px) {
  .input-request textarea {
    font-size: 16px;
  }
}
</style>
