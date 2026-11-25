<script setup lang="ts">
import { computed } from 'vue'
import { Checkbox } from '@/components/ui/checkbox'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { Textarea } from '@/components/ui/textarea'
import PiReadiness from '@/components/PiReadiness.vue'
import AgentCommandPermission from '@/components/AgentCommandPermission.vue'
import type { AgentProject } from '@/services/workbench-agent'
import type { PiModel, PiRuntimeStatus } from '@/services/workbench-platform'

const props = defineProps<{
  mainId: string
  projects: AgentProject[]
  sourcePath: string
  busy: boolean
  reviewResumeId?: string
  participatingIds?: string[]
}>()
const readyModel = defineModel<PiModel>('readyModel')
const runtimeStatus = defineModel<PiRuntimeStatus>('runtimeStatus')
const selectedModel = defineModel<PiModel>('selectedModel')
const blockedReason = defineModel<string>('blockedReason')
const modelRestrictions = defineModel<Record<string, string>>('modelRestrictions', { required: true })
const selected = defineModel<string[]>('selected', { required: true })
const carry = defineModel<Record<string, string>>('carry', { required: true })
const allowCommands = defineModel<boolean>('allowCommands', { required: true })
const readOnly = defineModel<boolean>('readOnly')
const participating = computed(() =>
  props.projects.filter((project) => project.id === props.mainId ||
    (props.reviewResumeId ? props.participatingIds : selected.value)?.includes(project.id))
)
function toggle(id: string, enabled: boolean) {
  selected.value = enabled ? [...new Set([...selected.value, id])] : selected.value.filter((project) => project !== id)
}
function setMode(value: unknown) {
  if (value !== 'read' && value !== 'edit') return
  readOnly.value = value === 'read'
  if (readOnly.value) allowCommands.value = false
}
</script>

<template>
  <div class="pi-settings">
    <PiReadiness v-model:ready-model="readyModel" v-model:selected-model="selectedModel"
      v-model:runtime-status="runtimeStatus"
      v-model:blocked-reason="blockedReason" v-model:model-restrictions="modelRestrictions"
      :resume-run-id="reviewResumeId" :busy="busy" allow-model-switch />
    <fieldset :disabled="busy" class="pi-settings-fieldset">
      <legend>参与工程</legend>
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
    <fieldset v-if="!reviewResumeId" :disabled="busy" class="pi-settings-fieldset">
      <legend>带入未提交文件</legend>
      <p>各工程从 Git HEAD 建立隔离工作区。主 Spec 尚未提交时，在主工程中显式填写 <code>{{ sourcePath }}</code>。</p>
      <label v-for="project in participating" :key="project.id" class="pi-settings-files">
        {{ project.name }}
        <Textarea :model-value="carry[project.id] ?? ''" rows="2" spellcheck="false"
          placeholder="每行一个相对路径"
          @update:model-value="carry = { ...carry, [project.id]: String($event) }" />
      </label>
    </fieldset>
    <p v-else class="muted">续接沿用该会话的工作区和初始文件。所选模型用于下一轮。</p>
    <fieldset :disabled="busy" class="pi-settings-fieldset">
      <legend>工作模式</legend>
      <ToggleGroup class="segmented" type="single" aria-label="工作模式" :model-value="readOnly ? 'read' : 'edit'"
        :disabled="busy" @update:model-value="setMode">
        <ToggleGroupItem value="edit">编辑工作区</ToggleGroupItem>
        <ToggleGroupItem value="read">只读分析</ToggleGroupItem>
      </ToggleGroup>
      <p v-if="readOnly" data-workbench-reveal>仅开放读取、搜索和展示本轮计划等工具。</p>
      <AgentCommandPermission v-else v-model="allowCommands" :disabled="busy" />
    </fieldset>
  </div>
</template>

<style scoped>
.pi-settings { display: grid; align-content: start; gap: 18px; min-width: 0; padding: 0 20px 24px; }
.pi-settings-fieldset { display: grid; gap: 9px; min-width: 0; }
.pi-settings-fieldset legend { margin-bottom: 7px; font-weight: 600; }
.pi-settings-fieldset p, .pi-settings-project code { color: var(--brand-color-text-secondary); font-size: 12px; overflow-wrap: anywhere; }
.pi-settings-project { display: flex; align-items: flex-start; gap: 9px; padding-block: 4px; font-size: 13px; }
.pi-settings-project code { display: block; }
.pi-settings-files { display: grid; gap: 5px; }
</style>
