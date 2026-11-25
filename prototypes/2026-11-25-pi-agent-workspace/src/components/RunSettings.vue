<script setup lang="ts">
import { FolderGit2, GitBranch, ShieldCheck } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Textarea } from '@/components/ui/textarea'
import { projects, type Configuration } from '../demo-agent'
import { useDialogFocus } from '../useDialogFocus'

const open = defineModel<boolean>('open', { required: true })
const returnFromSettings = useDialogFocus(open)
const props = defineProps<{ config: Configuration, commands: boolean, existing: boolean, busy: boolean }>()
const emit = defineEmits<{ save: [config: Configuration, commands: boolean] }>()
const projectIds = ref<string[]>([])
const inputs = ref<Record<string, string>>({})
const allowedCommands = ref(false)
const invalidPath = computed(() => Object.entries(inputs.value)
  .filter(([id]) => projectIds.value.includes(id))
  .some(([, value]) => value.split('\n').some((path) => /^\s*(\/|~|[a-z]:|\\)/i.test(path) || path.trim().split(/[\\/]/).includes('..'))))

watch(open, (value) => {
  if (!value) return
  projectIds.value = [...props.config.projectIds]
  inputs.value = { ...props.config.inputs }
  allowedCommands.value = props.commands
})

function toggleProject(id: string, value: boolean | 'indeterminate') {
  projectIds.value = value ? [...projectIds.value, id] : projectIds.value.filter((entry) => entry !== id)
}

function save() {
  if (invalidPath.value || props.busy) return
  emit('save', { projectIds: projectIds.value, inputs: Object.fromEntries(Object.entries(inputs.value).filter(([id]) => projectIds.value.includes(id))) }, allowedCommands.value)
  open.value = false
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="run-settings-dialog" @close-auto-focus="returnFromSettings">
      <DialogHeader><DialogTitle>运行设置</DialogTitle><DialogDescription>{{ existing ? '续接沿用本会话的模型、参与工程与隔离工作区。' : '选择参与工程。Pi 在各工程的隔离 worktree 中运行。' }}</DialogDescription></DialogHeader>
      <form class="settings-form" @submit.prevent="save">
        <fieldset class="settings-projects" :disabled="busy">
          <legend>参与工程</legend>
          <div v-for="(project, index) in projects" :key="project.id" class="project-option">
            <label class="project-row" :for="`project-${project.id}`">
              <Checkbox :id="`project-${project.id}`" :model-value="projectIds.includes(project.id)" :disabled="index === 0 || existing || busy" @update:model-value="toggleProject(project.id, $event)" />
              <FolderGit2 :size="16" /><strong>{{ project.id }}</strong><Badge variant="outline">{{ project.label }}</Badge><code>{{ project.base }}</code>
            </label>
            <div v-if="projectIds.includes(project.id)" class="worktree-input">
              <label :for="`input-${project.id}`">带入未提交文件</label>
              <Textarea :id="`input-${project.id}`" v-model="inputs[project.id]" :disabled="existing" placeholder="每行一个仓库相对路径（可选）" rows="2" />
            </div>
          </div>
          <p v-if="invalidPath" class="input-error" role="status">请填写工程内相对路径。</p>
        </fieldset>
        <div class="settings-permission">
          <label class="permission-row" for="allow-command"><Checkbox id="allow-command" v-model="allowedCommands" :disabled="busy" /><ShieldCheck :size="17" /><strong>允许本轮执行离线命令</strong></label>
          <p>适用于本轮运行。文件写入限制在隔离工作区。</p>
        </div>
        <DialogFooter><Button type="button" variant="outline" @click="open = false">{{ busy ? '关闭' : '取消' }}</Button><Button v-if="!busy" type="submit" :disabled="invalidPath"><GitBranch /> 采用设置</Button></DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
