<script setup lang="ts">
import { computed, shallowRef, useId } from 'vue'
import { Button } from '@/components/ui/button'
import PiReadiness from '@/components/PiReadiness.vue'
import { stages, type Stage } from '@/services/workbench'
import type { AgentProject, AgentProjects, ProjectAccessMode } from '@/services/workbench-agent'
import type { PiCiAttachment, PiCiRecovery } from '../usePiDrafts'
import PiProjectScope from './PiProjectScope.vue'
import PiCiJobSettings from './PiCiJobSettings.vue'
import { piCapabilityLabels, piCommandCapability, piCommandExecutionLabels,
  type PiCommandExecution, type PiModel, type PiRuntimeStatus } from '@/services/workbench-platform'

const props = defineProps<{
  specId: string
  mainId: string
  projectScope?: AgentProjects
  projectBusy: boolean
  projectError: string
  projectGrantIssue: string
  hostSelectable: boolean
  commandUnavailable: boolean
  ciIssue: string
  ciRecovery?: PiCiRecovery
  projects: AgentProject[]
  busy: boolean
  reviewResumeId?: string
  nativeFork?: boolean
  participatingIds?: string[]
  availableStages: Stage[]
  automaticStage?: Stage
  stageNeedsReview: boolean
}>()
const emit = defineEmits<{ refreshProjects: []; clearStaleProjectGrants: [] }>()
const readyModel = defineModel<PiModel>('readyModel')
const runtimeStatus = defineModel<PiRuntimeStatus>('runtimeStatus')
const selectedModel = defineModel<PiModel>('selectedModel')
const blockedReason = defineModel<string>('blockedReason')
const modelRestrictions = defineModel<Record<string, string>>('modelRestrictions', { required: true })
const selected = defineModel<string[]>('selected', { required: true })
const projectModes = defineModel<Record<string, ProjectAccessMode>>('projectModes', { required: true })
const ciAttachment = defineModel<PiCiAttachment | undefined>('ciAttachment')
const ciOpen = shallowRef(!!ciAttachment.value || !!props.ciRecovery)
const ciId = useId()
const commandExecution = defineModel<PiCommandExecution>('commandExecution', { required: true })
const commandId = useId()
const commandModes = ['sandbox', 'host'] as const
const commandCapabilities = computed(() => {
  const runtime = runtimeStatus.value
  return runtime ? commandModes.map((mode) => ({ mode, ...piCommandCapability(runtime, mode) })) : []
})
const hostSupported = computed(() => commandCapabilities.value.some(({ mode, state }) => mode === 'host' && state !== 'unsupported'))
const stage = defineModel<Stage | ''>('stage', { required: true })
const stageId = useId()
const stageValue = computed({
  get: () => props.stageNeedsReview ? 'stale' : stage.value,
  set: (value: string) => {
    if (value === '' || props.availableStages.includes(value as Stage)) stage.value = value as Stage | ''
  }
})
const participatingIds = computed(() => props.reviewResumeId || props.nativeFork ? props.participatingIds ?? [] : selected.value)
const ciProjects = computed(() => (props.projectScope?.projects ?? []).filter((project) =>
  project.status === 'ready' && project.head && (project.id === props.mainId || participatingIds.value.includes(project.id))))
</script>

<template>
  <div class="pi-settings">
    <PiReadiness v-model:ready-model="readyModel" v-model:selected-model="selectedModel"
      v-model:runtime-status="runtimeStatus"
      v-model:blocked-reason="blockedReason" v-model:model-restrictions="modelRestrictions"
      :resume-run-id="reviewResumeId" :busy="busy" :allow-model-switch="!nativeFork" />
    <p v-if="nativeFork" class="muted">分叉首轮使用原工程范围和模型，只读当前工程。</p>
    <fieldset v-else-if="availableStages.length || stageNeedsReview" :disabled="busy" class="pi-settings-fieldset">
      <legend>本轮进度</legend>
      <label :for="stageId">跟进阶段</label>
      <select :id="stageId" v-model="stageValue" name="lifecycleStage" class="pi-settings-stage"
        :aria-invalid="stageNeedsReview || undefined" :aria-describedby="stageNeedsReview ? stageId + '-review' : undefined">
        <option v-if="stageNeedsReview" value="stale" disabled>原选择待核对</option>
        <option value="">{{ automaticStage ? '自动 · ' + stages[automaticStage] : '自动 · 暂无可跟进阶段' }}</option>
        <option v-for="option in availableStages" :key="option" :value="option">{{ stages[option] }}</option>
      </select>
      <p v-if="stageNeedsReview" :id="stageId + '-review'" role="status">目标版本或适用阶段已变化，请重新选择。</p>
    </fieldset>
    <p v-else class="muted">本轮暂无可跟进的生命周期阶段。</p>
    <div v-if="runtimeStatus && !nativeFork" class="pi-settings-fieldset">
      <label :for="commandId" class="font-semibold">命令运行方式</label>
      <select :id="commandId" v-model="commandExecution" name="commandExecution" class="pi-settings-stage" :disabled="busy"
        :aria-describedby="commandId + '-help ' + commandId + '-status'">
        <option value="sandbox">沙箱离线执行</option>
        <option v-if="hostSupported || commandExecution === 'host'" value="host" :disabled="!hostSelectable">本机直接执行（仅本轮）</option>
      </select>
      <p :id="commandId + '-help'">
        <template v-if="hostSupported">本机直接执行以当前用户权限运行，可访问工程之外的本机文件和网络。选择仅对本轮已核对的工程范围有效。</template>
        <template v-else>命令在工程的隔离环境中离线执行。</template>
      </p>
    </div>
    <dl v-if="commandCapabilities.length" :id="commandId + '-status'" class="pi-settings-fieldset">
      <div v-for="capability in commandCapabilities" :key="capability.mode">
        <dt class="inline text-xs font-medium">{{ piCommandExecutionLabels[capability.mode] }}</dt>
        <dd class="ml-2 inline text-xs text-muted-foreground">
          {{ piCapabilityLabels[capability.state] }}<template v-if="capability.reason"> · {{ capability.reason }}</template>
        </dd>
      </div>
    </dl>
    <PiProjectScope v-model:selected="selected" v-model:modes="projectModes"
      :main-id="mainId" :projects="projects" :scope="projectScope" :loading="projectBusy" :error="projectError"
      :locked="!!reviewResumeId || nativeFork" :participating-ids="participatingIds"
      :command-execution="commandExecution" :commands-unavailable="commandUnavailable"
      :busy="busy" :read-only="nativeFork" :grant-issue="projectGrantIssue" @refresh="emit('refreshProjects')"
      @clear-stale-project-grants="emit('clearStaleProjectGrants')" />
    <div v-if="!reviewResumeId && !nativeFork && projectScope" class="pi-ci-settings">
      <Button type="button" variant="outline" size="sm" :aria-expanded="ciOpen" :aria-controls="ciId" @click="ciOpen = !ciOpen">
        {{ ciOpen ? '收起 GitLab CI 作业' : ciAttachment ? '查看已附加的 CI 作业' : '选择 GitLab CI 作业' }}
      </Button>
      <div :id="ciId">
        <p v-if="ciIssue" class="pi-settings-issue" role="alert">{{ ciIssue }}</p>
        <PiCiJobSettings v-if="ciOpen" v-model="ciAttachment" :spec-id="specId" :projects="ciProjects" :registry="projectScope.projects"
          :recovery="ciRecovery" :busy="busy || projectBusy" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.pi-settings { display: grid; align-content: start; gap: 18px; min-width: 0; padding: 0 20px 24px; }
.pi-settings-fieldset { display: grid; gap: 9px; min-width: 0; }
.pi-settings-fieldset legend { margin-bottom: 7px; font-weight: 600; }
.pi-settings-fieldset p, .pi-settings-issue { color: var(--brand-color-text-secondary); font-size: 12px; overflow-wrap: anywhere; }
.pi-ci-settings, .pi-ci-settings > div { display: grid; justify-items: start; gap: 10px; min-width: 0; }
.pi-ci-settings > div { justify-items: stretch; width: 100%; }
.pi-settings-stage { min-height: 48px; padding-inline: 10px; border: 1px solid var(--input); border-radius: var(--radius-md); background: var(--background); color: var(--foreground); font: inherit; }
</style>
