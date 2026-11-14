<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, shallowRef, useId, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ModalDialog from '@/components/ModalDialog.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { errorMessage, type QuestionView } from '@/services/workbench'
const props = defineProps<{ specId: string; item: QuestionView | null; confirmLeave: (required: boolean) => boolean | Promise<boolean> }>()
const emit = defineEmits<{ close: []; saved: []; dirty: [value: boolean] }>()
const { snapshot, saving, submit } = useWorkbench()
const id = useId(); const error = shallowRef(''); const baseline = shallowRef(''); let revision = 0
const form = reactive({ responseKind: 'answer' as 'answer' | 'defer', answer: '', reason: '', impact: '', excludedScope: '', reviewAt: '', reviewWhen: '' })
const dirty = computed(() => !!props.item && JSON.stringify(form) !== baseline.value)
const open = computed({ get: () => !!props.item, set: value => { if (!value) emit('close') } })
watch(() => props.item, item => {
  if (!item) return
  const response = item.response
  Object.assign(form, { responseKind: response?.kind ?? 'answer', answer: response?.answer ?? '', reason: response?.reason ?? '', impact: response?.impact ?? item.question.affectedScope, excludedScope: response?.excludedScope ?? item.question.affectedScope, reviewAt: response?.reviewAt ?? '', reviewWhen: response?.reviewWhen ?? '' })
  baseline.value = JSON.stringify(form); error.value = ''; revision = snapshot.value?.revision ?? 0
})
watch(dirty, value => emit('dirty', value), { immediate: true })
onBeforeUnmount(() => emit('dirty', false))
async function close() { if (await props.confirmLeave(dirty.value)) emit('close') }
async function save() {
  if (!props.item) return
  error.value = ''
  if (form.responseKind === 'answer' && !form.answer.trim()) { error.value = '请填写答复或补充依据。'; return }
  if (form.responseKind === 'defer' && (!form.reason.trim() || !form.impact.trim() || !form.excludedScope.trim() || (!form.reviewAt && !form.reviewWhen.trim()))) { error.value = '延期须填写原因、影响、排除范围，以及复查日期或触发条件。'; return }
  try {
    await submit({ kind: 'respondQuestion', specId: props.specId, questionId: props.item.question.id, questionDigest: props.item.digest, ...form, reviewUtcOffsetMinutes: form.reviewAt ? -new Date(`${form.reviewAt}T00:00:00`).getTimezoneOffset() : 0 }, revision)
    baseline.value = JSON.stringify(form); emit('saved'); emit('close')
  } catch (e) { error.value = errorMessage(e) }
}
</script>
<template>
  <ModalDialog v-model="open" title="答复与延期" :busy="saving" :before-close="() => confirmLeave(dirty)">
    <form v-if="item" class="form-stack" @submit.prevent="save">
      <p class="preserve-lines">{{ item.question.title }}</p>
      <fieldset :disabled="saving" class="plain-fieldset form-stack">
        <label :for="`${id}-kind`">处理方式<select :id="`${id}-kind`" v-model="form.responseKind"><option value="answer">{{ item.question.kind === 'research' ? '补充依据' : '答复问题' }}</option><option value="defer">延期处理</option></select></label>
        <template v-if="form.responseKind === 'answer'"><label :for="`${id}-answer`">{{ item.question.kind === 'research' ? '补充依据与来源' : '我的答复' }}<textarea :id="`${id}-answer`" v-model="form.answer" rows="5" required :aria-describedby="`${id}-hint`" /></label><Button v-if="item.question.recommendation" type="button" variant="outline" @click="form.answer = item.question.recommendation">采用推荐答案</Button></template>
        <template v-else>
          <label :for="`${id}-reason`">延期原因<textarea :id="`${id}-reason`" v-model="form.reason" rows="2" required /></label>
          <label :for="`${id}-impact`">影响范围<textarea :id="`${id}-impact`" v-model="form.impact" rows="2" required /></label>
          <label :for="`${id}-excluded`">从本次确认中排除的范围<textarea :id="`${id}-excluded`" v-model="form.excludedScope" rows="2" required /></label>
          <div class="form-grid"><label :for="`${id}-date`">复查日期<Input :id="`${id}-date`" v-model="form.reviewAt" type="date" :aria-describedby="`${id}-review-hint`" /></label><label :for="`${id}-condition`">或复查触发条件<Input :id="`${id}-condition`" v-model="form.reviewWhen" :aria-describedby="`${id}-review-hint`" /></label></div>
          <p :id="`${id}-review-hint`" class="muted">日期与条件至少填写一项；日期按提交时本地时区记录，条件是否发生需你主动复查。</p>
        </template>
      </fieldset>
      <p :id="`${id}-hint`" class="muted">保存后仍待 Agent 回写相关规格、验收条件、交互说明与原型并核验。延期不计为已解决。</p>
      <p v-if="error" role="alert" class="notice error">{{ error }}<span>输入已保留；如记录有变，请复制输入后重新打开问题。</span></p>
      <div class="form-actions"><Button type="button" variant="outline" :disabled="saving" @click="close">取消</Button><Button :disabled="saving">{{ saving ? '保存中…' : '保存，等待回写' }}</Button></div>
    </form>
  </ModalDialog>
</template>
