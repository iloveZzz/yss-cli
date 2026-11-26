<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Textarea } from '@/components/ui/textarea'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import type { PiQueue, PiQueueDraft, PiQueueEntry } from '@/services/workbench-agent'

const props = defineProps<{
  queue?: PiQueue
  busy: boolean
  error: string
  uncertain: boolean
  active: boolean
  verified: boolean
  goalVersion?: string
  refresh: () => Promise<void>
  put: (draft: PiQueueDraft, itemId?: string) => Promise<boolean>
  remove: (id: string, version: number) => Promise<boolean>
  resume: (version: number, confirmDirectEdit?: boolean) => Promise<boolean>
}>()
const pauseLabels: Record<string, string> = {
  restarted: '工作台已重启，请核对后继续。',
  dispatchUnknown: '上次派发结果待核对。',
  receiptVerified: '上次派发已核实，请确认是否继续。',
  targetChanged: '交付目标已变化，请核对待发任务。',
  verificationRequired: '规格来源尚待核对。',
  previousRoundStopped: '上一轮未成功完成，请核对后继续。',
  reviewRequired: '请先处理上一轮待审阅成果。',
  directEditConfirmation: '下一条会直接编辑原工程，继续前需要确认。',
  admissionRejected: '上一条未受理，请检查任务和运行条件。',
  sessionUnavailable: '会话当前无法续接。'
}
const editing = shallowRef<{ id: string; version: number; draft: Omit<PiQueueDraft, 'version'> }>()
const removing = shallowRef<{ id: string; version: number }>()
const approval = shallowRef<{ id: string; version: number }>()
const editTarget = computed(() => props.queue?.items.find((item) => item.id === editing.value?.id))
const editStale = computed(() => !!editing.value && (editing.value.version !== props.queue?.version ||
  editing.value.draft.expectedGoalVersion !== props.goalVersion))
const allowWrites = computed(() => !props.busy && !props.uncertain && props.verified)
const head = computed(() => props.queue?.items[0])
const requireApproval = computed(() => !!head.value && !head.value.dispatching && !head.value.readOnly)
const pauseLabel = computed(() => {
  const reason = props.queue?.pausedReason
  return reason ? pauseLabels[reason] ?? '队列已暂停，请核对后继续。' : ''
})

function edit(item: PiQueueEntry) {
  if (!props.queue || item.dispatching) return
  editing.value = {
    id: item.id, version: props.queue.version,
    draft: { prompt: item.prompt, citations: item.citations, readOnly: item.readOnly, expectedGoalVersion: props.goalVersion }
  }
}
function patchDraft(patch: Partial<Omit<PiQueueDraft, 'version'>>) {
  if (editing.value) editing.value = { ...editing.value, draft: { ...editing.value.draft, ...patch } }
}
function rebaseEdit() {
  if (editing.value && props.queue && editTarget.value && !editTarget.value.dispatching) {
    editing.value = {
      ...editing.value, version: props.queue.version,
      draft: { ...editing.value.draft, expectedGoalVersion: props.goalVersion }
    }
  }
}
async function save() {
  const current = editing.value
  if (!current || !allowWrites.value || editStale.value || !editTarget.value || editTarget.value.dispatching) return
  if (await props.put({ ...current.draft, version: current.version }, current.id) && editing.value === current)
    editing.value = undefined
}
async function confirmRemoval() {
  const current = removing.value
  if (!current || !allowWrites.value) return
  if (await props.remove(current.id, current.version) && removing.value === current) removing.value = undefined
}
async function continueQueue() {
  const current = props.queue
  if (!current || !head.value || !allowWrites.value || props.active) return
  if (requireApproval.value) approval.value = { id: head.value.id, version: current.version }
  else await props.resume(current.version)
}
async function approve() {
  const item = approval.value
  if (!item || item.id !== head.value?.id || item.version !== props.queue?.version || !allowWrites.value || props.active) return
  await props.resume(item.version, true)
  if (approval.value === item) approval.value = undefined
}
watch(() => props.queue?.runId, () => {
  editing.value = undefined
  removing.value = undefined
  approval.value = undefined
})
</script>

<template>
  <section aria-label="续办队列" class="pi-queue-panel">
    <h2>续办队列<template v-if="queue?.items.length"> · {{ queue.items.length }}</template></h2>
    <p class="muted">沿用本会话的工程和模型，关闭离线命令。</p>
    <p v-if="pauseLabel" class="pi-issue" role="status">{{ pauseLabel }}</p>
    <p v-if="error" class="pi-issue" role="alert">{{ error }}</p>
    <Button v-if="error || uncertain" type="button" variant="outline" size="sm" :disabled="busy" @click="refresh">读取最新队列</Button>
    <p v-if="!queue && busy" role="status">正在读取队列…</p>
    <p v-else-if="queue && !queue.items.length">暂无待发任务。</p>
    <ol v-else-if="queue" class="pi-queue-items">
      <li v-for="item in queue.items" :key="item.id">
        <p class="pi-queue-prompt" :title="item.prompt">{{ item.prompt }}</p>
        <small>{{ item.dispatching ? '正在核对派发' : item.readOnly ? '只读原工程' : '编辑原工程 · 需确认' }}</small>
        <div class="pi-queue-actions">
          <Button type="button" variant="ghost" size="sm" :disabled="!allowWrites || item.dispatching" @click="edit(item)">编辑</Button>
          <Button type="button" variant="ghost" size="sm" :disabled="!allowWrites || item.dispatching"
            @click="removing = { id: item.id, version: queue.version }">移除</Button>
        </div>
      </li>
    </ol>
    <Button v-if="queue?.items.length && (queue.pausedReason || !active)" type="button" variant="outline" size="sm"
      :disabled="!allowWrites || active" @click="continueQueue">
      {{ head?.dispatching ? '核对派发' : requireApproval ? '确认编辑并继续' : '继续队列' }}
    </Button>
  </section>

  <Dialog :open="!!editing" @update:open="!$event && !busy && (editing = undefined)">
    <DialogContent class="workbench-dialog">
      <DialogHeader><DialogTitle>编辑待发任务</DialogTitle><DialogDescription>调整下一轮任务后保存。</DialogDescription></DialogHeader>
      <form v-if="editing" class="form-stack" @submit.prevent="save">
        <label>任务要求<Textarea :model-value="editing.draft.prompt" :disabled="busy" required rows="5"
          @update:model-value="patchDraft({ prompt: String($event) })" /></label>
        <label class="pi-queue-permission"><Checkbox :model-value="editing.draft.readOnly" :disabled="busy"
          @update:model-value="patchDraft({ readOnly: $event === true })" />只读原工程</label>
        <ul v-if="editing.draft.citations?.length">
          <li v-for="(cite, index) in editing.draft.citations" :key="index">
            {{ cite.path }} · {{ cite.startLine }}–{{ cite.endLine }} 行
            <Button type="button" variant="ghost" size="sm" :disabled="busy" @click="patchDraft({
              citations: editing.draft.citations?.filter((_, i) => index !== i)
            })">移除引用</Button>
          </li>
        </ul>
        <p v-if="error" class="pi-issue" role="alert">{{ error }}</p>
        <p v-if="!editTarget || editTarget.dispatching" class="pi-issue">原任务已变更，当前输入仍保留。</p>
        <Button v-else-if="editStale" type="button" variant="outline" size="sm" :disabled="!allowWrites"
          @click="rebaseEdit">采用最新版本，保留输入</Button>
        <DialogFooter><Button type="button" variant="outline" :disabled="busy" @click="editing = undefined">取消</Button>
          <Button type="submit" :disabled="!allowWrites || editStale || !editTarget || editTarget.dispatching || !editing.draft.prompt.trim()">
            <LoadingIndicator :active="busy" />保存任务
          </Button></DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
  <Dialog :open="!!removing" @update:open="!$event && !busy && (removing = undefined)">
    <DialogContent class="workbench-dialog">
      <DialogHeader><DialogTitle>移除待发任务？</DialogTitle><DialogDescription>这条任务会从续办队列移除。</DialogDescription></DialogHeader>
      <p v-if="error" class="pi-issue" role="alert">{{ error }}</p>
      <DialogFooter><Button type="button" variant="outline" :disabled="busy" @click="removing = undefined">取消</Button>
        <Button type="button" variant="destructive" :disabled="!allowWrites || removing?.version !== queue?.version"
          @click="confirmRemoval">移除任务</Button></DialogFooter>
    </DialogContent>
  </Dialog>
  <Dialog :open="!!approval" @update:open="!$event && !busy && (approval = undefined)">
    <DialogContent class="workbench-dialog">
      <DialogHeader><DialogTitle>允许下一条编辑原工程？</DialogTitle>
        <DialogDescription>本轮会直接写入原工程，执行失败或停止后，已写入的内容会保留。</DialogDescription></DialogHeader>
      <p v-if="head" class="pi-queue-prompt">{{ head.prompt }}</p>
      <DialogFooter><Button type="button" variant="outline" :disabled="busy" @click="approval = undefined">取消</Button>
        <Button type="button" :disabled="!allowWrites || active || approval?.version !== queue?.version"
          @click="approve">确认编辑并继续</Button></DialogFooter>
    </DialogContent>
  </Dialog>
</template>

<style scoped>
.pi-queue-items { display: grid; gap: 8px; padding: 0; margin: 3px 0; list-style: none; }
.pi-queue-items li { min-width: 0; border-bottom: 1px solid var(--border); overflow-wrap: anywhere; }
.pi-queue-prompt { white-space: pre-wrap; overflow-wrap: anywhere; overflow: hidden; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 4; }
.pi-queue-actions, .pi-queue-permission { display: flex; align-items: center; gap: 6px; }
.pi-queue-panel small { color: var(--quiet); font-size: 11px; }
</style>
