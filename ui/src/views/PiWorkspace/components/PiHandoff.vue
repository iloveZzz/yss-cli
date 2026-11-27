<script setup lang="ts">
import { computed, shallowRef, useId, watch } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { ApiError, errorMessage } from '@/services/workbench'
import {
  generateAgentContinuationDraft, previewAgentContinuationDraft, type AgentContinuationDraftPreview, type AgentContinuationPreview
} from '@/services/workbench-agent'
import { jobStatusLabels, type PiModel } from '@/services/workbench-platform'

const props = defineProps<{
  specId: string
  source: AgentContinuationPreview
  sourceLink: RouteLocationRaw
  projectIds: string[]
  projectNames: Record<string, string>
  model?: PiModel
  disabled: boolean
  refreshing: boolean
  nativeSelected: boolean
  nativeReady: boolean
}>()
const emit = defineEmits<{ refresh: []; apply: [text: string]; selectNative: [selected: boolean] }>()
const notesId = useId()
const notesErrorId = useId()
const draftId = useId()
const notes = shallowRef('')
const preview = shallowRef<AgentContinuationDraftPreview>()
const suggestion = shallowRef('')
const busy = shallowRef<'preview' | 'generate'>()
const error = shallowRef('')
const stale = shallowRef(false)
const input = computed(() => {
  const { runId, jobId, summaryDigest } = props.source
  if (props.nativeSelected || !props.model || !summaryDigest) return
  return {
    runId, jobId, expectedContinuationDigest: summaryDigest, expectedModel: { ...props.model },
    projectIds: [...props.projectIds].sort(), notes: notes.value
  }
})
const inputKey = computed(() => JSON.stringify([props.specId, input.value]))
const notesTooLong = computed(() => new TextEncoder().encode(notes.value).byteLength > 8192)
let pending: AbortController | undefined

function cancel() {
  pending?.abort()
  pending = undefined
  busy.value = undefined
}

watch(inputKey, (value, previous, onCleanup) => {
  if (previous !== undefined && value !== previous) {
    preview.value = undefined
    suggestion.value = ''
    error.value = ''
    stale.value = false
  }
  onCleanup(cancel)
}, { immediate: true, flush: 'sync' })
watch(() => props.source, () => {
  if (stale.value) {
    stale.value = false
    error.value = ''
  }
})
watch(() => props.disabled, (disabled) => { if (disabled) cancel() })

async function prepare(generate = false) {
  const data = input.value
  const digest = preview.value?.previewDigest
  if (!data || props.disabled || props.refreshing || busy.value || notesTooLong.value || (generate && !digest)) return
  const controller = new AbortController()
  pending = controller
  busy.value = generate ? 'generate' : 'preview'
  error.value = ''
  try {
    if (generate && digest) {
      const result = await generateAgentContinuationDraft(props.specId, data, digest, controller.signal)
      if (pending === controller) suggestion.value = result.draft
    } else {
      const result = await previewAgentContinuationDraft(props.specId, data, controller.signal)
      if (pending === controller) preview.value = result
    }
  } catch (cause) {
    if (pending !== controller) return
    error.value = errorMessage(cause)
    if (cause instanceof ApiError) {
      const issue = cause.context.continuationIssue
      if (issue === 'summaryChanged' || issue === 'previewChanged' || issue === 'scopeChanged') {
        preview.value = undefined
        stale.value = issue === 'summaryChanged'
      }
    }
  } finally {
    if (pending === controller) {
      pending = undefined
      busy.value = undefined
    }
  }
}
</script>

<template>
  <details class="mb-2 rounded-md border bg-card px-3 py-2 text-sm">
    <summary class="cursor-pointer font-medium">
      {{ nativeSelected ? 'Pi 原生分叉 · 首轮只读' : '历史交接摘要' }}
      <span v-if="!nativeSelected && source.nativeFork?.state === 'available'" class="ml-2 font-normal text-muted-foreground">可原生分叉</span>
    </summary>
    <div class="mt-2 grid max-h-[42dvh] gap-3 overflow-auto">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
        <RouterLink :to="sourceLink" class="text-sm underline underline-offset-4">查看来源轮次</RouterLink>
        <span v-if="source.summary" class="text-muted-foreground">
          {{ jobStatusLabels[source.summary.status] ?? source.summary.status }}
        </span>
        <Button type="button" variant="ghost" size="sm" :disabled="disabled || refreshing || !!busy" @click="emit('refresh')">
          <LoadingIndicator :active="refreshing" />刷新历史摘要
        </Button>
      </div>
      <div v-if="source.nativeFork || nativeSelected" class="grid gap-2 rounded-md border p-2.5">
        <p class="font-medium">从此轮开始前分叉</p>
        <p v-if="nativeSelected || source.nativeFork?.state === 'available'" class="text-xs text-muted-foreground">
          新会话带入此轮之前的原生对话，可能包含早期工具结果。工程使用当前检出，首轮只读。
        </p>
        <p v-if="nativeSelected && !nativeReady" class="text-xs text-destructive" role="status">
          {{ source.nativeFork?.message || '分叉来源已变化，请刷新并重新选择。' }}
        </p>
        <p v-else-if="source.nativeFork && source.nativeFork.state !== 'available'" class="text-xs text-muted-foreground">
          {{ source.nativeFork.message }}
        </p>
        <div class="flex flex-wrap gap-2">
          <Button v-if="source.nativeFork?.state === 'available' && !nativeReady" type="button" variant="outline" size="sm"
            :disabled="disabled || refreshing || !!busy" @click="emit('selectNative', true)">
            {{ nativeSelected ? '重新选择原生分叉' : '选择原生分叉' }}
          </Button>
          <Button v-if="nativeSelected" type="button" variant="ghost" size="sm" :disabled="disabled || refreshing"
            @click="emit('selectNative', false)">改用历史摘要</Button>
        </div>
      </div>
      <p v-if="source.originalPrompt" class="max-h-28 overflow-auto whitespace-pre-wrap break-words text-muted-foreground">
        <strong class="text-foreground">原任务</strong>{{ '\n' + source.originalPrompt }}
      </p>
      <div v-if="source.summary" class="grid gap-2 text-xs text-muted-foreground">
        <div v-if="source.summary.goalVersion || source.summary.specDigest" class="flex flex-wrap gap-x-3 gap-y-1 break-all">
          <span v-if="source.summary.goalVersion">目标版本 {{ source.summary.goalVersion }}</span>
          <span v-if="source.summary.specDigest">规格摘要 {{ source.summary.specDigest }}</span>
        </div>
        <ul v-if="source.summary.projects.length" class="grid gap-2">
          <li v-for="project in source.summary.projects" :key="project.projectId" class="grid gap-0.5 break-all">
            <strong class="text-foreground">{{ projectNames[project.projectId] ?? project.projectId }}</strong>
            <span v-if="project.head">{{ project.branch ?? '分离 HEAD' }} · {{ project.head }}</span>
            <span v-for="path in project.frozenPaths" :key="path">{{ path }}</span>
          </li>
        </ul>
        <p v-if="source.summary.ciJob" class="break-all">
          CI #{{ source.summary.ciJob.jobId }} · {{ source.summary.ciJob.status || '已关联' }} · {{ source.summary.ciJob.sha }}
        </p>
        <ul v-if="source.summary.localTasks.length" class="grid gap-1">
          <li v-for="task in source.summary.localTasks" :key="task.jobId" class="break-all">
            {{ projectNames[task.projectId] ?? task.projectId }} · {{ task.action === 'test' ? '测试' : '构建' }} ·
            {{ jobStatusLabels[task.status] ?? task.status }}<template v-if="task.exitCode != null"> · 退出码 {{ task.exitCode }}</template>
          </li>
        </ul>
        <p v-if="source.summary.responseIds.length" class="break-all">已记录答复 {{ source.summary.responseIds.join('、') }}</p>
        <ul v-if="source.summary.gaps.length" class="list-disc space-y-1 pl-4">
          <li v-for="gap in source.summary.gaps" :key="gap">{{ gap }}</li>
        </ul>
      </div>
      <p v-else class="text-muted-foreground">可直接编辑本轮要求，或刷新历史摘要补齐可用证据。</p>
      <div v-if="source.summaryDigest && !nativeSelected" class="grid gap-2 border-t pt-3">
        <label :for="notesId" class="font-medium">补充剩余事项</label>
        <Textarea :id="notesId" v-model="notes" rows="2" maxlength="8192" :aria-invalid="notesTooLong || undefined"
          :aria-describedby="notesTooLong ? notesErrorId : undefined" />
        <p v-if="notesTooLong" :id="notesErrorId" class="text-destructive" role="status">补充内容须在 8 KiB 以内。</p>
        <p v-if="!model" class="text-muted-foreground">选定可用的 Pi 模型后可生成草稿。</p>
        <div v-if="preview" class="grid gap-2">
          <p class="text-xs text-muted-foreground">将发送给 {{ model?.provider }} / {{ model?.id }}</p>
          <pre class="max-h-56 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-2 text-xs">{{ preview.input.systemPrompt }}{{ '\n\n' }}{{ preview.input.prompt }}</pre>
        </div>
        <div v-if="suggestion" class="grid gap-2">
          <label :for="draftId" class="font-medium">模型草稿，待核对</label>
          <Textarea :id="draftId" v-model="suggestion" rows="4" />
        </div>
        <p class="text-xs" :class="error ? 'text-destructive' : 'text-muted-foreground'" role="status">{{ error ||
          (busy === 'preview' ? '正在预览发送内容' : busy === 'generate' ? '正在生成交接草稿' :
            suggestion ? '草稿已生成，可以编辑后填入本轮要求。' : '') }}</p>
        <div class="flex flex-wrap gap-2">
          <Button type="button" size="sm" variant="outline"
            :disabled="disabled || refreshing || !!busy || !input || notesTooLong || stale" @click="prepare(!!preview)">
            <LoadingIndicator :active="!!busy" />{{ preview ? '生成交接草稿' : '预览发送内容' }}
          </Button>
          <Button v-if="busy" type="button" size="sm" variant="ghost" @click="cancel">取消</Button>
          <Button v-if="suggestion.trim()" type="button" size="sm" :disabled="disabled || !!busy"
            @click="emit('apply', suggestion)">填入本轮要求</Button>
        </div>
      </div>
    </div>
  </details>
</template>
