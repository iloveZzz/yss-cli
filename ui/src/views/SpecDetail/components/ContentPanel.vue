<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import ModalDialog from '@/components/ModalDialog.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { contentStatuses, dateLabel, errorMessage, loadAgentRequest, type ContentView, type QuestionView } from '@/services/workbench'
import { contentReminders, questionStateLabel } from '@/services/workbench-content'
import QuestionResponseForm from './QuestionResponseForm.vue'
const props = defineProps<{ specId: string; sourcePath: string; content: ContentView; confirmLeave: (required: boolean) => boolean | Promise<boolean> }>()
const emit = defineEmits<{ dirty: [value: boolean]; saved: [] }>()
const { snapshot, submit, saving } = useWorkbench()
const selected = shallowRef<QuestionView | null>(null); const notice = shallowRef(''); const error = shallowRef('')
const requestText = shallowRef(''); const requestLoading = shallowRef(false)
const confirmOpen = shallowRef(false); const accepted = shallowRef(false)
const confirmation = shallowRef<{ scope: string; artifactDigest: string; exclusions: string[]; revision: number }>()
const reminders = computed(() => contentReminders(props.content))
const exclusions = computed(() => props.content.questions.filter(item => item.response?.kind === 'defer' && item.state !== 'resolved').map(item => item.response?.excludedScope ?? '').filter(Boolean))
const latestConfirmation = computed(() => props.content.confirmations.at(-1))
const pending = computed(() => props.content.questions.filter(item => item.state !== 'resolved'))
watch(() => props.specId, () => { selected.value = null; requestText.value = ''; notice.value = ''; error.value = '' })
watch(() => snapshot.value?.readAt, () => { requestText.value = '' })
function saved() { notice.value = '处理意见已保存，待 Agent 回写并核验。请生成更新要求交给 Agent 继续处理。'; emit('saved') }
async function generateRequest() {
  requestLoading.value = true; error.value = ''
  try { requestText.value = (await loadAgentRequest(props.specId)).text; notice.value = '更新要求已生成，可复制给 Agent。' }
  catch (e) { error.value = errorMessage(e) }
  finally { requestLoading.value = false }
}
async function copyRequest() {
  try { await navigator.clipboard.writeText(requestText.value); notice.value = '更新要求已复制，请粘贴给 Agent。' }
  catch { error.value = '无法访问剪贴板，请在下方文本框中手工选择并复制。' }
}
function prepareConfirmation() {
  if (props.content.status === 'confirmed' || !props.content.canConfirm || !props.content.artifactDigest) return
  confirmation.value = { scope: props.content.scope, artifactDigest: props.content.artifactDigest, exclusions: [...exclusions.value], revision: snapshot.value?.revision ?? 0 }
  accepted.value = false; error.value = ''; confirmOpen.value = true
}
async function confirmContent() {
  if (!accepted.value || !confirmation.value) return
  try {
    await submit({ kind: 'confirmContent', specId: props.specId, scope: confirmation.value.scope, artifactDigest: confirmation.value.artifactDigest }, confirmation.value.revision)
    confirmOpen.value = false; notice.value = '已确认本次范围与内容版本；来源变化后需要重新确认。'; emit('saved')
  } catch (e) { error.value = errorMessage(e) }
}
</script>
<template>
  <section class="panel content-panel">
    <div class="section-heading"><h2>内容确认 <span class="content-status" :data-status="content.status">{{ contentStatuses[content.status] }}</span></h2><Button :disabled="content.status === 'confirmed' || !content.canConfirm || saving" @click="prepareConfirmation">{{ content.status === 'confirmed' ? '已确认此版本' : '查看范围并确认' }}</Button></div>
    <p class="muted">确认本次规格、交互说明与原型的范围和版本。个人“已完成”不会代替内容确认，也无需逐阶段批准。</p>
    <div class="content-scope"><strong>本次范围</strong><p class="preserve-lines">{{ content.scope || '尚未标记。请让 Agent 在主规格中明确本次范围。' }}</p></div>
    <div v-if="exclusions.length" class="notice warning"><strong>本次排除项</strong><ul><li v-for="(item, index) in exclusions" :key="index">{{ item }}</li></ul></div>
    <p v-if="content.metadataError" role="alert" class="notice error">{{ content.metadataError }}</p>
    <p v-if="content.needsReconfirmation" class="notice warning">来源内容已变更，之前的确认不适用于当前版本，请重新阅读后确认。</p>
    <div class="reminder-strip" aria-label="当前内容提醒"><span v-for="item in reminders" :key="item.id"><strong>{{ item.count }}</strong>{{ item.label }}</span></div>
    <ul v-if="content.blockers.length" class="content-blockers"><li v-for="blocker in content.blockers" :key="blocker">{{ blocker }}</li></ul>
    <p v-if="latestConfirmation" class="muted">最近确认：{{ dateLabel(latestConfirmation.createdAt) }} · 版本 {{ latestConfirmation.artifactDigest.slice(0, 12) }}</p>
    <div class="section-heading questions-heading"><h2>待确认事项 <span class="count">{{ pending.length }}</span></h2><Button variant="outline" :disabled="requestLoading || saving" @click="generateRequest">{{ requestLoading ? '正在生成…' : '生成 Agent 更新要求' }}</Button></div>
    <p class="muted">答复与补充先记录为待回写；Agent 更新实际内容并核验后，事项才会解决。</p>
    <ul v-if="content.questions.length" class="question-list">
      <li v-for="item in content.questions" :key="item.question.id" class="question-item">
        <div class="section-heading"><h3>{{ item.question.title }}</h3><span class="question-state" :data-state="item.state">{{ questionStateLabel(item) }}</span></div>
        <p class="path-text">来源：{{ sourcePath }} · {{ item.question.id }}<span v-if="item.question.blocking"> · 影响本次确认</span></p>
        <dl class="question-facts"><div><dt>影响</dt><dd>{{ item.question.impact || '未说明' }}</dd></div><div><dt>涉及范围</dt><dd>{{ item.question.affectedScope || '未说明' }}</dd></div><div><dt>推荐答案</dt><dd>{{ item.question.recommendation || '暂无建议，请补充依据后判断。' }}</dd></div></dl>
        <div v-if="item.response" class="response-summary"><strong>{{ item.response.kind === 'answer' ? '已保存答复' : '延期安排' }}</strong><p class="preserve-lines">{{ item.response.kind === 'answer' ? item.response.answer : item.response.reason }}</p><template v-if="item.response.kind === 'defer'"><p>影响：{{ item.response.impact }}</p><p>排除：{{ item.response.excludedScope }}</p><p>复查：{{ item.response.reviewAt || '未设日期' }}{{ item.response.reviewWhen ? `；${item.response.reviewWhen}` : '' }}</p></template></div>
        <p v-if="item.question.resolution" class="preserve-lines">{{ item.state === 'resolved' ? '回写结论' : '来源记录' }}：{{ item.question.resolution }}</p>
        <div v-if="item.state !== 'resolved'" class="inline-actions"><Button variant="outline" :aria-label="`${item.question.kind === 'research' ? '补充依据' : '答复'}：${item.question.title}`" :disabled="saving" @click="selected = item">{{ item.question.kind === 'research' ? '补充依据 / 延期' : '答复 / 延期' }}</Button><span v-if="item.state === 'needsReview'" class="warning-text">问题内容已变化，原答复须重新核对。</span></div>
      </li>
    </ul>
    <p v-else class="empty">暂无结构化待确认事项；未标记内容不代表已确认。</p>
    <div v-if="requestText" class="request-output"><div class="section-heading"><label for="agent-update-request">给 Agent 的更新要求</label><Button variant="outline" @click="copyRequest">复制更新要求</Button></div><textarea id="agent-update-request" :value="requestText" readonly rows="10" /></div>
    <p v-if="notice" role="status" class="notice">{{ notice }}</p><p v-if="error && !confirmOpen" role="alert" class="notice error">{{ error }}</p>
    <QuestionResponseForm :spec-id="specId" :item="selected" :confirm-leave="confirmLeave" @close="selected = null" @dirty="emit('dirty', $event)" @saved="saved" />
    <ModalDialog v-model="confirmOpen" title="确认本次内容范围" :busy="saving">
      <form v-if="confirmation" class="form-stack" @submit.prevent="confirmContent"><div><strong>确认范围</strong><p class="preserve-lines">{{ confirmation.scope }}</p></div><div><strong>本次排除项</strong><ul v-if="confirmation.exclusions.length"><li v-for="(item, index) in confirmation.exclusions" :key="index">{{ item }}</li></ul><p v-else>无</p></div><p class="path-text">内容版本：{{ confirmation.artifactDigest }}</p><label class="check-label" for="accept-content-confirmation"><input id="accept-content-confirmation" v-model="accepted" type="checkbox" required :disabled="saving">我已阅读并确认上述范围、排除项与当前内容版本。</label><p class="muted">记录仅适用于本次范围与版本；文件变化后须重新确认。</p><p v-if="error" role="alert" class="notice error">{{ error }}</p><div class="form-actions"><Button type="button" variant="outline" :disabled="saving" @click="confirmOpen = false">返回阅读</Button><Button :disabled="!accepted || saving">确认此范围与版本</Button></div></form>
    </ModalDialog>
  </section>
</template>
<style scoped>
.content-panel { padding: 22px; margin-bottom: 20px; }
.content-status { font-size: 12px; color: var(--yss-color-primary-control); margin-left: 12px; }
.content-status[data-status=confirmed] { color: #278064; }
.content-scope { margin: 16px 0; }
.content-scope p { margin-top: 6px; }
.reminder-strip { display: flex; gap: 22px; flex-wrap: wrap; padding: 16px 0; }
.reminder-strip span { display: flex; align-items: baseline; gap: 7px; color: #717780; }
.reminder-strip strong { color: #313944; font-size: 18px; }
.content-blockers { margin: 0 0 14px; padding-left: 20px; list-style: disc; color: #a15b12; }
.questions-heading { margin-top: 22px; }
.question-list { list-style: none; margin: 0; padding: 0; }
.question-item { padding: 18px 0; border-bottom: 1px solid var(--yss-colors-border); }
.question-item:last-child { border-bottom: 0; }
.question-state { color: #a15b12; white-space: nowrap; font-size: 12px; }
.question-state[data-state=resolved] { color: #278064; }
.question-facts { margin: 12px 0; }
.question-facts > div { display: grid; grid-template-columns: 76px 1fr; gap: 10px; margin: 8px 0; }
.question-facts dt { color: #717780; }
.question-facts dd { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; }
.response-summary { background: #f7f8fa; padding: 12px; border-radius: 8px; margin: 12px 0; }
.question-item .inline-actions { margin-top: 12px; }
.request-output { margin: 16px 0; }
@media (width < 720px) { .content-panel > .section-heading { flex-wrap: wrap; }.question-item .section-heading { align-items: start; }.reminder-strip { gap: 14px; } }
</style>
