<script setup lang="ts">
import { useId } from 'vue'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import AgentCommandPermission from '@/components/AgentCommandPermission.vue'

defineProps<{ disabled?: boolean; commandsUnavailable?: boolean; commandsMessage?: string | null }>()
const readOnly = defineModel<boolean>('readOnly', { required: true })
const allowCommands = defineModel<boolean>('allowCommands', { required: true })
const descriptionId = useId()

function setMode(value: unknown) {
  if (value !== 'read' && value !== 'edit') return
  readOnly.value = value === 'read'
  if (readOnly.value) allowCommands.value = false
}
</script>

<template>
  <fieldset :disabled="disabled" class="agent-run-permissions">
    <legend>本轮权限</legend>
    <ToggleGroup class="segmented" type="single" aria-label="本轮权限" :aria-describedby="descriptionId"
      :model-value="readOnly ? 'read' : 'edit'" :disabled="disabled" @update:model-value="setMode">
      <ToggleGroupItem value="read">只读原工程</ToggleGroupItem>
      <ToggleGroupItem value="edit">编辑原工程</ToggleGroupItem>
    </ToggleGroup>
    <p :id="descriptionId" class="muted">
      {{ readOnly ? '可读取和搜索选定工程的当前文件。' : '文件直接写入原工程。运行失败或停止后，已写入的内容会保留。' }}
    </p>
    <AgentCommandPermission v-if="!readOnly" v-model="allowCommands"
      :disabled="disabled || (commandsUnavailable && !allowCommands)" />
    <p v-if="!readOnly && commandsUnavailable" role="status">离线命令不可用：{{ commandsMessage || '命令隔离尚未通过检测。' }}<template v-if="allowCommands">请取消离线命令授权后运行。</template></p>
  </fieldset>
</template>

<style scoped>
.agent-run-permissions { display: grid; gap: 9px; min-width: 0; }
.agent-run-permissions legend { margin-bottom: 7px; font-weight: 600; }
.agent-run-permissions p { font-size: 12px; overflow-wrap: anywhere; }
</style>
