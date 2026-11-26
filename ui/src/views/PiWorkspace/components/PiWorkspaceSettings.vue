<script setup lang="ts">
import { computed, useId } from 'vue'
import { Checkbox } from '@/components/ui/checkbox'
import PiReadiness from '@/components/PiReadiness.vue'
import AgentRunPermissions from '@/components/AgentRunPermissions.vue'
import { stages, type Stage } from '@/services/workbench'
import type { AgentProject } from '@/services/workbench-agent'
import type { PiModel, PiRuntimeStatus } from '@/services/workbench-platform'

const props = defineProps<{
  mainId: string
  projects: AgentProject[]
  busy: boolean
  reviewResumeId?: string
  participatingIds?: string[]
  availableStages: Stage[]
  automaticStage?: Stage
  stageNeedsReview: boolean
}>()
const readyModel = defineModel<PiModel>('readyModel')
const runtimeStatus = defineModel<PiRuntimeStatus>('runtimeStatus')
const selectedModel = defineModel<PiModel>('selectedModel')
const blockedReason = defineModel<string>('blockedReason')
const modelRestrictions = defineModel<Record<string, string>>('modelRestrictions', { required: true })
const selected = defineModel<string[]>('selected', { required: true })
const allowCommands = defineModel<boolean>('allowCommands', { required: true })
const readOnly = defineModel<boolean>('readOnly', { required: true })
const stage = defineModel<Stage | ''>('stage', { required: true })
const stageId = useId()
const stageValue = computed({
  get: () => props.stageNeedsReview ? 'stale' : stage.value,
  set: (value: string) => {
    if (value === '' || props.availableStages.includes(value as Stage)) stage.value = value as Stage | ''
  }
})
const participating = computed(() =>
  props.projects.filter((project) => project.id === props.mainId ||
    (props.reviewResumeId ? props.participatingIds : selected.value)?.includes(project.id))
)
function toggle(id: string, enabled: boolean) {
  selected.value = enabled ? [...new Set([...selected.value, id])] : selected.value.filter((project) => project !== id)
}
</script>

<template>
  <div class="pi-settings">
    <PiReadiness v-model:ready-model="readyModel" v-model:selected-model="selectedModel"
      v-model:runtime-status="runtimeStatus"
      v-model:blocked-reason="blockedReason" v-model:model-restrictions="modelRestrictions"
      :resume-run-id="reviewResumeId" :busy="busy" allow-model-switch />
    <fieldset v-if="availableStages.length || stageNeedsReview" :disabled="busy" class="pi-settings-fieldset">
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
    <fieldset :disabled="busy" class="pi-settings-fieldset">
      <legend>参与工程</legend>
      <p>{{ reviewResumeId ? '本轮沿用会话的参与工程，读取当前文件和 Git 基线。' : '直接使用所选工程的当前文件，包括未提交文件。' }}</p>
      <div v-for="project in projects" :key="project.id">
        <label v-if="!reviewResumeId || participating.includes(project)" class="pi-settings-project">
          <Checkbox :model-value="participating.includes(project)"
            :disabled="busy || !!reviewResumeId || !project.active || project.id === mainId"
            @update:model-value="toggle(project.id, $event === true)" />
          <span>{{ project.name }} <small v-if="project.id === mainId">· 主工程</small>
            <code>{{ project.rootPath }}</code>
          </span>
        </label>
      </div>
    </fieldset>
    <AgentRunPermissions v-model:read-only="readOnly" v-model:allow-commands="allowCommands" :disabled="busy" />
  </div>
</template>

<style scoped>
.pi-settings { display: grid; align-content: start; gap: 18px; min-width: 0; padding: 0 20px 24px; }
.pi-settings-fieldset { display: grid; gap: 9px; min-width: 0; }
.pi-settings-fieldset legend { margin-bottom: 7px; font-weight: 600; }
.pi-settings-fieldset p, .pi-settings-project code { color: var(--brand-color-text-secondary); font-size: 12px; overflow-wrap: anywhere; }
.pi-settings-project { display: flex; align-items: flex-start; gap: 9px; padding-block: 4px; font-size: 13px; }
.pi-settings-project code { display: block; }
.pi-settings-stage { min-height: 48px; padding-inline: 10px; border: 1px solid var(--input); border-radius: var(--radius-md); background: var(--background); color: var(--foreground); font: inherit; }
</style>
