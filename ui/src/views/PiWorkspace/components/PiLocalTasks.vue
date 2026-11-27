<script setup lang="ts">
import { computed, shallowRef, useId, watch } from 'vue'
import { Terminal } from '@lucide/vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import type { AgentProjectCandidate } from '@/services/workbench-agent'
import type { LocalProjectTaskAction } from '@/services/workbench-platform'
import { usePiLocalTasks } from '../usePiLocalTasks'
import { projectCheckout } from '../usePiProjects'
import PiLocalTaskResult from './PiLocalTaskResult.vue'

const props = defineProps<{
  runId: string
  jobId: string
  mainId: string
  projectIds: string[]
  projects: AgentProjectCandidate[]
  projectNames: Record<string, string>
  projectBusy: boolean
  projectError: string
  unavailable: string
}>()
const emit = defineEmits<{ refreshProjects: []; recheckRound: [] }>()
const id = useId()
const open = shallowRef(false)
const chosenProject = shallowRef('')
const directory = shallowRef('')
const projects = computed(() => props.projects.filter((item) => item.status === 'ready' && props.projectIds.includes(item.id)))
const project = computed(() => projects.value.find((item) => item.id === chosenProject.value) ??
  projects.value.find((item) => item.id === props.mainId))
const checkout = computed(() => projectCheckout(project.value))
const { jobs, loaded, reading, readError, sending, launchError, running, pending, pendingInput, refresh, launch } =
  usePiLocalTasks(props.runId, open, recheckTarget)
const unavailable = computed(() => props.unavailable || (running.value ? '此会话的本机任务正在运行。' : ''))
const canRun = computed(() => !unavailable.value && !pending.value && !sending.value && !props.projectBusy &&
  !props.projectError && !reading.value && !readError.value && !!checkout.value && !!props.jobId && loaded.value)
const canRetry = computed(() => !running.value && !sending.value && !reading.value && loaded.value && !readError.value &&
  pending.value?.status === 'uncertain' && !!pendingInput.value && (!!pendingInput.value.expectedCheckout || !unavailable.value))

watch(open, (show) => { if (show) recheckTarget() })
watch(() => project.value?.id, () => { directory.value = '' }, { flush: 'sync' })

function recheckTarget() {
  emit('refreshProjects')
  emit('recheckRound')
}
function chooseProject(event: Event) {
  chosenProject.value = (event.target as HTMLSelectElement).value
}
function start(action: LocalProjectTaskAction) {
  if (!canRun.value || !project.value || !checkout.value) return
  const { rootPath, head, branch } = checkout.value
  void launch({
    projectId: project.value.id, directory: directory.value, action,
    expectedJobId: props.jobId, expectedCheckout: { rootPath, head, branch }
  })
}
function retry() {
  if (canRetry.value && pendingInput.value) void launch(pendingInput.value)
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetTrigger as-child>
      <Button type="button" variant="outline" class="pi-diff-shortcut pi-local-task-shortcut">
        <Terminal :size="15" aria-hidden="true" />本机测试与构建
      </Button>
    </SheetTrigger>
    <SheetContent class="pi-sheet">
      <SheetHeader class="pi-sheet-header">
        <SheetTitle>本机测试与构建</SheetTitle>
        <SheetDescription>工程脚本以当前本机用户权限在所选工程的现有检出运行，可写入本机文件。</SheetDescription>
      </SheetHeader>
      <div class="pi-sheet-body grid content-start gap-5 px-5 pt-2 pb-5 text-sm">
        <div class="grid min-w-0 gap-3">
          <div class="grid gap-1.5">
            <label :for="id + '-project'" class="font-medium">工程</label>
            <select :id="id + '-project'" :value="project?.id || ''" :disabled="projectBusy || !projects.length || sending || !!pending"
              class="h-9 w-full min-w-0 rounded-md border border-input bg-background px-3 text-base shadow-xs outline-none focus-visible:ring-2 focus-visible:ring-ring/50 disabled:opacity-50 md:text-sm"
              @change="chooseProject">
              <option value="" disabled>{{ projectBusy ? '正在核对工程…' : '选择本会话的工程' }}</option>
              <option v-for="item in projects" :key="item.id" :value="item.id">
                {{ item.name }}{{ item.id === mainId ? '（主工程）' : '' }}
              </option>
            </select>
            <div v-if="project" class="grid gap-1 text-xs text-muted-foreground [overflow-wrap:anywhere]">
              <p>原工程路径 <code>{{ project.rootPath }}</code></p>
              <p v-if="checkout">{{ checkout.branch ?? '游离 HEAD' }} · <code :title="checkout.head">{{ checkout.head.slice(0, 12) }}</code></p>
              <p v-else>无法确认工程检出，请重新核对工程。</p>
            </div>
            <p v-else-if="!projectBusy && !projectError && !projects.length" class="text-muted-foreground">此会话没有可用的已登记工程。</p>
            <p v-if="projectError" class="pi-issue" role="alert">
              {{ projectError }} <Button type="button" variant="ghost" size="sm" @click="emit('refreshProjects')">重新核对工程</Button>
            </p>
          </div>
          <div class="grid gap-1.5">
            <label :for="id + '-directory'" class="font-medium">相对子目录（可选）</label>
            <Input :id="id + '-directory'" v-model="directory" autocomplete="off" autocapitalize="none" spellcheck="false"
              :aria-describedby="id + '-directory-help'" :disabled="!project || sending || !!pending" />
            <p :id="id + '-directory-help'" class="text-xs text-muted-foreground">留空使用工程根目录，例如可填写 <code>prototypes/contacts</code>。</p>
          </div>
          <p v-if="unavailable" role="status" class="text-muted-foreground">{{ unavailable }}</p>
          <p v-if="sending || pending?.status === 'sending'" role="status" class="flex items-center gap-1.5">
            <LoadingIndicator :active="true" />正在提交本机任务…
          </p>
          <p v-if="launchError" role="alert" class="pi-issue">{{ launchError }}</p>
          <div v-if="pending?.status === 'uncertain'" class="grid gap-2 [overflow-wrap:anywhere]">
            <p role="status">
              <template v-if="pendingInput">
                {{ pendingInput.action === 'test' ? '本机测试' : '本机构建' }}待核对 ·
                {{ projectNames[pendingInput.projectId] ?? pendingInput.projectId }} · {{ pendingInput.directory || '工程根目录' }}
              </template>
              <template v-else>原任务待核对。</template>
            </p>
            <p v-if="pendingInput?.expectedCheckout" class="text-xs text-muted-foreground">
              <code>{{ pendingInput.expectedCheckout.rootPath }}</code> ·
              {{ pendingInput.expectedCheckout.branch ?? '游离 HEAD' }} ·
              <code :title="pendingInput.expectedCheckout.head">{{ pendingInput.expectedCheckout.head.slice(0, 12) }}</code>
            </p>
            <div class="flex flex-wrap gap-2">
              <Button type="button" variant="outline" size="sm" :disabled="reading || sending" @click="refresh">核对任务记录</Button>
              <Button v-if="pendingInput" type="button" size="sm" :disabled="!canRetry" @click="retry">
                按同一请求重试
              </Button>
            </div>
          </div>
          <div v-else class="flex flex-wrap gap-2">
            <Button type="button" class="max-sm:min-h-11" :disabled="!canRun" @click="start('test')">本机测试</Button>
            <Button type="button" class="max-sm:min-h-11" variant="outline" :disabled="!canRun" @click="start('build')">本机构建</Button>
          </div>
        </div>
        <section :aria-labelledby="id + '-history'" class="grid min-w-0 gap-2">
          <h3 :id="id + '-history'" class="font-semibold">任务记录</h3>
          <p v-if="readError" class="pi-issue" role="alert">
            {{ readError }} <Button type="button" variant="ghost" size="sm" :disabled="reading" @click="refresh">重读任务记录</Button>
          </p>
          <LoadingSkeleton v-if="!loaded && !readError" variant="text" :rows="2" label="正在读取本机任务记录" />
          <p v-else-if="loaded && !jobs.length" class="text-muted-foreground">此会话暂无本机任务记录。</p>
          <ul v-if="jobs.length">
            <PiLocalTaskResult v-for="job in jobs" :key="job.id" :job="job"
              :project-name="projectNames[job.localProjectTask.projectId] ?? job.localProjectTask.projectId" @changed="refresh" />
          </ul>
        </section>
      </div>
    </SheetContent>
  </Sheet>
</template>

<style scoped>
@media (width < 600px) {
  .pi-diff-shortcut.pi-local-task-shortcut { min-height: 44px; }
}
</style>
