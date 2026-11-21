<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, shallowRef, useTemplateRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import ModalDialog from '@/components/ModalDialog.vue'
import { useQuestionReviewDeadline } from '@/hooks/useQuestionReviewDeadline'
import { errorMessage, type AgentQuestion, type AgentRequest } from '@/services/workbench'
import { questionState, responseText } from '@/services/workbench-content'
import { getAgentRequest } from '@/services/workbench-platform'
import QuestionHistory from './QuestionHistory.vue'

const props = defineProps<{ specId: string }>()
const open = defineModel<boolean>({ required: true })
const request = shallowRef<AgentRequest>()
const error = shallowRef('')
const copyError = shallowRef('')
const copying = shallowRef(false)
const notice = shallowRef('')
const expanded = shallowRef(false)
const rawText = useTemplateRef<HTMLTextAreaElement>('rawText')
let generation = 0
onBeforeUnmount(() => {
  generation++
})
const sections = computed(() => {
  const groups: Record<'pending' | 'applied' | 'review' | 'open', AgentQuestion[]> =
    { pending: [], applied: [], review: [], open: [] }
  for (const item of request.value?.questions ?? []) {
    if (item.state === 'resolved' || item.state === 'deferred') groups.applied.push(item)
    else if (item.state === 'pendingWriteback' && item.response) groups.pending.push(item)
    else if (item.state === 'open') groups.open.push(item)
    else groups.review.push(item)
  }
  return [
    { title: '当前可回写', items: groups.pending, active: true },
    { title: '已回写与已有研究结论', items: groups.applied },
    { title: '待复查问题', items: groups.review }, { title: '待答问题', items: groups.open }
  ].filter((section) => section.active || section.items.length)
})
function hasExpiredResponse(value: AgentRequest) {
  return value.questions?.some((item) => item.response && questionState(item) === 'overdue')
}
async function readLatest(specId: string) {
  const value = await getAgentRequest(specId)
  if (!hasExpiredResponse(value)) return value
  const latest = await getAgentRequest(specId)
  if (hasExpiredResponse(latest)) throw new Error('延期已到复查日期，请重新读取续办文本。')
  return latest
}
async function load(reset = true) {
  const own = ++generation
  request.value = undefined
  if (reset) expanded.value = false
  error.value = copyError.value = notice.value = ''
  copying.value = false
  if (!open.value) return
  try {
    const value = await readLatest(props.specId)
    if (own === generation) request.value = value
  } catch (cause) {
    if (own === generation) error.value = errorMessage(cause)
  }
}
watch([open, () => props.specId], () => load(), { immediate: true })
useQuestionReviewDeadline(() => {
  const value = request.value
  return value?.questions && { readAt: value.readAt, questions: value.questions }
}, async () => {
  if (copying.value) request.value = undefined
  else await load(false)
})
async function copy() {
  if (!request.value || copying.value) return
  const own = generation
  copying.value = true
  notice.value = copyError.value = ''
  try {
    const latest = await readLatest(props.specId)
    if (own !== generation) return
    request.value = latest
    try {
      await navigator.clipboard.writeText(latest.text)
      if (own === generation && request.value === latest) notice.value = '已复制最新的完整续办文本。'
    } catch {
      if (own !== generation || request.value !== latest) return
      expanded.value = true
      await nextTick()
      if (own !== generation || request.value !== latest) return
      rawText.value?.focus()
      rawText.value?.select()
      notice.value = '已选中最新的完整原文，请手动复制。'
    }
  } catch (cause) {
    if (own !== generation) return
    const message = `未复制。读取最新续办文本失败：${errorMessage(cause)}`
    if (request.value && !hasExpiredResponse(request.value)) copyError.value = message
    else {
      request.value = undefined
      error.value = message
    }
  } finally {
    if (own === generation) {
      copying.value = false
      if (!request.value && !error.value) void load(false)
    }
  }
}
</script>
<template>
  <ModalDialog v-model="open" title="Agent 续办文本">
    <div class="form-stack handoff">
      <p v-if="error" role="alert" class="notice error">{{ error }}</p>
      <p v-else-if="!request" role="status">正在读取当前问题与答复…</p>
      <template v-else>
        <template v-if="request.questions">
          <section v-for="section in sections" :key="section.title" :aria-label="section.title">
            <h3>{{ section.title }}</h3>
            <p v-if="!section.items.length" class="muted">当前没有可回写的答复。</p>
            <article v-for="item in section.items" :key="item.question.id" :class="{ active: section.active }">
              <p class="muted">{{ item.question.id }} · <template v-if="item.response">{{ item.response.id }} · </template>{{ item.usage }}</p>
              <strong class="preserve-lines">{{ item.question.title }}</strong>
              <p class="muted">当前问题摘要 · {{ item.digest || '未知' }}</p>
              <p class="preserve-lines">涉及范围 · {{ item.question.affectedScope || '未说明' }}</p>
              <p v-if="item.response" class="preserve-lines">{{ responseText(item.response) }}</p>
              <p v-if="item.response?.reviewWhen" class="muted">请核对复查条件。已触发时转入复查。</p>
              <p v-if="item.question.resolution" class="preserve-lines">来源结论 · {{ item.question.resolution }}</p>
            </article>
          </section>
          <QuestionHistory :entries="request.history ?? []" title="历史原文" show-question-id />
          <details :open="expanded" @toggle="expanded = ($event.currentTarget as HTMLDetailsElement).open">
            <summary>查看完整原文</summary>
            <textarea ref="rawText" :value="request.text" readonly rows="12" aria-label="完整续办文本" />
          </details>
        </template>
        <textarea v-else ref="rawText" :value="request.text" readonly rows="15" aria-label="完整续办文本" />
      </template>
      <p v-if="notice" role="status">{{ notice }}</p>
      <p v-if="copyError" role="alert" class="notice error">{{ copyError }}</p>
      <div class="form-actions">
        <Button v-if="error" type="button" variant="outline" @click="load()">重新读取</Button>
        <Button v-if="request" type="button" variant="outline" :disabled="copying" @click="copy">复制完整原文</Button>
        <Button type="button" variant="outline" @click="open = false">关闭</Button>
      </div>
    </div>
  </ModalDialog>
</template>
<style scoped>
.handoff { overflow-wrap: anywhere; }
.handoff h3 { font-size: 15px; }
.handoff article { margin-top: 8px; border: 1px solid #e4e6eb; border-radius: 8px; padding: 12px 14px; }
.handoff article.active { background: #f4f7ff; border-color: #d9e2fb; }
.handoff article p { margin-top: 4px; }
.handoff article p:first-child { margin-top: 0; }
.handoff .muted { font-size: 12px; }
.handoff summary { cursor: pointer; }
.handoff details textarea { margin-top: 8px; width: 100%; }
</style>
