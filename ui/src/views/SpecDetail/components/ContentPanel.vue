<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import ModalDialog from '@/components/ModalDialog.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { contentStatuses, dateLabel, errorMessage, type ContentView, type QuestionView } from '@/services/workbench'
import { canCopyQuestionResponse, formatQuestionReplies, questionStateLabel } from '@/services/workbench-content'
import QuestionResponseForm from './QuestionResponseForm.vue'
const props = defineProps<{ specId: string; sourcePath: string; projectRoot?: string; content: ContentView; confirmLeave: (required: boolean) => boolean | Promise<boolean> }>()
const emit = defineEmits<{ dirty: [value: boolean]; saved: [] }>()
const { snapshot, submit, saving } = useWorkbench()
const selected = shallowRef<QuestionView | null>(null); const notice = shallowRef(''); const error = shallowRef('')
const manualCopy = shallowRef(''); const manualOpen = shallowRef(false)
const confirmOpen = shallowRef(false); const accepted = shallowRef(false)
const confirmation = shallowRef<{ scope: string; artifactDigest: string; exclusions: string[]; revision: number }>()
const exclusions = computed(() => props.content.questions.filter(item => item.response?.kind === 'defer' && item.state !== 'resolved').map(item => item.response?.excludedScope ?? '').filter(Boolean))
const latestConfirmation = computed(() => props.content.confirmations.at(-1))
const pending = computed(() => props.content.questions.filter(item => item.state !== 'resolved'))
const copyable = computed(() => props.content.questions.filter(canCopyQuestionResponse))
watch(() => props.specId, () => { selected.value = null; error.value = '' })
watch([() => props.specId, () => snapshot.value?.readAt], () => { manualOpen.value = false; manualCopy.value = ''; notice.value = '' })
function saved() { notice.value = '答复已保存。可直接复制答复提示词，交给 Agent 回写并核验。'; emit('saved') }
async function copyReplies(items: QuestionView[]) {
  const text = formatQuestionReplies(props.sourcePath, items, props.projectRoot)
  if (!text) return
  const specId = props.specId
  const readAt = snapshot.value?.readAt
  error.value = ''
  try {
    await navigator.clipboard.writeText(text)
    if (specId !== props.specId || readAt !== snapshot.value?.readAt) return
    const message = `已复制 ${items.filter(canCopyQuestionResponse).length} 项答复，可直接粘贴给 Agent。`
    notice.value = notice.value === message ? `${message}\u00a0` : message
    manualOpen.value = false
  } catch {
    if (specId !== props.specId || readAt !== snapshot.value?.readAt) return
    notice.value = ''; manualCopy.value = text; manualOpen.value = true
  }
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
  <div class="content-panel">
    <section class="panel confirmation-summary" aria-label="内容确认">
      <div class="section-heading"><div><h2>内容确认 <span class="content-status" :data-status="content.status">{{ contentStatuses[content.status] }}</span></h2><p class="muted">答复回写并核验后，再确认本次范围与版本。</p></div><Button :disabled="content.status === 'confirmed' || !content.canConfirm || saving" @click="prepareConfirmation">{{ content.status === 'confirmed' ? '已确认此版本' : '查看范围并确认' }}</Button></div>
      <details class="content-scope"><summary>本次确认范围</summary><p class="preserve-lines">{{ content.scope || '尚未标记。请让 Agent 在主规格中明确本次范围。' }}</p></details>
      <div v-if="exclusions.length" class="notice warning"><strong>本次排除项</strong><ul><li v-for="(item, index) in exclusions" :key="index">{{ item }}</li></ul></div>
      <p v-if="content.metadataError" role="alert" class="notice error">{{ content.metadataError }}</p>
      <p v-if="content.needsReconfirmation" class="notice warning">来源内容已变更，之前的确认不适用于当前版本，请重新阅读后确认。</p>
      <details v-if="content.blockers.length" class="confirmation-blockers"><summary>查看确认限制（{{ content.blockers.length }}）</summary><ul><li v-for="blocker in content.blockers" :key="blocker">{{ blocker }}</li></ul></details>
      <p v-if="latestConfirmation" class="muted">最近确认：{{ dateLabel(latestConfirmation.createdAt) }} · 版本 {{ latestConfirmation.artifactDigest.slice(0, 12) }}</p>
    </section>
    <div class="section-heading questions-heading"><div><h2>待确认事项 <span class="count">{{ content.questions.length }}</span></h2><p class="muted">{{ pending.length }} 项未解决 · {{ content.questions.length - pending.length }} 项已解决</p></div><Button type="button" variant="outline" :disabled="!copyable.length || saving" @click="copyReplies(copyable)">复制全部答复<span v-if="copyable.length">（{{ copyable.length }}）</span></Button></div>
    <p class="copy-hint muted">复制已保存的答复与延期安排，即可作为回复提示词交给 Agent。已变更或到期的事项请先复查。</p>
    <p role="status" class="copy-notice">{{ notice }}</p><p v-if="error && !confirmOpen" role="alert" class="notice error">{{ error }}</p>
    <ul v-if="content.questions.length" class="question-list" aria-label="待确认事项卡片">
      <li v-for="item in content.questions" :key="item.question.id" class="panel question-card">
        <div class="question-heading"><div class="question-title"><span class="question-id">{{ item.question.id }}</span><h3>{{ item.question.title }}</h3></div><span class="question-state" :data-state="item.state">{{ questionStateLabel(item) }}</span></div>
        <dl class="question-facts"><div><dt>涉及范围</dt><dd>{{ item.question.affectedScope || '未说明' }}<span v-if="item.question.blocking" class="blocking-label">影响本次确认</span></dd></div><div><dt>影响</dt><dd>{{ item.question.impact || '未说明' }}</dd></div></dl>
        <div v-if="item.response" class="response-summary" :class="{ outdated: !canCopyQuestionResponse(item) }"><strong>{{ !canCopyQuestionResponse(item) ? '原答复，待复查' : item.response.kind === 'answer' ? '已保存答复' : '已保存延期安排' }}</strong><p class="preserve-lines">{{ item.response.kind === 'answer' ? item.response.answer : item.response.reason }}</p><template v-if="item.response.kind === 'defer'"><p>影响：{{ item.response.impact }}</p><p>排除：{{ item.response.excludedScope }}</p><p>复查：{{ item.response.reviewAt || '未设日期' }}{{ item.response.reviewWhen ? `；${item.response.reviewWhen}` : '' }}</p></template></div>
        <div v-else class="recommendation"><strong>推荐答案</strong><p>{{ item.question.recommendation || '暂无建议，请补充依据后判断。' }}</p></div>
        <p v-if="item.state === 'needsReview'" class="warning-text">问题内容或延期安排需重新核对，原答复不会作为当前回复复制。</p>
        <p v-if="item.state === 'overdue'" class="warning-text">延期已到期，请复查后重新答复或安排延期。</p>
        <details class="question-details"><summary>查看来源{{ item.response ? '与问题背景' : '记录' }}</summary><p class="path-text">{{ sourcePath }} · {{ item.question.id }}</p><p v-if="item.response && item.question.recommendation" class="preserve-lines">推荐答案：{{ item.question.recommendation }}</p><p v-if="item.question.resolution" class="preserve-lines">{{ item.state === 'resolved' ? '回写结论' : '来源记录' }}：{{ item.question.resolution }}</p></details>
        <div class="question-actions"><Button v-if="item.state !== 'resolved'" type="button" variant="outline" :aria-label="`${item.question.kind === 'research' ? '补充依据' : '答复'}：${item.question.title}`" :disabled="saving" @click="selected = item">{{ item.response ? '修改答复 / 延期' : item.question.kind === 'research' ? '补充依据 / 延期' : '答复 / 延期' }}</Button><Button v-if="canCopyQuestionResponse(item)" type="button" variant="ghost" :aria-label="`复制答复：${item.question.title}`" :disabled="saving" @click="copyReplies([item])">复制答复</Button><span v-if="item.state === 'pendingWriteback'" class="muted">待 Agent 回写并核验</span></div>
      </li>
    </ul>
    <p v-else class="panel empty">暂无结构化待确认事项；未标记内容不代表已确认。</p>
    <QuestionResponseForm :spec-id="specId" :item="selected" :confirm-leave="confirmLeave" @close="selected = null" @dirty="emit('dirty', $event)" @saved="saved" />
    <ModalDialog v-model="manualOpen" title="手动复制答复">
      <p role="alert">无法访问剪贴板。请选中以下提示词，手动复制后粘贴给 Agent。</p><label for="reply-prompt">答复提示词</label><textarea id="reply-prompt" :value="manualCopy" readonly rows="12" class="manual-copy" @focus="($event.target as HTMLTextAreaElement).select()" /><div class="form-actions"><Button type="button" variant="outline" @click="manualOpen = false">关闭</Button></div>
    </ModalDialog>
    <ModalDialog v-model="confirmOpen" title="确认本次内容范围" :busy="saving">
      <form v-if="confirmation" class="form-stack" @submit.prevent="confirmContent"><div><strong>确认范围</strong><p class="preserve-lines">{{ confirmation.scope }}</p></div><div><strong>本次排除项</strong><ul v-if="confirmation.exclusions.length"><li v-for="(item, index) in confirmation.exclusions" :key="index">{{ item }}</li></ul><p v-else>无</p></div><p class="path-text">内容版本：{{ confirmation.artifactDigest }}</p><label class="check-label" for="accept-content-confirmation"><input id="accept-content-confirmation" v-model="accepted" type="checkbox" required :disabled="saving">我已阅读并确认上述范围、排除项与当前内容版本。</label><p class="muted">记录仅适用于本次范围与版本；文件变化后须重新确认。</p><p v-if="error" role="alert" class="notice error">{{ error }}</p><div class="form-actions"><Button type="button" variant="outline" :disabled="saving" @click="confirmOpen = false">返回阅读</Button><Button :disabled="!accepted || saving">确认此范围与版本</Button></div></form>
    </ModalDialog>
  </div>
</template>
<style scoped>
.content-panel { margin-bottom:20px; min-width:0; }
.confirmation-summary { padding:18px 20px; }
.confirmation-summary .section-heading { margin-bottom:10px; }
.confirmation-summary .section-heading p, .questions-heading p { margin:5px 0 0; }
.content-status { display:inline-block; margin-left:8px; padding:2px 7px; border-radius:5px; font-size:11px; color:var(--yss-color-primary-control); background:var(--yss-colors-primary-bg); vertical-align:middle; }
.content-status[data-status=confirmed] { color:#278064; background:#edf7f2; }
.content-scope, .confirmation-blockers { margin-top:10px; }
.content-scope summary, .confirmation-blockers summary, .question-details summary { width:fit-content; cursor:pointer; color:var(--brand-color-text-secondary); font-size:12px; padding:3px 0; }
.content-scope p, .confirmation-blockers ul { margin:8px 0 0; }
.confirmation-blockers ul { padding-left:20px; list-style:disc; color:#a15b12; }
.questions-heading { margin:22px 0 8px; align-items:center; }
.copy-hint { margin:0 0 12px; font-size:12px; }
.copy-notice { margin:0; font-size:12px; color:#278064; }
.copy-notice:not(:empty) { margin-bottom:12px; }
.question-list { display:grid; gap:12px; list-style:none; margin:0; padding:0; }
.question-card { padding:18px 20px; min-width:0; }
.question-heading { display:flex; align-items:flex-start; justify-content:space-between; gap:12px; }
.question-title { display:flex; align-items:baseline; gap:10px; min-width:0; }
.question-title h3 { font-size:14px; line-height:1.6; margin:0; overflow-wrap:anywhere; }
.question-id { color:var(--brand-color-text-secondary); font-size:11px; flex:none; }
.question-state { flex:none; color:#96601e; background:#fff5e5; border-radius:5px; padding:3px 7px; font-size:11px; white-space:nowrap; }
.question-state[data-state=resolved] { color:#278064; background:#edf7f2; }
.question-state[data-state=pendingWriteback] { color:var(--yss-color-primary-control); background:var(--yss-colors-primary-bg); }
.question-facts { margin:12px 0; font-size:12px; }
.question-facts > div { display:grid; grid-template-columns:60px minmax(0,1fr); gap:10px; margin:6px 0; }
.question-facts dt { color:var(--brand-color-text-secondary); }
.question-facts dd { margin:0; white-space:pre-wrap; overflow-wrap:anywhere; }
.blocking-label { display:inline-block; margin-left:8px; color:#96601e; font-size:10px; }
.response-summary, .recommendation { background:#f7f8fa; padding:12px 14px; border-radius:8px; margin:12px 0; overflow-wrap:anywhere; }
.response-summary strong, .recommendation strong { color:var(--brand-color-text-secondary); font-size:11px; font-weight:500; }
.response-summary p, .recommendation p { margin:5px 0 0; line-height:1.7; white-space:pre-wrap; }
.response-summary.outdated { background:#fff9ef; }
.question-details { margin-top:10px; }
.question-details p { margin:8px 0 0; font-size:12px; overflow-wrap:anywhere; }
.question-actions { display:flex; flex-wrap:wrap; align-items:center; gap:8px; margin-top:12px; }
.question-actions .muted { margin-left:auto; font-size:11px; }
.manual-copy { display:block; width:100%; margin:8px 0 16px; }
@media (width < 720px) { .confirmation-summary, .question-card { padding:16px; }.confirmation-summary .section-heading, .questions-heading { flex-wrap:wrap; gap:12px; }.question-heading { flex-wrap:wrap; gap:8px; }.question-title { flex-basis:100%; }.question-state { margin-left:0; }.question-actions .muted { width:100%; margin-left:0; } }
</style>
