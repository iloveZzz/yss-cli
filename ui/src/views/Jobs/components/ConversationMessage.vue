<script lang="ts">
type MarkdownModule = typeof import('./AssistantMarkdown.vue')
let markdownRequest: Promise<MarkdownModule | undefined> | undefined
let loadedMarkdown: MarkdownModule | undefined

function loadMarkdown() {
  markdownRequest ??= import('./AssistantMarkdown.vue')
    .then(module => (loadedMarkdown = module))
    .catch(() => {
      markdownRequest = undefined
      return undefined
    })
  return markdownRequest
}
</script>

<script setup lang="ts">
import { ChevronDown, CornerUpLeft } from '@lucide/vue'
import { useEventListener } from '@vueuse/core'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { computed, nextTick, shallowRef, useId, useTemplateRef, watch, watchPostEffect } from 'vue'
import { Message, MessageContent, MessageHeader } from '@/components/ui/message'
import { Button } from '@/components/ui/button'
import { dateLabel } from '@/services/workbench'
import type { JobActivity } from '@/services/workbench-platform'
import type { AssistantMarkdownResult } from '../schemas/assistant-markdown'
import { renderAssistantMarkdownFallback } from '../schemas/assistant-markdown-fallback'
import { assistantCitations } from '../schemas/conversation'

const props = defineProps<{
  text?: string
  user?: boolean
  row?: Readonly<JobActivity>
  streaming?: boolean
  jobId?: string
  projectNames?: Record<string, string>
  citationStreaming?: boolean
}>()

const text = computed(() => props.row?.text ?? props.text ?? '')
const user = computed(() => props.row ? props.row.kind === 'user' : props.user)
const rowId = computed(() => props.row?.id)
const createdAt = computed(() => props.row?.createdAt)
const assistantContent = useTemplateRef<HTMLElement>('assistantContent')
const fallbackContent = useTemplateRef<HTMLElement>('fallbackContent')
const sourcesPanel = useTemplateRef<HTMLDetailsElement>('sourcesPanel')
const sourceSummary = useTemplateRef<HTMLElement>('sourceSummary')
const selectedSource = shallowRef<number>()
let citationTrigger: HTMLButtonElement | null = null

const markdownId = `pi-message-${useId()}`
const markdown = shallowRef(loadedMarkdown)
const pendingMarkdown = shallowRef<MarkdownModule>()
let formatAssistant: ReturnType<MarkdownModule['createAssistantMarkdown']> | undefined

function activateMarkdown() {
  if (!pendingMarkdown.value) return
  const content = assistantContent.value
  const page = content?.ownerDocument
  const selection = page?.getSelection()
  // Keep the fallback DOM while someone is using a link, citation, or text selection.
  if (content?.contains(page?.activeElement ?? null) || (citationTrigger?.isConnected && sourcesPanel.value?.open) ||
    (selection && !selection.isCollapsed && (content?.contains(selection.anchorNode) || content?.contains(selection.focusNode)))) return
  markdown.value = pendingMarkdown.value
  pendingMarkdown.value = undefined
}

useEventListener(() => pendingMarkdown.value ? assistantContent.value?.ownerDocument : undefined,
  ['focusout', 'selectionchange'], () => void nextTick(activateMarkdown))

watch(() => !user.value && !!text.value, (enabled, _, onCleanup) => {
  if (!enabled || markdown.value) return
  let active = true
  onCleanup(() => {
    active = false
    pendingMarkdown.value = undefined
  })
  void loadMarkdown().then(module => {
    if (!active || !module) return
    pendingMarkdown.value = module
    activateMarkdown()
  })
}, { immediate: true })

const formatted = computed<AssistantMarkdownResult>(() => {
  if (user.value || !text.value) return { nodes: [], sources: [] }
  const pending = props.citationStreaming ?? props.streaming ?? false
  const candidates = pending ? [] : assistantCitations(props.row?.citations)
  if (!markdown.value) return renderAssistantMarkdownFallback(markdownId, text.value, pending, candidates)
  formatAssistant ??= markdown.value.createAssistantMarkdown(markdownId)
  return formatAssistant(text.value, props.streaming ?? false, pending, candidates)
})

watchPostEffect(onCleanup => {
  const content = fallbackContent.value
  if (!formatted.value.html || !content || typeof ResizeObserver === 'undefined') return
  const tables = content.querySelectorAll<HTMLElement>('.assistant-table-scroll')
  if (!tables.length) return

  function updateTables() {
    for (const table of tables) {
      if (table.scrollWidth > table.clientWidth + 1) {
        table.tabIndex = 0
        table.setAttribute('role', 'region')
        table.setAttribute('aria-label', '可横向滚动的数据表格')
      } else {
        table.removeAttribute('tabindex')
        table.removeAttribute('role')
        table.removeAttribute('aria-label')
      }
    }
  }

  const observer = new ResizeObserver(updateTables)
  for (const table of tables) {
    observer.observe(table)
    if (table.firstElementChild) observer.observe(table.firstElementChild)
  }
  updateTables()
  onCleanup(() => observer.disconnect())
})

function focusAndReveal(element: HTMLElement) {
  element.focus({ preventScroll: true })
  element.scrollIntoView({ block: 'nearest' })
}

async function showCitation(event: MouseEvent) {
  if (!(event.target instanceof Element)) return
  const trigger = event.target.closest<HTMLButtonElement>('button[data-assistant-citation]')
  const panel = sourcesPanel.value
  const number = Number(trigger?.dataset.assistantCitation)
  if (!trigger || !panel || !formatted.value.sources.some(item => item.number === number)) return
  citationTrigger = trigger
  selectedSource.value = number
  panel.open = true
  await nextTick()
  if (citationTrigger !== trigger || !panel.open) return
  const entry = panel.querySelector<HTMLElement>(`[data-assistant-source="${number}"]`)
  if (entry) focusAndReveal(entry)
}

function backToCitation() {
  const trigger = citationTrigger
  citationTrigger = null
  selectedSource.value = undefined
  if (sourcesPanel.value) sourcesPanel.value.open = false
  const target = trigger?.isConnected ? trigger : sourceSummary.value
  if (target) focusAndReveal(target)
}

function onSourceToggle() {
  if (sourcesPanel.value?.open) return
  citationTrigger = null
  selectedSource.value = undefined
  activateMarkdown()
}

function onSourceEscape(event: KeyboardEvent) {
  if (!sourcesPanel.value?.open) return
  event.preventDefault()
  event.stopPropagation()
  backToCitation()
}
</script>

<template>
  <Message as="article" :align="user ? 'end' : 'start'" :aria-busy="streaming || undefined">
    <div class="conversation-message" :class="{ 'conversation-user': user }">
      <MessageHeader class="conversation-message-label">
        <strong>{{ user ? '你' : 'Pi' }}</strong>
        <time v-if="createdAt" :datetime="createdAt" :title="dateLabel(createdAt)">
          {{ dateLabel(createdAt).split(' ').at(-1) }}
        </time>
      </MessageHeader>
      <MessageContent class="conversation-message-body">
        <p v-if="user" class="preserve-lines">{{ text }}</p>
        <div v-else ref="assistantContent" class="assistant-message-content" :data-assistant-row="rowId" @click="showCitation">
          <div class="source-markdown typeset" :class="{ 'assistant-markdown-fallback': formatted.html !== undefined }">
            <div v-if="formatted.html !== undefined" ref="fallbackContent" v-html="formatted.html" />
            <component v-else-if="markdown" :is="markdown.default" :nodes="formatted.nodes" :streaming="streaming" />
          </div>
        </div>
        <details v-if="formatted.sources.length" ref="sourcesPanel" class="conversation-sources"
          @toggle="onSourceToggle" @keydown.esc="onSourceEscape"
        >
          <summary ref="sourceSummary" class="conversation-source-summary">
            <span>来源</span>
            <span class="conversation-source-count">{{ formatted.sources.length }} 条</span>
            <ChevronDown class="conversation-source-chevron" :size="15" aria-hidden="true" />
          </summary>
          <ol class="conversation-source-list">
            <li v-for="source in formatted.sources" :key="source.number" :data-assistant-source="source.number"
              :value="source.number" :data-selected="selectedSource === source.number || undefined" tabindex="-1"
            >
              <code v-if="source.kind === 'command'">{{ source.command || '命令' }}</code>
              <strong v-else>{{ source.path }}</strong>
              <p v-if="source.kind === 'command' && source.exitCode != null" class="conversation-source-meta">
                退出码 {{ source.exitCode }}
              </p>
              <template v-if="source.kind !== 'command'">
                <p class="conversation-source-meta">
                  {{ projectNames?.[source.projectId] || source.projectId }} · 第 {{ source.startLine }}<template v-if="source.endLine !== source.startLine">–{{ source.endLine }}</template> 行 ·
                  <span :title="source.digest">版本 {{ source.digest.slice(0, 10) }}</span>
                </p>
                <pre role="region" aria-label="已留存的来源片段" tabindex="0">{{ source.snippet }}</pre>
                <p v-if="source.snippetTruncated" class="conversation-source-meta">仅显示已留存选段的开头</p>
              </template>
              <div v-if="(source.kind === 'command' && jobId) || selectedSource === source.number" class="conversation-source-actions">
                <Button v-if="source.kind === 'command' && jobId" as-child variant="link" size="sm">
                  <RouterLink :to="{ path: '/jobs/' + jobId, query: { detail: '1', tab: 'log', activity: String(source.activityId) } }">
                    查看命令日志
                  </RouterLink>
                </Button>
                <Button v-if="selectedSource === source.number" type="button" variant="ghost" size="sm" @click="backToCitation">
                  <CornerUpLeft :size="14" aria-hidden="true" />
                  返回引用
                </Button>
              </div>
            </li>
          </ol>
        </details>
        <span v-if="streaming" class="muted conversation-streaming">
          <LoadingIndicator :active="streaming" />
          正在生成…
        </span>
      </MessageContent>
    </div>
  </Message>
</template>

<style scoped>
.conversation-message {
  width: min(100%, 760px);
  min-width: 0;
}
.conversation-user {
  width: fit-content;
  max-width: min(90%, 760px);
}
.conversation-message-label {
  margin-bottom: 5px;
  gap: 8px;
  font-size: 12px;
  color: var(--brand-color-text-secondary);
}
.conversation-user .conversation-message-label {
  justify-content: flex-end;
}
.conversation-message-body {
  padding: 10px 14px;
  border-radius: 12px;
  border: 1px solid var(--yss-colors-border);
  background: var(--brand-color-bg-container);
  font-size: 13px;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.conversation-user .conversation-message-body {
  background: var(--yss-colors-primary-bg);
  border-color: transparent;
}
.conversation-user .preserve-lines {
  margin: 0;
}
.assistant-message-content {
  min-width: 0;
}
.assistant-message-content :deep(.pi-inline-citation) {
  display: inline-block;
  margin-inline: 0.16em;
  min-inline-size: 24px;
  min-block-size: 24px;
  padding-inline: 0.38em;
  border: 1px solid var(--yss-colors-border);
  border-radius: var(--brand-border-radius-sm);
  background: var(--brand-color-bg-layout);
  color: var(--brand-color-text);
  cursor: pointer;
  font-size: 0.81em;
  font-variant-numeric: tabular-nums;
  line-height: 1.35;
}
.assistant-message-content :deep(.pi-inline-citation:hover) {
  background: var(--yss-colors-primary-bg);
  text-decoration: underline;
}
.conversation-sources {
  margin-block-start: 20px;
  padding-block-start: 4px;
  border-block-start: 1px solid var(--yss-colors-border);
}
.conversation-source-summary {
  display: flex;
  align-items: center;
  gap: 8px;
  min-block-size: 36px;
  padding-inline: 4px;
  border-radius: var(--brand-border-radius-sm);
  color: var(--brand-color-text);
  cursor: pointer;
  font-size: 13px;
  font-weight: 600;
  list-style: none;
}
.conversation-source-summary::-webkit-details-marker { display: none; }
.conversation-source-summary:hover { background: var(--brand-color-bg-layout); }
.conversation-source-count,
.conversation-source-meta {
  color: var(--brand-color-text-secondary);
  font-size: 12px;
  font-weight: 400;
  font-variant-numeric: tabular-nums;
}
.conversation-source-chevron {
  flex: none;
  margin-inline-start: auto;
  color: var(--brand-color-text-secondary);
  transform: rotate(-90deg);
}
.conversation-sources[open] .conversation-source-chevron { transform: none; }
.conversation-source-list {
  margin-block: 3px 0;
  padding-inline-start: 26px;
  list-style: decimal;
}
.conversation-source-list li {
  min-width: 0;
  padding: 9px 10px;
  border-block-start: 1px solid var(--yss-colors-border);
  scroll-margin-block: 12px;
}
.conversation-source-list li::marker {
  color: var(--brand-color-text-secondary);
  font-variant-numeric: tabular-nums;
}
.conversation-source-list li[data-selected] {
  border-radius: var(--brand-border-radius-sm);
  background: var(--yss-colors-primary-bg);
}
.conversation-source-list li:focus,
.assistant-message-content :deep(.pi-inline-citation:focus-visible),
.conversation-source-summary:focus-visible {
  outline: 2px solid var(--yss-color-primary-control);
  outline-offset: -1px;
}
.conversation-source-list pre {
  max-block-size: 14rem;
  overflow: auto;
  margin-block: 6px 0;
  padding: 7px 9px;
  border: 1px solid var(--yss-colors-border);
  border-radius: var(--brand-border-radius-sm);
  background: var(--brand-color-bg-container);
  font-size: 12px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.conversation-source-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-block-start: 4px;
}
.conversation-source-actions :deep([data-slot='button']) { min-block-size: 32px; }
@media (pointer: coarse) {
  .conversation-source-summary,
  .conversation-source-actions :deep([data-slot='button']) { min-block-size: 44px; }
}
.conversation-streaming {
  display: inline-block;
  font-size: 12px;
  margin-top: 4px;
}
</style>

<style scoped src="./assistant-typeset.css"></style>
