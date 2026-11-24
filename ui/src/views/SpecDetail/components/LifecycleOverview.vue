<script setup lang="ts">
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Alert } from '@/components/ui/alert'
import { computed, shallowRef, watch, useId } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { Button } from '@/components/ui/button'
import ProgressOverrideForm from '@/components/ProgressOverrideForm.vue'
import {
  dateLabel,
  errorMessage,
  sourceLabels,
  stageState,
  stages,
  type Progress,
  type ProgressSummary,
  type Stage
} from '@/services/workbench'
import LifecycleGoalForm from './LifecycleGoalForm.vue'
import ProgressEvidence from './ProgressEvidence.vue'
import ParticipantProgressList from './ParticipantProgressList.vue'
import ProgressConfirmation from './ProgressConfirmation.vue'
import LifecycleProgressHistory from './LifecycleProgressHistory.vue'
const props = defineProps<{
  specId: string
  projectId: string
  specDigest: string | null
  progress?: ProgressSummary
  initialStage?: Stage
  confirmLeave: (dirty: boolean) => boolean | Promise<boolean>
}>()
const emit = defineEmits<{
  dirty: [value: boolean]
  saved: []
  select: [stage: Stage]
  confirmContent: []
}>()
const { submit, saving } = useWorkbench()
const id = useId()
const selectedStage = shallowRef<Stage>(
  props.initialStage ?? props.progress?.primaryStage ?? 'research'
)
const selected = computed(
  () =>
    props.progress?.stages.find(
      (stage) => stage.stage === selectedStage.value
    ) ?? props.progress?.stages[0]
)
const goalOpen = shallowRef(false)
const overrideOpen = shallowRef(false)
const overrideProject = shallowRef<string>()
const overrideValue = shallowRef<Progress>()
const error = shallowRef('')
const notice = shallowRef('')
watch(
  () => props.initialStage,
  (stage) => {
    if (stage && stage !== 'unmarked') selectedStage.value = stage
  }
)
function select(stage: Stage) {
  selectedStage.value = stage
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
  error.value = ''
  try {
    await submit({ kind: 'reconcileProgress', specId: props.specId })
    notice.value = '已重新核实当前凭证。'
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
        <p class="muted">阶段可并行，点击查看依据。</p>
      </div>
      <div class="inline-actions">
        <Button
          type="button"
          variant="outline"
          :disabled="saving"
          @click="goalOpen = true"
        >
          {{ progress?.status === 'done' ? '登记新一轮目标' : progress?.goal ? '编辑交付目标' : '录入交付目标' }}
        </Button>
        <Button
          type="button"
          variant="ghost"
          :disabled="saving"
          @click="reconcile"
        >
          重新核实
        </Button>
      </div>
    </div>
    <Collapsible v-if="progress?.lastDelivery" class="goal-strip">
      <CollapsibleTrigger>最近已验收目标 · {{ dateLabel(progress.lastDelivery.recordedAt) }}</CollapsibleTrigger>
      <CollapsibleContent>
        <p>{{ progress.lastDelivery.scope }}</p>
        <code>{{ progress.lastDelivery.goalVersion }}</code>
        <p class="muted">{{ progress.lastDelivery.source }}</p>
      </CollapsibleContent>
    </Collapsible>
    <div v-if="progress?.goal" class="goal-strip">
      <p>{{ progress.goal.scope }}</p>
      <span>
        {{
          { local: '本地产物', merged: '合并代码', environment: '指定环境' }[
            progress.goal.target.kind
          ]
        }}
        · {{ progress.goal.target.label }}
      </span>
      <code>{{ progress.goal.target.reference }}</code>
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
    <p v-else class="notice warning">
      本次交付目标待核实。录入范围、交付位置、适用阶段与验收引用后，可按同一目标跟进。
    </p>
    <ul v-if="progress?.conflicts.length" class="notice warning">
      <li v-for="conflict in progress.conflicts" :key="conflict">
        {{ conflict }}
      </li>
    </ul>
    <p v-if="notice" role="status" class="notice">{{ notice }}</p>
    <Alert v-if="error" role="alert" class="notice error">
      {{ error }}
    </Alert>
    <div
      v-if="progress"
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
        @click="select(stage.stage)"
      >
        <span>
          {{
            stage.stage === 'delivery' &&
            !stage.applicable &&
            stage.confirmationRequired
              ? '目标结果确认'
              : stages[stage.stage]
          }}
        </span>
        <span class="stage-state">{{ stageState(stage) }}</span>
      </Button>
    </div>
    <section
      v-if="selected && progress"
      :id="id + '-detail'"
      class="stage-detail"
      :aria-labelledby="id + '-selected'"
    >
      <div class="section-heading">
        <h3 :id="id + '-selected'">
          {{
            selected.stage === 'delivery' &&
            !selected.applicable &&
            selected.confirmationRequired
              ? '本次目标结果确认'
              : stages[selected.stage]
          }}
          <span class="detail-state">{{ stageState(selected) }}</span>
        </h3>
        <div class="inline-actions">
          <Button
            v-if="selected.hasOverride"
            type="button"
            variant="ghost"
            :disabled="saving"
            @click="clearOverride()"
          >
            清除更正
          </Button>
          <Button
            v-if="selected.applicable"
            type="button"
            variant="outline"
            :disabled="saving || !progress.goalVersion"
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
              ? '本次目标结果确认。执行范围说明 · ' +
                (selected.scopeReason || '交付阶段未纳入执行范围')
              : selected.scopeReason || '未纳入当前目标'
        }}
      </p>
      <p v-if="selected.blocker" class="warning-text preserve-lines">
        {{ selected.blocker }}
      </p>
      <p v-if="selected.overrideRecord" class="override-reason">
        人工更正原因 · {{ selected.overrideRecord.reason }}
      </p>
      <ul v-if="selected.conflicts.length" class="notice warning">
        <li v-for="conflict in selected.conflicts" :key="conflict">
          {{ conflict }}
        </li>
      </ul>
      <ProgressEvidence
        :evidence="selected.evidence"
        :versions="selected.versions"
        :current-versions="progress.currentVersions"
        :participants="progress.participants"
      />
      <dl class="stage-meta">
        <div>
          <dt>确认方式</dt>
          <dd>
            {{ sourceLabels[selected.source] }}
            <span v-if="selected.confirmationRequired">
              ·
              {{
                selected.stage === 'spec'
                  ? '通过待确认事项完成内容确认'
                  : '由用户明确确认'
              }}
            </span>
          </dd>
        </div>
        <div>
          <dt>最近核实</dt>
          <dd>{{ dateLabel(selected.lastVerifiedAt) }}</dd>
        </div>
      </dl>
      <ProgressConfirmation
        :key="selected.stage"
        :spec-id="specId"
        :progress="progress"
        :stage="selected"
        @content="emit('confirmContent')"
        @saved="emit('saved')"
      />
      <ParticipantProgressList
        :participants="selected.participants"
        :busy="saving"
        :can-correct="!!progress.goalVersion"
        @correct="correct"
        @clear="clearOverride"
      />
    </section>
    <LifecycleProgressHistory
      :spec-id="specId"
      :goal-version="progress?.goalVersion ?? null"
    />
    <LifecycleGoalForm
      v-model="goalOpen"
      :spec-id="specId"
      :project-id="projectId"
      :spec-digest="specDigest"
      :progress="progress"
      :confirm-leave="confirmLeave"
      @dirty="emit('dirty', $event)"
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
.stage-button[data-progress='done'][data-validity='current'] .stage-state {
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
