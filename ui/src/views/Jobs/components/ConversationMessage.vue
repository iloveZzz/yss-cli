<script lang="ts">
const completedMarkdown = new Map<number, { text: string; html: string }>()
</script>

<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { computed, nextTick, shallowRef, useTemplateRef } from 'vue'
import { Message, MessageContent, MessageHeader } from '@/components/ui/message'
import { Button } from '@/components/ui/button'
import { dateLabel } from '@/services/workbench'
import type { JobActivity } from '@/services/workbench-platform'
import { assistantCitations } from '../schemas/conversation'
import { renderMarkdown } from '../../SpecDetail/schemas/markdown'

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
const sourcesPanel = useTemplateRef<HTMLDetailsElement>('sourcesPanel')
const selectedSource = shallowRef<number>()
let citationTrigger: HTMLButtonElement | null = null

const formatted = computed(() => {
  if (user.value) return { html: '', sources: [] }
  const id = rowId.value
  const body = text.value
  const pending = props.citationStreaming ?? props.streaming ?? false
  const candidates = pending ? [] : assistantCitations(props.row?.citations)
  const withCitations = pending || candidates.length > 0
  const cached = id !== undefined && !withCitations ? completedMarkdown.get(id) : undefined
  if (cached?.text === body) return { html: cached.html, sources: [] }
  const rendered = new Set<number>()
  const value = renderMarkdown(body, {
    documentLinks: false,
    hideSpecMetadata: false,
    headingPrefix: `pi-message-${id}-`,
    assistantCitations: withCitations
      ? { pending, verified: new Map(candidates.map(item => [item.marker, item.number])), rendered }
      : undefined
  })
  if (id !== undefined && !withCitations && body.length + value.length <= 64 * 1024) {
    completedMarkdown.delete(id)
    completedMarkdown.set(id, { text: body, html: value })
    if (completedMarkdown.size > 32) completedMarkdown.delete(completedMarkdown.keys().next().value!)
  }
  return { html: value, sources: candidates.filter(item => rendered.has(item.number)) }
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
  if (trigger?.isConnected) focusAndReveal(trigger)
}

function onSourceToggle() {
  if (sourcesPanel.value?.open) return
  citationTrigger = null
  selectedSource.value = undefined
}

function onSourceEscape(event: KeyboardEvent) {
  if (!citationTrigger) return
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
        <!-- MarkdownIt: raw HTML and image loads are disabled. -->
        <div v-else class="source-markdown" :data-assistant-row="rowId" v-html="formatted.html" @click="showCitation" />
        <details v-if="formatted.sources.length" ref="sourcesPanel" class="conversation-sources"
          @toggle="onSourceToggle" @keydown.esc="onSourceEscape"
        >
          <summary>来源 <span>{{ formatted.sources.length }}</span></summary>
          <ol class="conversation-source-list">
            <li v-for="source in formatted.sources" :key="source.number" :data-assistant-source="source.number"
              :value="source.number" tabindex="-1"
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
                <pre>{{ source.snippet }}</pre>
                <p v-if="source.snippetTruncated" class="conversation-source-meta">仅显示已留存选段的开头</p>
              </template>
              <div v-if="(source.kind === 'command' && jobId) || selectedSource === source.number" class="conversation-source-actions">
                <Button v-if="source.kind === 'command' && jobId" as-child variant="link" size="sm">
                  <RouterLink :to="{ path: '/jobs/' + jobId, query: { detail: '1', tab: 'log', activity: String(source.activityId) } }">
                    查看命令日志
                  </RouterLink>
                </Button>
                <Button v-if="selectedSource === source.number" type="button" variant="ghost" size="sm" @click="backToCitation">
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
.conversation-message-body :deep(p) {
  margin: 0;
}
.conversation-message-body :deep(.source-markdown) {
  padding: 0;
  max-width: none;
  overflow: auto;
}
.conversation-message-body :deep(.source-markdown > * + *) {
  margin-top: 0.6em;
}
.conversation-message-body :deep(pre) {
  overflow: auto;
}
.source-markdown :deep(.pi-inline-citation) {
  display: inline-block;
  margin-inline: 0.15em;
  padding-inline: 0.35em;
  border: 1px solid var(--yss-colors-border);
  border-radius: 4px;
  background: var(--brand-color-bg-container);
  color: var(--brand-color-text);
  cursor: pointer;
  font-size: 0.9em;
  font-variant-numeric: tabular-nums;
  line-height: 1.45;
}
.source-markdown :deep(.pi-inline-citation:hover) {
  text-decoration: underline;
}
.conversation-sources {
  margin-top: 12px;
  padding-top: 7px;
  border-top: 1px solid var(--yss-colors-border);
}
.conversation-sources summary {
  width: fit-content;
  cursor: pointer;
  font-weight: 600;
}
.conversation-sources summary span,
.conversation-source-meta {
  color: var(--brand-color-text-secondary);
  font-size: 12px;
  font-weight: normal;
}
.conversation-source-list {
  display: grid;
  gap: 9px;
  margin-block: 7px 0;
  padding-inline-start: 23px;
  list-style: decimal;
}
.conversation-source-list li {
  min-width: 0;
  padding-inline: 4px;
  scroll-margin-block: 6px;
}
.conversation-source-list li:focus,
.source-markdown :deep(.pi-inline-citation:focus-visible),
.conversation-sources summary:focus-visible {
  outline: 2px solid var(--brand-color-text);
  outline-offset: 2px;
}
.conversation-source-list pre {
  max-height: 14rem;
  margin-block: 4px;
  padding: 4px 7px;
  border: 1px solid var(--yss-colors-border);
  border-radius: 5px;
  font-size: 12px;
  white-space: pre-wrap;
}
.conversation-source-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.conversation-streaming {
  display: inline-block;
  font-size: 12px;
  margin-top: 4px;
}
</style>
