<script setup lang="ts">
import { computed, shallowRef, useId, watch } from 'vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Textarea } from '@/components/ui/textarea'
import { errorMessage } from '@/services/workbench'
import { getAgentHandoff, getGrokRuntime, getPiRuntime, type AgentContinuationSource, type AgentHandoffPreview } from '@/services/workbench-agent'
import { agentHostLabels, grokRuntimeLabel, piThinkingLabels, runtimeIdentityLabel, type AgentHost, type PiModel, type PiThinkingLevel } from '@/services/workbench-platform'

const props = defineProps<{
  specId: string
  source?: AgentContinuationSource
  target: AgentHost
  projectName: string
  permission: string
  sourceIssue?: string
  existing?: AgentHandoffPreview
  destination?: { identity?: string | null; model?: PiModel; level?: PiThinkingLevel; status?: string }
}>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{
  confirm: [preview: AgentHandoffPreview, destination: { identity?: string | null; model?: PiModel; level?: PiThinkingLevel }]
  closed: [event: Event]
}>()
const bodyId = useId()
const preview = shallowRef<AgentHandoffPreview>()
const body = shallowRef('')
const runtime = shallowRef<typeof props.destination>()
const busy = shallowRef(false)
const error = shallowRef('')
const retry = shallowRef(0)
const bytes = computed(() => new TextEncoder().encode(body.value).byteLength)
const tooLong = computed(() => bytes.value > 32 * 1024)
const editing = computed(() => !!props.existing)
const refreshChanged = computed(() => editing.value && !!preview.value && props.existing?.digest !== preview.value.digest)
const destination = computed(() => props.destination ?? runtime.value)

watch([open, () => props.specId, () => props.source?.runId, () => props.source?.jobId, () => props.target, retry],
  async ([visible, specId, runId, jobId, host], previous, onCleanup) => {
    if (!visible || !runId || !jobId) return
    const request = new AbortController()
    onCleanup(() => request.abort())
    preview.value = undefined
    runtime.value = undefined
    error.value = ''
    const newSource = !previous?.[0] || specId !== previous[1] || runId !== previous[2] ||
      jobId !== previous[3] || host !== previous[4]
    if (newSource) body.value = props.existing?.context ?? ''
    busy.value = true
    try {
      const result = await getAgentHandoff(specId, { runId, jobId }, host, request.signal)
      if (request.signal.aborted) return
      if (result.source.runId !== runId || result.source.jobId !== jobId || result.targetHost !== host)
        throw new Error('交接资料与所选来源或目标不一致。')
      preview.value = result
      if (!props.existing && !body.value) body.value = result.context
    } catch (cause) {
      if (!request.signal.aborted) error.value = errorMessage(cause)
    } finally {
      if (!request.signal.aborted) busy.value = false
    }
  }, { immediate: true })

watch([open, () => props.target, () => props.destination, () => props.specId, () => props.source?.jobId, retry],
  async ([visible, host, current], _, onCleanup) => {
  if (!visible || current) return
  const request = new AbortController()
  onCleanup(() => request.abort())
  try {
    const result = await (host === 'grok' ? getGrokRuntime({}, request.signal) : getPiRuntime({}, request.signal))
    if (request.signal.aborted) return
    runtime.value = {
      identity: result.identity, model: result.model ?? undefined,
      level: result.thinkingLevel && result.thinkingLevels?.includes(result.thinkingLevel) ? result.thinkingLevel : undefined,
      status: [host === 'grok' ? 'YSS 专用配置' : ('configDir' in result ? result.configDir : undefined),
        result.version, result.executable, result.available ? undefined : result.message ||
        (host === 'grok' ? grokRuntimeLabel(result as Awaited<ReturnType<typeof getGrokRuntime>>) : 'Pi 模型暂不可用')]
        .filter(Boolean).join(' · ')
    }
  } catch (cause) {
    if (!request.signal.aborted) runtime.value = { status: errorMessage(cause) }
  }
}, { immediate: true })

function confirm() {
  if (!preview.value || !body.value.trim() || tooLong.value || props.sourceIssue) return
  emit('confirm', { ...preview.value, context: body.value }, destination.value ?? {})
  open.value = false
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="max-h-[90dvh] overflow-auto sm:max-w-2xl" @close-auto-focus="emit('closed', $event)">
      <DialogHeader>
        <DialogTitle>{{ editing ? '复核交接来源' : '交接给其他 CLI' }}</DialogTitle>
        <DialogDescription>核对并编辑将交给目标会话的资料。发送前可以继续补充本轮要求。</DialogDescription>
      </DialogHeader>
      <dl class="grid gap-2 break-words text-sm sm:grid-cols-[max-content_1fr] sm:gap-x-4">
        <dt class="text-muted-foreground">来源轮次</dt>
        <dd>{{ preview ? agentHostLabels[preview.sourceHost] + ' · ' + preview.sourceTitle : '正在核对' }}
          <code v-if="preview" class="text-xs">{{ preview.source.jobId }}</code>
        </dd>
        <dt class="text-muted-foreground">目标 CLI</dt>
        <dd>{{ agentHostLabels[target] }}<template v-if="destination?.model"> · {{ destination.model.id }}</template>
          <template v-if="destination?.level"> · {{ piThinkingLabels[destination.level] ?? destination.level }}推理</template>
          <span v-if="destination?.status" class="block text-xs text-muted-foreground">{{ destination.status }}</span>
          <span v-if="runtimeIdentityLabel(destination?.identity)" class="block text-xs text-muted-foreground">{{ runtimeIdentityLabel(destination?.identity) }}</span>
        </dd>
        <dt class="text-muted-foreground">主工程与权限</dt>
        <dd>{{ projectName }} · {{ permission }}</dd>
      </dl>
      <div v-if="busy && !preview" class="text-sm text-muted-foreground" role="status">
        <LoadingIndicator :active="true" />正在核对来源资料…
      </div>
      <p v-if="sourceIssue || error" class="text-sm text-destructive" role="status">{{ sourceIssue || error }}</p>
      <div v-if="refreshChanged && preview" class="grid gap-2 rounded-md border p-3 text-sm">
        <p>来源资料已变化。请对照最新资料，核对下方保留的编辑。</p>
        <details>
          <summary class="cursor-pointer">查看最新来源资料</summary>
          <pre class="max-h-40 overflow-auto whitespace-pre-wrap break-words">{{ preview.context }}</pre>
        </details>
      </div>
      <div v-if="preview || editing" class="grid gap-1.5">
        <label :for="bodyId" class="text-sm font-medium">交接正文</label>
        <Textarea :id="bodyId" v-model="body" rows="8" :aria-invalid="tooLong || undefined" />
        <p v-if="tooLong" class="text-sm text-destructive" role="status">请将交接正文缩减至 32 KiB 内。</p>
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" @click="open = false">取消</Button>
        <Button v-if="error" type="button" variant="outline" :disabled="busy" @click="retry++">重新核对</Button>
        <Button type="button" :disabled="!preview || !body.trim() || tooLong || !!sourceIssue" @click="confirm">
          {{ editing ? '确认复核结果' : '创建交接草稿' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
