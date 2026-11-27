<script setup lang="ts">
import { nextTick, shallowRef, useTemplateRef, watch } from 'vue'
import { FileCode } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { getAgentDiffFile, getAgentDiffSummary, type ReviewExcerpt } from '@/services/workbench-agent'

defineProps<{ excerpts: readonly ReviewExcerpt[]; projectNames: Record<string, string> }>()

const open = shallowRef(false)
const selected = shallowRef<ReviewExcerpt>()
const status = shallowRef<'loading' | 'verified' | 'unavailable'>('loading')
const sourceLines = shallowRef<{ number: number; text: string }[]>([])
const code = useTemplateRef<HTMLElement>('code')
let trigger: HTMLElement | undefined

function show(excerpt: ReviewExcerpt, event: MouseEvent) {
  if (event.currentTarget instanceof HTMLElement) trigger = event.currentTarget
  selected.value = excerpt
  open.value = true
}

function restoreFocus() {
  trigger?.focus({ preventScroll: true })
  trigger = undefined
}

function lineLabel({ startLine, endLine }: ReviewExcerpt) {
  return startLine === endLine ? `${startLine} 行` : `${startLine}–${endLine} 行`
}

function readLines(text: string) {
  if (!text) return []
  const lines = text.split(/\r?\n/)
  if (text.endsWith('\n')) lines.pop()
  return lines
}

watch([open, selected], async ([visible, excerpt], _, onCleanup) => {
  if (!visible || !excerpt) return
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  sourceLines.value = []
  status.value = 'loading'
  const { runId, jobId, bindingRevision, projectId, path, side, startLine, endLine, snippet } = excerpt
  try {
    if (!runId || !jobId || bindingRevision === undefined || !Number.isSafeInteger(bindingRevision) || bindingRevision < 0 ||
      (side !== 'initial' && side !== 'result') || !Number.isSafeInteger(startLine) ||
      !Number.isSafeInteger(endLine) || startLine < 1 || endLine < startLine || endLine - startLine >= 200)
      return
    const source = await getAgentDiffSummary(runId, jobId, controller.signal)
    if (controller.signal.aborted || source.runId !== runId || source.jobId !== jobId ||
      source.bindingRevision !== bindingRevision || !source.reviewToken) return
    const file = source.files.find((item) => item.projectId === projectId && item.path === path)
    if (!file) return
    const detail = await getAgentDiffFile(runId, jobId, source.reviewToken, file, true, controller.signal)
    if (controller.signal.aborted || detail.projectId !== projectId || detail.path !== path || detail.binary) return
    const text = side === 'initial' ? detail.initialText : detail.resultText
    if (typeof text !== 'string') return
    const lines = readLines(text)
    if (endLine > lines.length || lines.slice(startLine - 1, endLine).join('\n') !== snippet) return

    const start = Math.max(0, startLine - 1 - 8)
    sourceLines.value = lines.slice(start, endLine + 8).map((text, index) => ({ number: start + index + 1, text }))
    status.value = 'verified'
    await nextTick()
    if (!controller.signal.aborted && open.value && selected.value === excerpt) {
      const first = code.value?.querySelector<HTMLElement>('[data-citation-highlight]')
      first?.focus({ preventScroll: true })
      first?.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'instant' })
    }
  } catch {
    // The retained excerpt remains available when the source cannot be reopened.
  } finally {
    if (!controller.signal.aborted && status.value === 'loading') status.value = 'unavailable'
  }
}, { flush: 'sync' })
</script>

<template>
  <Dialog v-model:open="open">
    <div class="pi-submitted-citations" role="group" aria-label="已发送的代码引用">
      <Button v-for="(excerpt, index) in excerpts" :key="index" type="button" variant="ghost" size="sm"
        class="citation-trigger" aria-haspopup="dialog" :data-submitted-citation="index"
        :title="excerpt.path" @click="show(excerpt, $event)">
        <FileCode :size="14" aria-hidden="true" />
        <span>{{ excerpt.path.split('/').at(-1) }} · {{ lineLabel(excerpt) }}</span>
      </Button>
    </div>
    <DialogContent v-if="selected" class="pi-review-dialog pi-citation-dialog" @close-auto-focus.prevent="restoreFocus">
      <DialogHeader>
        <DialogTitle>成果引用</DialogTitle>
        <DialogDescription>
          {{ projectNames[selected.projectId] ?? selected.projectId }} / {{ selected.path }}
          · {{ selected.side === 'initial' ? '运行起点' : '本轮结果' }} · {{ lineLabel(selected) }}
        </DialogDescription>
      </DialogHeader>
      <div class="pi-review-body citation-body">
        <p class="pi-file-basis">
          来源轮次 {{ selected.jobId || '未留存' }} · 版本 {{ selected.reviewToken?.slice(0, 10) || '未留存' }}
        </p>
        <p v-if="selected.note" class="citation-note">{{ selected.note }}</p>
        <p v-if="status === 'loading'" class="pi-file-basis" role="status">
          <LoadingIndicator active /> 正在核对来源版本
        </p>
        <p v-else-if="status === 'unavailable'" class="pi-issue" role="status">来源版本无法定位，以下为已留存的片段。</p>
        <div v-if="status === 'verified'" ref="code" class="citation-source" tabindex="0" aria-label="已核对的来源行">
          <div v-for="line in sourceLines" :key="line.number" class="citation-line"
            :data-citation-highlight="line.number >= selected.startLine && line.number <= selected.endLine ? '' : undefined"
            :data-source-line="line.number" :tabindex="line.number === selected.startLine ? -1 : undefined">
            <span class="citation-line-number" :aria-label="`第 ${line.number} 行`">{{ line.number }}</span>
            <code>{{ line.text }}</code>
          </div>
        </div>
        <pre v-else class="pi-file-code citation-snippet" tabindex="0" aria-label="已留存的片段">{{ selected.snippet }}</pre>
      </div>
    </DialogContent>
  </Dialog>
</template>

<style scoped>
.pi-submitted-citations { width: min(100%, 560px); justify-self: end; display: grid; justify-items: end; gap: 4px; }
.citation-trigger { max-width: 100%; height: auto; min-height: 32px; white-space: normal; overflow-wrap: anywhere; text-align: start; color: var(--quiet); text-decoration: underline; text-underline-offset: 3px; }
.citation-body { display: grid; align-content: start; gap: 10px; padding-block: 8px 24px; }
.citation-note { white-space: pre-wrap; overflow-wrap: anywhere; }
.citation-snippet { white-space: pre-wrap; overflow-wrap: anywhere; }
.citation-source { min-width: 0; overflow-x: auto; padding-block: 6px; border: 1px solid var(--border); border-radius: 6px; background: var(--muted); font: 12px/1.6 var(--font-mono, monospace); }
.citation-line { display: grid; grid-template-columns: 6ch 1fr; min-width: max-content; padding-inline: 8px 12px; }
.citation-line:focus-visible { outline-offset: -2px; }
.citation-line-number { color: var(--quiet); user-select: none; }
.citation-line code { white-space: pre; }
.citation-line[data-citation-highlight] { background: var(--accent); color: var(--accent-foreground); font-weight: 600; }
@media (width < 720px), (pointer: coarse) { .citation-trigger { min-height: 44px; } }
</style>
