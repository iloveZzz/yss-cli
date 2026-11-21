<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, shallowRef, useId, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import ModalDialog from '@/components/ModalDialog.vue'
import SingleSelect from '@/components/SingleSelect.vue'
import { useQuestionReviewDeadline } from '@/hooks/useQuestionReviewDeadline'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  contentStatuses,
  dateLabel,
  errorMessage,
  type AgentRequest,
  type ContentView,
  type QuestionView
} from '@/services/workbench'
import {
  canCopyQuestionResponse,
  formatQuestionReplies,
  matchesContentReminder
} from '@/services/workbench-content'
import QuestionResponseForm from './QuestionResponseForm.vue'
import ConfirmationDiff from './ConfirmationDiff.vue'
import QuestionList from './QuestionList.vue'
import AgentRequestPreview from './AgentRequestPreview.vue'
import { getAgentRequest, getSpecContent } from '@/services/workbench-platform'
const props = defineProps<{
  specId: string
  sourcePath: string
  projectRoot?: string
  content: ContentView
  confirmLeave: (required: boolean) => boolean | Promise<boolean>
}>()
const route = useRoute()
const router = useRouter()
const id = useId()
const categories = [
  { value: 'all', label: '全部事项' },
  { value: 'decision', label: '待你决定' },
  { value: 'research', label: '待补充' },
  { value: 'overdue', label: '需要复查' },
  { value: 'writeback', label: '待回写' },
  { value: 'ready', label: 'Spec 确认状态' }
]
const showResolved = computed({
  get: () => route.query.resolved === 'true',
  set: (value) => {
    void router.replace({ query: { ...route.query, resolved: value ? 'true' : undefined } })
  }
})
const category = computed({
  get: () =>
    categories.some((item) => item.value === route.query.category)
      ? String(route.query.category)
      : 'all',
  set: (value) => {
    void router.replace({
      query: {
        ...route.query,
        category: value === 'all' ? undefined : value,
        questionId: undefined
      }
    })
  }
})
const visibleQuestions = computed(() =>
  props.content.questions.filter((item) => {
    if (route.query.questionId === item.question.id) return true
    if (category.value === 'ready') return false
    if (category.value !== 'all') return matchesContentReminder(item, category.value)
    return item.state !== 'resolved' || showResolved.value
  })
)
const emit = defineEmits<{ dirty: [value: boolean]; saved: [] }>()
const { snapshot, submit, saving } = useWorkbench()
const selected = shallowRef<QuestionView | null>(null)
let responseTrigger: HTMLButtonElement | undefined
let mounted = true
onBeforeUnmount(() => {
  mounted = false
})
function openQuestion(item: QuestionView, trigger: HTMLButtonElement) {
  responseTrigger = trigger
  selected.value = item
}
async function closeQuestion() {
  if (!selected.value) return
  const specId = props.specId
  const trigger = responseTrigger
  responseTrigger = undefined
  selected.value = null
  await nextTick()
  if (
    !mounted ||
    props.specId !== specId ||
    selected.value ||
    route.query.tab !== 'content' ||
    document.querySelector('dialog[open]')
  )
    return
  const original =
    trigger?.isConnected && !trigger.disabled && trigger.getClientRects().length ? trigger : null
  const target =
    original ??
    document.getElementById(id + '-category') ??
    document.getElementById(id + '-questions')
  if (target !== document.activeElement) target?.focus()
}
const notice = shallowRef('')
const error = shallowRef('')
const manualCopy = shallowRef<{ text: string; questions: QuestionView[]; readAt: string }>()
const manualOpen = shallowRef(false)
const requestOpen = shallowRef(false)
const confirmOpen = shallowRef(false)
const accepted = shallowRef(false)
const confirmation = shallowRef<{
  scope: string
  artifactDigest: string
  exclusions: string[]
  revision: number
}>()
const exclusions = computed(() =>
  props.content.questions
    .filter((item) => canCopyQuestionResponse(item) && item.response?.kind === 'defer' && item.state !== 'resolved')
    .map((item) => item.response?.excludedScope ?? '')
    .filter(Boolean)
)
const latestConfirmation = computed(() => props.content.confirmations.at(-1))
const pending = computed(() => props.content.questions.filter((item) => item.state !== 'resolved'))
const copyable = computed(() => props.content.questions.filter(canCopyQuestionResponse))
let copyAttempt = 0
watch(
  [() => props.specId, () => route.query.tab],
  ([specId], [previousSpecId]) => {
    copyAttempt++
    if (specId !== previousSpecId) selected.value = null
    requestOpen.value = false
    manualOpen.value = false
    manualCopy.value = undefined
    error.value = ''
  }
)
watch([() => props.specId, () => snapshot.value?.readAt], () => {
  manualOpen.value = false
  manualCopy.value = undefined
  notice.value = ''
})
useQuestionReviewDeadline(() => manualOpen.value ? manualCopy.value : undefined, async () => {
  manualOpen.value = false
  manualCopy.value = undefined
  notice.value = '已有延期到达复查日期，请重新复制有效答复。'
})
function saved(responseId?: string | null) {
  notice.value = `答复已保存${responseId ? `（${responseId}）` : ''}。可在问题详情中查看回写状态。`
  emit('saved')
}
async function copyReplies(selected?: QuestionView) {
  const attempt = ++copyAttempt
  const specId = props.specId
  const isCurrent = () =>
    mounted && route.query.tab === 'content' && specId === props.specId && attempt === copyAttempt
  error.value = ''
  notice.value = ''
  let current: QuestionView[]
  let request: AgentRequest
  try {
    request = await getAgentRequest(specId)
    const questions = request.questions ?? (await getSpecContent(specId)).questions
    if (!isCurrent()) return
    current = questions.filter((item) =>
      canCopyQuestionResponse(item) &&
      (!selected || (item.question.id === selected.question.id && item.response?.id === selected.response?.id))
    )
  } catch (cause) {
    if (isCurrent()) error.value = `未复制。读取最新答复失败：${errorMessage(cause)}`
    return
  }
  const text = formatQuestionReplies(
    request.sourcePath ?? props.sourcePath, current, request.projectRoot ?? props.projectRoot, request
  )
  if (!text) {
    notice.value = selected ? '选中的答复已变更或已到复查日期，请先复查。' : '当前没有可复制的有效答复。'
    return
  }
  try {
    await navigator.clipboard.writeText(text)
    if (!isCurrent()) return
    notice.value = `已复制 ${current.length} 项有效答复，可直接粘贴给 Agent。`
    manualOpen.value = false
  } catch {
    if (!isCurrent()) return
    notice.value = ''
    manualCopy.value = { text, questions: current, readAt: request.readAt }
    manualOpen.value = true
  }
}
function prepareConfirmation() {
  if (
    props.content.status === 'confirmed' ||
    !props.content.canConfirm ||
    !props.content.artifactDigest
  )
    return
  confirmation.value = {
    scope: props.content.scope,
    artifactDigest: props.content.artifactDigest,
    exclusions: [...exclusions.value],
    revision: snapshot.value?.revision ?? 0
  }
  accepted.value = false
  error.value = ''
  confirmOpen.value = true
}
async function confirmContent() {
  if (!accepted.value || !confirmation.value) return
  try {
    await submit(
      {
        kind: 'confirmContent',
        specId: props.specId,
        scope: confirmation.value.scope,
        artifactDigest: confirmation.value.artifactDigest
      },
      confirmation.value.revision
    )
    confirmOpen.value = false
    notice.value = '已确认本次范围与内容版本；来源变化后需要重新确认。'
    emit('saved')
  } catch (e) {
    error.value = errorMessage(e)
  }
}
</script>
<template>
  <div class="content-panel">
    <section class="panel confirmation-summary" aria-label="内容确认">
      <div class="section-heading">
        <div>
          <h2>
            内容确认
            <span class="content-status" :data-status="content.status">
              {{ contentStatuses[content.status] }}
            </span>
          </h2>
          <p class="muted">答复回写并核验后，再确认本次范围与版本。</p>
        </div>
        <div class="inline-actions">
          <Button type="button" variant="outline" @click="requestOpen = true">查看 Agent 续办文本</Button>
          <Button
            :disabled="content.status === 'confirmed' || !content.canConfirm || saving"
            @click="prepareConfirmation"
          >
            {{ content.status === 'confirmed' ? '已确认此版本' : '查看范围并确认' }}
          </Button>
        </div>
      </div>
      <details class="content-scope">
        <summary>本次确认范围</summary>
        <p class="preserve-lines">
          {{ content.scope || '尚未标记。请让 Agent 在主规格中明确本次范围。' }}
        </p>
      </details>
      <div v-if="exclusions.length" class="notice warning">
        <strong>本次排除项</strong>
        <ul>
          <li v-for="(item, index) in exclusions" :key="index">{{ item }}</li>
        </ul>
      </div>
      <p v-if="content.metadataError" role="alert" class="notice error">
        {{ content.metadataError }}
      </p>
      <p v-if="content.needsReconfirmation" class="notice warning">
        来源内容已变更，之前的确认不适用于当前版本，请重新阅读后确认。
      </p>
      <details v-if="content.blockers.length" class="confirmation-blockers">
        <summary>查看确认限制（{{ content.blockers.length }}）</summary>
        <ul>
          <li v-for="blocker in content.blockers" :key="blocker">{{ blocker }}</li>
        </ul>
      </details>
      <ConfirmationDiff :spec-id="specId" />
      <p v-if="latestConfirmation" class="muted">
        最近确认：{{ dateLabel(latestConfirmation.createdAt) }} · 版本
        {{ latestConfirmation.artifactDigest.slice(0, 12) }}
      </p>
    </section>
    <div class="section-heading questions-heading">
      <div>
        <h2 :id="id + '-questions'" tabindex="-1">
          待确认事项
          <span class="count">{{ content.questions.length }}</span>
        </h2>
        <p class="muted">
          {{ pending.length }} 项未解决 · {{ content.questions.length - pending.length }} 项已解决
        </p>
      </div>
      <Button
        v-if="copyable.length"
        type="button"
        variant="outline"
        :disabled="saving"
        @click="copyReplies()"
      >
        复制有效答复
        <span v-if="copyable.length">（{{ copyable.length }}）</span>
      </Button>
    </div>
    <div class="toolbar question-filter">
      <label :for="id + '-category'" class="inline-field">
        事项范围
        <SingleSelect
          :id="id + '-category'"
          v-model="category"
          :options="categories"
          name="category"
        />
      </label>
      <label v-if="category === 'all' && content.questions.length > pending.length" class="check-label">
        <input v-model="showResolved" type="checkbox" name="resolved" />
        显示已解决（{{ content.questions.length - pending.length }}）
      </label>
      <span v-if="category !== 'ready'" class="muted">显示 {{ visibleQuestions.length }} 项</span>
    </div>
    <p v-if="notice" role="status" class="copy-notice">{{ notice }}</p>
    <p v-if="error && !confirmOpen" role="alert" class="notice error">{{ error }}</p>
    <p v-if="category === 'ready' && !visibleQuestions.length" class="confirmation-category">
      “可确认”表示整份 Spec 的内容状态，请在上方核对范围与版本后确认。问题可通过其他事项范围查看。
    </p>
    <QuestionList
      v-else-if="visibleQuestions.length"
      :spec-id="specId"
      :source-path="sourcePath"
      :items="visibleQuestions"
      :saving="saving"
      @answer="openQuestion"
      @copy="copyReplies"
    />
    <p v-else-if="!content.questions.length" class="question-empty">主规格尚未列出待确认事项。</p>
    <p v-else class="question-empty">
      {{
        category === 'all' ? '当前没有未解决事项，可显示已解决记录。' : '当前类别没有待处理事项。'
      }}
      <Button v-if="category !== 'all'" type="button" variant="ghost" @click="category = 'all'">
        查看全部事项
      </Button>
    </p>
    <details v-if="content.questions.length" class="copy-help">
      <summary>答复如何交给 Agent</summary>
      <p>复制已保存的答复与延期安排，即可作为回复提示词交给 Agent。已变更或到期的事项请先复查。</p>
    </details>
    <QuestionResponseForm
      :spec-id="specId"
      :item="selected"
      :confirm-leave="confirmLeave"
      @close="closeQuestion"
      @dirty="emit('dirty', $event)"
      @saved="saved"
    />
    <AgentRequestPreview v-model="requestOpen" :spec-id="specId" />
    <ModalDialog v-model="manualOpen" title="手动复制答复">
      <p role="alert">无法访问剪贴板。请选中以下提示词，手动复制后粘贴给 Agent。</p>
      <label for="reply-prompt">答复提示词</label>
      <textarea
        id="reply-prompt"
        :value="manualCopy?.text"
        readonly
        rows="12"
        class="manual-copy"
        @focus="($event.target as HTMLTextAreaElement).select()"
      />
      <div class="form-actions">
        <Button type="button" variant="outline" @click="manualOpen = false">关闭</Button>
      </div>
    </ModalDialog>
    <ModalDialog v-model="confirmOpen" title="确认本次内容范围" :busy="saving">
      <form v-if="confirmation" class="form-stack" @submit.prevent="confirmContent">
        <div>
          <strong>确认范围</strong>
          <p class="preserve-lines">{{ confirmation.scope }}</p>
        </div>
        <div>
          <strong>本次排除项</strong>
          <ul v-if="confirmation.exclusions.length">
            <li v-for="(item, index) in confirmation.exclusions" :key="index">{{ item }}</li>
          </ul>
          <p v-else>无</p>
        </div>
        <p class="path-text">内容版本：{{ confirmation.artifactDigest }}</p>
        <label class="check-label" for="accept-content-confirmation">
          <input
            id="accept-content-confirmation"
            v-model="accepted"
            type="checkbox"
            required
            :disabled="saving"
          />
          我已阅读并确认上述范围、排除项与当前内容版本。
        </label>
        <p class="muted">记录仅适用于本次范围与版本；文件变化后须重新确认。</p>
        <p v-if="error" role="alert" class="notice error">{{ error }}</p>
        <div class="form-actions">
          <Button type="button" variant="outline" :disabled="saving" @click="confirmOpen = false">
            返回阅读
          </Button>
          <Button :disabled="!accepted || saving">确认此范围与版本</Button>
        </div>
      </form>
    </ModalDialog>
  </div>
</template>
<style scoped>
.content-panel {
  margin-bottom: 20px;
  min-width: 0;
}
.confirmation-summary {
  padding: 16px 18px;
}
.confirmation-summary .section-heading {
  margin-bottom: 10px;
}
.confirmation-summary .section-heading p,
.questions-heading p {
  margin: 5px 0 0;
  font-size: 12px;
}
.content-status {
  display: inline-block;
  margin-left: 8px;
  padding: 2px 7px;
  border-radius: 5px;
  font-size: 11px;
  color: var(--yss-color-primary-control);
  background: var(--yss-colors-primary-bg);
  vertical-align: middle;
}
.content-status[data-status='confirmed'] {
  color: #278064;
  background: #edf7f2;
}
.content-scope,
.confirmation-blockers {
  margin-top: 10px;
}
.content-scope summary,
.confirmation-blockers summary,
.copy-help summary {
  width: fit-content;
  cursor: pointer;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
  padding: 3px 0;
}
.content-scope p,
.confirmation-blockers ul {
  margin: 8px 0 0;
}
.confirmation-blockers ul {
  padding-left: 20px;
  list-style: disc;
  color: #a15b12;
}
.questions-heading {
  margin: 22px 0 8px;
  align-items: center;
  flex-wrap: wrap;
}
.copy-notice {
  margin: 0 0 12px;
  font-size: 12px;
  color: #278064;
}
.question-filter {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px 20px;
  margin: 12px 0;
}
.question-filter > label {
  min-width: 0;
}
.question-filter :deep([data-slot='select-trigger']) {
  min-width: 170px;
}
.question-filter > .muted {
  font-size: 11px;
}
.confirmation-diff {
  margin: 14px 0;
}
.confirmation-category,
.question-empty {
  padding: 16px 0;
  color: #717780;
  font-size: 12px;
  line-height: 1.8;
}
.copy-help {
  margin-top: 12px;
}
.copy-help p {
  margin: 8px 0 0;
  color: #858d99;
  font-size: 11px;
}
.manual-copy {
  margin-top: 8px;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 12px;
}
@media (width<720px) {
  .confirmation-summary .section-heading {
    align-items: flex-start;
    flex-wrap: wrap;
  }
  .question-filter {
    gap: 10px;
  }
  .question-filter > label:first-child {
    flex-basis: 100%;
  }
}
</style>
