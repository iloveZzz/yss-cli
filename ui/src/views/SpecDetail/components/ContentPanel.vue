<script setup lang="ts">
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Alert } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { Empty, EmptyContent, EmptyDescription } from '@/components/ui/empty'
import { Checkbox } from '@/components/ui/checkbox'
import { Textarea } from '@/components/ui/textarea'
import { computed, nextTick, onBeforeUnmount, shallowRef, useId, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
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
  matchesContentReminder,
  materialNeedsReview
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
  focusMaterials?: boolean
  confirmLeave: (required: boolean) => boolean | Promise<boolean>
}>()
const route = useRoute()
const router = useRouter()
const id = useId()
const materialHeading = useTemplateRef<HTMLElement>('material-heading')
watch(
  [() => props.focusMaterials, materialHeading],
  ([focus, heading], _previous, onCleanup) => {
    if (!focus || !heading) return
    const frame = requestAnimationFrame(() => {
      heading.focus({ preventScroll: true })
      heading.scrollIntoView({ block: 'start', behavior: 'instant' })
    })
    onCleanup(() => cancelAnimationFrame(frame))
  },
  { flush: 'post' }
)
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
    document.querySelector('[data-slot="dialog-content"][data-state="open"]')
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
const confirmedVersion = computed(() => props.content.confirmedVersion ?? props.content.confirmations.at(-1))
const materialsNeedReview = computed(() => materialNeedsReview(props.content))
const sharedMaterialUpdates = computed(() =>
  props.content.materialChanges?.filter((change) => change.kind === 'sharedUpdated') ?? []
)
const confirmationMaterialChanges = computed(() =>
  props.content.materialChanges?.filter((change) => change.kind !== 'sharedUpdated') ?? []
)
const changeLabels = {
  changed: '确认材料已更新',
  sharedUpdated: '关联资料更新',
  dependencyReview: '依赖待核实',
  unavailable: '来源异常',
  historyUnavailable: '当前适用性待核实'
}
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
    notice.value = '已确认本次范围与所采用的材料版本。'
    emit('saved')
  } catch (e) {
    error.value = errorMessage(e)
  }
}
</script>
<template>
  <div class="content-panel">
    <Card class="confirmation-summary block shadow-none" role="region" aria-label="内容确认">
      <div class="section-heading">
        <div>
          <h2>
            内容确认
            <Badge variant="secondary" class="content-status" :data-status="content.status">
              {{ content.needsReconfirmation ? '新版待确认' : contentStatuses[content.status] }}
            </Badge>
          </h2>
          <p v-if="confirmedVersion" class="muted">
            已确认版本 {{ confirmedVersion.artifactDigest.slice(0, 12) }} ·
            {{ dateLabel(confirmedVersion.createdAt) }}
          </p>
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
      <Collapsible class="content-scope">
        <CollapsibleTrigger>本次确认范围</CollapsibleTrigger>
        <CollapsibleContent>
          <p class="preserve-lines">
            {{ content.scope || '尚未标记。请让 Agent 在主规格中明确本次范围。' }}
          </p>
        </CollapsibleContent>
      </Collapsible>
      <div v-if="exclusions.length" class="notice warning">
        <strong>本次排除项</strong>
        <ul>
          <li v-for="(item, index) in exclusions" :key="index">{{ item }}</li>
        </ul>
      </div>
      <Alert v-if="content.metadataError" role="alert" class="notice error">
        {{ content.metadataError }}
      </Alert>
      <div v-if="content.needsReconfirmation || materialsNeedReview" class="notice warning">
        <h3 ref="material-heading" tabindex="-1">{{ content.needsReconfirmation ? '新版待确认' : '材料待核实' }}</h3>
        <p v-if="content.needsReconfirmation">本 Spec 有新版材料或范围。核对本轮采用的内容后确认。</p>
        <p v-if="materialsNeedReview">由 Agent 核实关联材料及相关条款。已确认版本可在下方回看。</p>
      </div>
      <Collapsible v-if="confirmationMaterialChanges.length" class="confirmation-blockers" default-open>
        <CollapsibleTrigger>确认材料与来源变化</CollapsibleTrigger>
        <CollapsibleContent>
          <ul class="material-change-list">
            <li v-for="change in confirmationMaterialChanges" :key="`${change.kind}:${change.target}`">
              <strong>{{ changeLabels[change.kind] }}</strong>
              <code>{{ change.target }}</code>
              <div>{{ change.reason }}</div>
              <small v-if="change.confirmedDigest || change.observedDigest" class="muted">
                已采用 {{ change.confirmedDigest?.slice(0, 12) ?? '—' }} ·
                当前资料 {{ change.observedDigest?.slice(0, 12) ?? '—' }}
              </small>
            </li>
          </ul>
        </CollapsibleContent>
      </Collapsible>
      <Collapsible v-if="content.blockers.length" class="confirmation-blockers">
        <CollapsibleTrigger>查看确认限制（{{ content.blockers.length }}）</CollapsibleTrigger>
        <CollapsibleContent>
          <ul>
            <li v-for="blocker in content.blockers" :key="blocker">{{ blocker }}</li>
          </ul>
        </CollapsibleContent>
      </Collapsible>
      <Collapsible v-if="sharedMaterialUpdates.length" class="shared-material-updates">
        <div class="shared-material-summary">
          <CollapsibleTrigger :aria-describedby="id + '-shared-material-status'">
            {{ changeLabels.sharedUpdated }}（{{ sharedMaterialUpdates.length }}）
          </CollapsibleTrigger>
          <span :id="id + '-shared-material-status'">仍采用已确认版本</span>
        </div>
        <CollapsibleContent>
          <ul class="material-change-list">
            <li v-for="change in sharedMaterialUpdates" :key="change.target">
              <code>{{ change.target }}</code>
              <div>{{ change.reason }}</div>
              <small v-if="change.confirmedDigest || change.observedDigest" class="muted">
                已采用 {{ change.confirmedDigest?.slice(0, 12) ?? '—' }} ·
                当前资料 {{ change.observedDigest?.slice(0, 12) ?? '—' }}
              </small>
            </li>
          </ul>
        </CollapsibleContent>
      </Collapsible>
      <ConfirmationDiff :spec-id="specId" />
    </Card>
    <div class="section-heading questions-heading">
      <div>
        <h2 :id="id + '-questions'" tabindex="-1">
          待确认事项
          <Badge variant="secondary" class="count">{{ content.questions.length }}</Badge>
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
        <Checkbox :model-value="showResolved" @update:model-value="showResolved = $event === true" name="resolved" />
        显示已解决（{{ content.questions.length - pending.length }}）
      </label>
      <span v-if="category !== 'ready'" class="muted">显示 {{ visibleQuestions.length }} 项</span>
    </div>
    <p v-if="notice" role="status" class="copy-notice">{{ notice }}</p>
    <Alert v-if="error && !confirmOpen" role="alert" class="notice error">{{ error }}</Alert>
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
    <Empty v-else class="question-empty">
      <EmptyDescription v-if="!content.questions.length">主规格尚未列出待确认事项。</EmptyDescription>
      <EmptyDescription v-else>
        {{ category === 'all' ? '当前没有未解决事项，可显示已解决记录。' : '当前类别没有待处理事项。' }}
      </EmptyDescription>
      <EmptyContent v-if="content.questions.length && category !== 'all'">
        <Button type="button" variant="ghost" @click="category = 'all'">查看全部事项</Button>
      </EmptyContent>
    </Empty>
    <Collapsible v-if="content.questions.length" class="copy-help">
      <CollapsibleTrigger>答复如何交给 Agent</CollapsibleTrigger>
      <CollapsibleContent>
        <p>复制已保存的答复与延期安排，即可作为回复提示词交给 Agent。已变更或到期的事项请先复查。</p>
      </CollapsibleContent>
    </Collapsible>
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
      <Textarea
        id="reply-prompt"
        :model-value="manualCopy?.text"
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
          <Checkbox
            id="accept-content-confirmation"
            :model-value="accepted" @update:model-value="accepted = $event === true"
            required
            :disabled="saving"
          />
          我已阅读并确认上述范围、排除项与当前内容版本。
        </label>
        <p class="muted">本 Spec 的确认范围或采用的材料变化后，需要确认新版本。</p>
        <Alert v-if="error" role="alert" class="notice error">{{ error }}</Alert>
        <div class="form-actions">
          <Button type="button" variant="outline" :disabled="saving" @click="confirmOpen = false">
            返回阅读
          </Button>
          <Button :disabled="!accepted || saving">
            <LoadingIndicator :active="saving" />
            {{ saving ? '正在确认…' : '确认此范围与版本' }}
          </Button>
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
  color: var(--yss-color-primary-control-hover);
  background: var(--yss-colors-primary-bg);
  vertical-align: middle;
}
.content-status[data-status='confirmed'] {
  color: #1b6e52;
  background: #edf7f2;
}
.content-scope,
.confirmation-blockers,
.shared-material-updates {
  margin-top: 10px;
}
.content-scope [data-slot='collapsible-trigger'],
.confirmation-blockers [data-slot='collapsible-trigger'],
.shared-material-updates [data-slot='collapsible-trigger'],
.copy-help [data-slot='collapsible-trigger'] {
  width: fit-content;
  cursor: pointer;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
  padding: 3px 0;
}
.content-scope p,
.confirmation-blockers ul,
.shared-material-updates ul {
  margin: 8px 0 0;
}
.confirmation-blockers ul,
.shared-material-updates ul {
  padding-left: 20px;
  list-style: disc;
}
.confirmation-blockers ul {
  color: #a15b12;
}
.shared-material-updates {
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.shared-material-summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0 10px;
}
.material-change-list li + li {
  margin-top: 8px;
}
.material-change-list code {
  display: block;
  overflow-wrap: anywhere;
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
  color: var(--brand-color-text-secondary);
  font-size: 12px;
  line-height: 1.8;
}
.copy-help {
  margin-top: 12px;
}
.copy-help p {
  margin: 8px 0 0;
  color: var(--brand-color-text-secondary);
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
