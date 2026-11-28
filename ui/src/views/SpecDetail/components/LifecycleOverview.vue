<script setup lang="ts">
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Alert } from '@/components/ui/alert'
import { computed, shallowRef, useId, useTemplateRef, watch } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { Button } from '@/components/ui/button'
import ProgressOverrideForm from '@/components/ProgressOverrideForm.vue'
import {
  dateLabel,
  errorMessage,
  progressLabels,
  sourceLabels,
  stageState,
  stages,
  statuses,
  targetLabel,
  validityLabels,
  type Progress,
  type ProgressSummary,
  type Stage
} from '@/services/workbench'
import LifecycleGoalForm from './LifecycleGoalForm.vue'
import ProgressEvidence from './ProgressEvidence.vue'
import ParticipantProgressList from './ParticipantProgressList.vue'
import ProgressConfirmation from './ProgressConfirmation.vue'
import LifecycleProgressHistory from './LifecycleProgressHistory.vue'
import LifecycleRunActivities from './LifecycleRunActivities.vue'
import LifecycleGoalCancel from './LifecycleGoalCancel.vue'
import { useLifecycleGoalChange } from '../hooks/useLifecycleGoalChange'
const props = defineProps<{
  specId: string
  active: boolean
  projectId: string
  specDigest: string | null
  registrationActive: boolean
  sourceUsable: boolean
  progress?: ProgressSummary
  initialStage?: Stage
  stageFocus?: { stage: Stage }
  confirmLeave: (dirty: boolean) => boolean | Promise<boolean>
}>()
const emit = defineEmits<{
  dirty: [value: boolean]
  saved: []
  select: [stage: Stage]
  focused: []
  confirmContent: []
}>()
const { submit, saving, expired } = useWorkbench()
const change = useLifecycleGoalChange(props.specId)
const id = useId()
const chosenStage = shallowRef<Stage>()
const selectedStage = computed(() =>
  chosenStage.value ??
  (props.initialStage === 'unmarked' ? undefined : props.initialStage) ??
  props.progress?.primaryStage ??
  'research'
)
const selected = computed(
  () => props.progress?.stages.find((stage) => stage.stage === selectedStage.value)
)
const stageHeading = useTemplateRef<HTMLElement>('stage-heading')
const goalStatus = useTemplateRef<HTMLElement>('goal-status')
const goalOpen = shallowRef(false)
const cancelOpen = shallowRef(false)
const reopen = shallowRef(false)
const overrideOpen = shallowRef(false)
const overrideProject = shallowRef<string>()
const overrideValue = shallowRef<Progress>()
const error = shallowRef('')
const notice = shallowRef('')
const busy = computed(() => saving.value || change.busy.value)
const writable = computed(() => !expired.value && props.registrationActive)
const ended = computed(() => props.progress?.status === 'done' || props.progress?.status === 'canceled')
const canCancel = computed(() => writable.value && !!props.progress?.goalVersion && !ended.value)
const canReopen = computed(() => writable.value && props.sourceUsable && !!props.progress?.goalVersion && ended.value)
const pending = computed(() => change.pending.value[0])
const recoverInput = shallowRef(false)
function openGoal(isReopen: boolean) {
  reopen.value = isReopen
  goalOpen.value = true
}
function openPending() {
  if (pending.value?.body.action.kind === 'cancelLifecycleGoal') cancelOpen.value = true
  else openGoal(true)
}
function openRejected() {
  recoverInput.value = true
  if (change.rejected.value?.body.action.kind === 'cancelLifecycleGoal') cancelOpen.value = true
  else openGoal(true)
}
watch([goalOpen, cancelOpen], ([goal, cancel]) => {
  if (!goal && !cancel) recoverInput.value = false
})
const focusGoalStatus = () => props.active ? goalStatus.value : null
watch(
  () => props.initialStage,
  () => { chosenStage.value = undefined }
)
watch(
  [() => props.stageFocus, () => props.active, selectedStage, stageHeading],
  ([target, active, stage, heading], _previous, onCleanup) => {
    if (!active || target?.stage !== stage || !heading) return
    // TabsContent updates its visibility after the current tick.
    const frame = requestAnimationFrame(() => {
      heading.focus({ preventScroll: true })
      if (heading.ownerDocument.activeElement !== heading) return
      heading.scrollIntoView({ block: 'start', behavior: 'instant' })
      emit('focused')
    })
    onCleanup(() => cancelAnimationFrame(frame))
  },
  { flush: 'post' }
)
function select(stage: Stage) {
  chosenStage.value = stage
  emit('select', stage)
}
function correct(projectId?: string, value?: Progress) {
  overrideProject.value = projectId
  overrideValue.value = value
  overrideOpen.value = true
}
async function clearOverride(projectId?: string) {
  if (!props.progress?.goalVersion || !selected.value) return
  error.value = ''
  try {
    await submit({
      kind: 'clearProgressOverride',
      input: {
        specId: props.specId,
        goalVersion: props.progress.goalVersion,
        stage: selected.value.stage,
        projectId
      }
    })
    notice.value = '已清除人工更正，恢复自动汇总。'
    emit('saved')
  } catch (cause) {
    error.value = errorMessage(cause)
  }
}
async function reconcile() {
  if (busy.value || !writable.value || !props.sourceUsable || props.progress?.status === 'canceled') return
  error.value = ''
  notice.value = ''
  try {
    await submit({ kind: 'reconcileProgress', specId: props.specId })
    notice.value = '已根据当前证据重新核实进展。'
    emit('saved')
  } catch (cause) {
    error.value = errorMessage(cause)
  }
}
</script>
<template>
  <section class="lifecycle-overview" aria-label="自动生命周期">
    <div class="section-heading lifecycle-heading">
      <div>
        <h2>生命周期</h2>
        <p ref="goal-status" class="muted" tabindex="-1">
          {{ !progress ? '目标待核实' : !progress.goalVersion ? '未设置本轮目标' : statuses[progress.status] }}
          <template v-if="progress?.goalVersion"> · {{ validityLabels[progress.validity] }} · {{ progress.goalVersion }}</template>
        </p>
      </div>
      <div class="inline-actions">
        <Button v-if="pending" type="button" variant="outline" @click="openPending">核对待确认操作</Button>
        <Button
          v-else-if="canReopen"
          type="button"
          variant="outline"
          :disabled="busy"
          @click="openGoal(true)"
        >
          开启新一轮
        </Button>
        <template v-else-if="writable && !ended">
          <Button
            v-if="sourceUsable && progress"
            type="button" variant="outline" :disabled="busy" @click="openGoal(false)"
          >
            {{ progress.goal ? '编辑交付目标' : '录入交付目标' }}
          </Button>
          <Button v-if="canCancel" type="button" variant="outline" :disabled="busy" @click="cancelOpen = true">
            取消本轮
          </Button>
        </template>
        <Button
          v-if="writable && sourceUsable && progress?.goalVersion && progress.status !== 'canceled'"
          type="button"
          variant="ghost"
          :disabled="busy || !!pending"
          @click="reconcile"
        >
          重新核实
        </Button>
        <Button v-if="!pending && change.rejected.value" type="button" variant="ghost" :disabled="busy" @click="openRejected">
          查看未保存输入
        </Button>
      </div>
    </div>
    <p role="status" :class="{ notice: change.notice.value }">{{ change.notice.value }}</p>
    <div v-if="progress?.cancellation" class="notice warning">
      <strong>已取消 · {{ dateLabel(progress.cancellation.recordedAt) }}</strong>
      <p class="preserve-lines">{{ progress.cancellation.reason }}</p>
      <p v-if="progress.cancellation.source" class="muted">记录来源 · {{ progress.cancellation.source }}</p>
    </div>
    <Collapsible v-if="progress?.lastDelivery" class="goal-strip">
      <CollapsibleTrigger>
        {{ progress.lastDelivery.origin === 'automatic' ? '最近自动核实完成的目标' : '最近已验收目标' }}
        · {{ dateLabel(progress.lastDelivery.recordedAt) }}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <p>{{ progress.lastDelivery.scope }}</p>
        <code>{{ progress.lastDelivery.goalVersion }}</code>
        <p class="muted">{{ progress.lastDelivery.source }}</p>
      </CollapsibleContent>
    </Collapsible>
    <div v-if="progress?.goal" class="goal-strip">
      <p>{{ progress.goal.scope }}</p>
      <span>{{ targetLabel(progress.goal.target) }}</span>
      <Collapsible>
        <CollapsibleTrigger>目标版本与验收引用</CollapsibleTrigger>
        <CollapsibleContent>
          <code>{{ progress.goalVersion }}</code>
          <code>规格摘要 {{ progress.goal.specDigest }}</code>
          <ul>
            <li
              v-for="reference in progress.goal.acceptanceRefs"
              :key="reference"
            >
              {{ reference }}
            </li>
          </ul>
        </CollapsibleContent>
      </Collapsible>
    </div>
    <ul v-if="progress?.conflicts.length" class="notice warning">
      <li v-for="conflict in progress.conflicts" :key="conflict">
        {{ conflict }}
      </li>
    </ul>
    <p role="status" :class="{ notice }">{{ notice }}</p>
    <Alert v-if="error" role="alert" class="notice error">
      {{ error }}
    </Alert>
    <div
      v-if="progress?.goal"
      class="stage-grid"
      role="group"
      aria-label="选择生命周期阶段"
    >
      <Button
        v-for="stage in progress.stages"
        :key="stage.stage"
        type="button"
        variant="outline"
        class="stage-button"
        :aria-pressed="selected?.stage === stage.stage"
        :aria-controls="id + '-detail'"
        :data-validity="stage.validity"
        :data-progress="stage.progress"
        :data-verified="!stage.confirmationRequired || stage.confirmation?.decision === 'confirm'"
        @click="select(stage.stage)"
      >
        <span>
          {{
            stage.stage === 'delivery' &&
            !stage.applicable &&
            stage.confirmationRequired
              ? '目标结果核实'
              : stages[stage.stage]
          }}
        </span>
        <span class="stage-state">{{ stageState(stage, progress.autoConfirmableStages) }}</span>
      </Button>
    </div>
    <section
      v-if="selected && progress"
      :id="id + '-detail'"
      class="stage-detail"
      :aria-labelledby="id + '-selected'"
    >
      <div class="section-heading">
        <h3 :id="id + '-selected'" ref="stage-heading" tabindex="-1">
          {{
            selected.stage === 'delivery' &&
            !selected.applicable &&
            selected.confirmationRequired
              ? '本次目标结果核实'
              : stages[selected.stage]
          }}
          <span class="detail-state">{{ stageState(selected, progress.autoConfirmableStages) }}</span>
        </h3>
        <div class="inline-actions">
          <Button
            v-if="selected.hasOverride && canCancel"
            type="button"
            variant="ghost"
            :disabled="busy || !!pending"
            @click="clearOverride()"
          >
            清除更正
          </Button>
          <Button
            v-if="selected.applicable && canCancel"
            type="button"
            variant="outline"
            :disabled="busy || !!pending"
            @click="correct()"
          >
            人工更正
          </Button>
        </div>
      </div>
      <p class="stage-summary">
        {{
          selected.applicable
            ? selected.summary || '阶段进展待核实'
            : selected.stage === 'delivery' && selected.confirmationRequired
              ? '执行范围 · ' +
                (selected.scopeReason || '交付阶段未纳入执行范围')
              : selected.scopeReason || '未纳入当前目标'
        }}
      </p>
      <p v-if="selected.blocker" class="warning-text preserve-lines">
        {{ selected.blocker }}
      </p>
      <p v-if="selected.nextAction" class="stage-summary">下一步 · {{ selected.nextAction }}</p>
      <p v-if="selected.overrideRecord" class="override-reason">
        人工更正原因 · {{ selected.overrideRecord.reason }}
      </p>
      <ul v-if="selected.conflicts.length" class="notice warning">
        <li v-for="conflict in selected.conflicts" :key="conflict">
          {{ conflict }}
        </li>
      </ul>
      <LifecycleRunActivities
        v-if="active && selected.applicable && progress.goalVersion && progress.goal"
        :key="specId + ':' + progress.goalVersion + ':' + progress.goal.specDigest"
        :spec-id="specId" :stage="selected.stage" :goal-version="progress.goalVersion" :spec-digest="progress.goal.specDigest"
      />
      <ProgressEvidence
        :evidence="selected.evidence"
        :versions="selected.versions"
        :current-versions="progress.currentVersions"
        :participants="progress.participants"
      />
      <dl class="stage-meta">
        <div v-if="selected.applicable">
          <dt>阶段进度</dt>
          <dd>{{ progressLabels[selected.progress] }}</dd>
        </div>
        <div v-if="selected.applicable">
          <dt>证据有效性</dt>
          <dd>{{ validityLabels[selected.validity] }}</dd>
        </div>
        <div>
          <dt>进展来源</dt>
          <dd>{{ sourceLabels[selected.source] }}</dd>
        </div>
        <div>
          <dt>最近核实</dt>
          <dd>{{ dateLabel(selected.lastVerifiedAt) }}</dd>
        </div>
      </dl>
      <ProgressConfirmation
        v-if="progress.status !== 'canceled'"
        :key="selected.stage"
        :spec-id="specId"
        :progress="progress"
        :stage="selected"
        @content="emit('confirmContent')"
        @saved="emit('saved')"
      />
      <ParticipantProgressList
        :participants="selected.participants"
        :busy="busy || !!pending"
        :can-correct="canCancel"
        @correct="correct"
        @clear="clearOverride"
      />
    </section>
    <section
      v-else-if="stageFocus"
      :id="id + '-detail'"
      class="stage-detail"
      :aria-labelledby="id + '-selected'"
    >
      <h3 :id="id + '-selected'" ref="stage-heading" tabindex="-1">{{ stages[selectedStage] }}</h3>
      <p class="stage-summary">阶段进展待核实。</p>
    </section>
    <LifecycleProgressHistory
      v-if="active"
      :spec-id="specId"
      :goal-version="progress?.goalVersion ?? null"
      :round-id="progress?.roundId"
    />
    <LifecycleGoalForm
      v-model="goalOpen"
      :spec-id="specId"
      :project-id="projectId"
      :spec-digest="specDigest"
      :progress="progress"
      :confirm-leave="confirmLeave"
      :round="reopen ? { change, canReopen } : undefined"
      :recover-input="recoverInput"
      :focus-fallback="focusGoalStatus"
      @dirty="emit('dirty', $event)"
      @saved="emit('saved')"
    />
    <LifecycleGoalCancel
      v-model="cancelOpen"
      :spec-id="specId"
      :progress="progress"
      :can-cancel="canCancel"
      :change="change"
      :recover-input="recoverInput"
      :focus-fallback="focusGoalStatus"
      @saved="emit('saved')"
    />
    <ProgressOverrideForm
      v-model="overrideOpen"
      :spec-id="specId"
      :progress="progress"
      :stage="selected?.stage ?? 'research'"
      :project-id="overrideProject"
      :initial-progress="overrideValue"
      :confirm-leave="confirmLeave"
      @dirty="emit('dirty', $event)"
      @saved="emit('saved')"
    />
  </section>
</template>
<style scoped>
.lifecycle-overview {
  margin: 20px 0 28px;
  min-width: 0;
}
.lifecycle-heading {
  gap: 12px;
  flex-wrap: wrap;
}
.lifecycle-heading p {
  margin-top: 4px;
  font-size: 12px;
}
.goal-strip {
  border-top: 1px solid var(--yss-colors-border);
  padding: 14px 0;
  margin-top: 12px;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.goal-strip > span,
.goal-strip > code,
.goal-strip [data-slot='collapsible-content'] > code {
  display: block;
  margin-top: 5px;
  color: var(--brand-color-text-secondary);
}
.goal-strip [data-slot='collapsible'] {
  margin-top: 8px;
}
.goal-strip [data-slot='collapsible-trigger'] {
  cursor: pointer;
  color: var(--brand-color-text-secondary);
}
.goal-strip code {
  font-size: 11px;
  overflow-wrap: anywhere;
}
.stage-grid {
  display: grid;
  grid-template-columns: repeat(8, minmax(0, 1fr));
  gap: 7px;
  margin: 16px 0;
}
.stage-button {
  min-width: 0;
  min-height: 66px;
  height: auto;
  white-space: normal;
  display: flex;
  flex-direction: column;
  gap: 7px;
  align-items: start;
  padding: 10px;
  border: 1px solid var(--yss-colors-border);
  border-radius: 6px;
  background: #fff;
  text-align: left;
  font-size: 12px;
}
.stage-button[aria-pressed='true'] {
  border-color: var(--yss-color-primary-control);
  background: #f4f7ff;
}
.stage-state {
  color: var(--brand-color-text-secondary);
  font-size: 11px;
}
.stage-button[data-progress='done'][data-validity='current'][data-verified='true'] .stage-state {
  color: #278064;
}
.stage-button[data-progress='doing'][data-validity='current'] .stage-state {
  color: #315dcf;
}
.stage-button[data-validity='needsSync'] .stage-state,
.stage-button[data-validity='needsReview'] .stage-state {
  color: #9b5a0e;
}
.stage-detail {
  padding: 18px 20px;
  border: 1px solid var(--yss-colors-border);
  border-radius: 8px;
  background: #fff;
  min-width: 0;
}
.stage-detail .section-heading {
  flex-wrap: wrap;
  gap: 10px;
}
h3 {
  font-size: 14px;
  font-weight: 600;
}
.detail-state {
  color: var(--brand-color-text-secondary);
  font-weight: 400;
  font-size: 11px;
  margin-left: 10px;
}
.stage-summary {
  font-size: 13px;
  line-height: 1.8;
  margin-top: 12px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.stage-meta {
  margin: 14px 0 0;
}
.stage-meta > div {
  display: grid;
  grid-template-columns: 76px minmax(0, 1fr);
  gap: 12px;
  margin-top: 10px;
}
dt {
  font-size: 12px;
  color: var(--brand-color-text-secondary);
}
dd {
  margin: 0;
  color: #62666c;
  font-size: 12px;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.override-reason,
.confirmation-record {
  margin-top: 12px;
  font-size: 12px;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.override-reason {
  color: #9b5a0e;
}
@media (width < 1000px) {
  .stage-grid {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
}
@media (width < 600px) {
  .stage-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .stage-detail {
    padding: 14px;
  }
  .stage-meta > div {
    grid-template-columns: minmax(0, 1fr);
    gap: 4px;
  }
}
</style>
