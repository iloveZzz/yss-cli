<script setup lang="ts">
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Alert } from '@/components/ui/alert'
import { Card } from '@/components/ui/card'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { computed, nextTick, onBeforeUnmount, shallowRef, useTemplateRef, watch } from 'vue'
import { useEventListener } from '@vueuse/core'
import { Button } from '@/components/ui/button'
import {
  loadSource,
  loadDocument,
  errorMessage,
  dateLabel,
  type SourceDocument
} from '@/services/workbench'
import { useWorkbench } from '@/hooks/useWorkbench'
import { renderMarkdown, resolveDocumentLink } from '../schemas/markdown'
import { loadAgentSource, loadAgentDocument } from '@/services/workbench-agent'
import { useMermaid } from '../hooks/useMermaid'
const props = withDefaults(defineProps<{
  specId: string
  sourcePath: string
  documentPath?: string
  fragment?: string
  embedded?: boolean
  runId?: string
  jobId?: string
  refreshKey?: string | number | null
  active?: boolean
}>(), { active: true })
const emit = defineEmits<{
  navigate: [path: string, fragment: string]
  position: [fragment: string]
}>()
const { snapshot, sourceVersions } = useWorkbench()
const source = shallowRef<SourceDocument>()
const error = shallowRef('')
const loading = shallowRef(false)
const retry = shallowRef(0)
const mode = shallowRef<'preview' | 'source'>('preview')
const article = useTemplateRef<HTMLElement>('article')
const currentPath = computed(() => props.documentPath ?? props.sourcePath)
const html = computed(() => renderMarkdown(source.value?.text ?? ''))
useMermaid(article, html)
const documentKey = computed(() =>
  JSON.stringify([props.specId, currentPath.value, props.runId, props.jobId])
)
const readVersion = computed(() =>
  JSON.stringify([
    documentKey.value,
    props.refreshKey,
    props.runId ? undefined : sourceVersions.value[props.specId],
    props.runId ? undefined : snapshot.value?.specs.find((spec) => spec.record.id === props.specId)?.content.artifactDigest,
    retry.value
  ])
)
let displayedDocument: string | undefined
let displayedVersion: string | undefined
let emittedFragment = ''
let scrollTimer: ReturnType<typeof setTimeout> | undefined
const scrollTarget = computed(() =>
  props.embedded ? (article.value?.closest('.resource-body') ?? window) : window
)
useEventListener(
  scrollTarget,
  'scroll',
  () => {
    if (!props.active) return
    clearTimeout(scrollTimer)
    scrollTimer = setTimeout(() => {
      if (!props.active) return
      const headings = Array.from(
        article.value?.querySelectorAll<HTMLElement>('h1[id],h2[id],h3[id],h4[id]') ?? []
      ).filter((heading) => heading.getClientRects().length)
      const current =
        [...headings].reverse().find((heading) => heading.getBoundingClientRect().top < 160) ?? headings[0]
      if (current && current.id !== emittedFragment) {
        emittedFragment = current.id
        emit('position', current.id)
      }
    }, 400)
  },
  { passive: true }
)
onBeforeUnmount(() => clearTimeout(scrollTimer))
async function scrollFragment(fragment?: string, signal?: AbortSignal) {
  const canScroll = () => !signal?.aborted && props.active
  if (!fragment || !canScroll()) return
  await nextTick()
  if (!canScroll()) return
  const target = Array.from(article.value?.querySelectorAll('[id]') ?? []).find(
    (element) => element.id === fragment
  )
  if (target instanceof HTMLElement) {
    if (!target.getClientRects().length) return
    emittedFragment = fragment
    target.scrollIntoView({ block: 'start' })
    target.tabIndex = -1
    target.focus({ preventScroll: true })
  } else error.value = `已打开文档，但没有找到章节“${fragment}”。`
}
watch(
  [readVersion, () => props.active],
  async ([version, active], _previous, cleanup) => {
    const document = documentKey.value
    const changedDocument = document !== displayedDocument
    if (changedDocument) {
      source.value = undefined
      emittedFragment = ''
    }
    if (!active || (source.value && displayedVersion === version)) {
      loading.value = false
      return
    }
    const controller = new AbortController()
    cleanup(() => controller.abort())
    loading.value = true
    error.value = ''
    try {
      const next = props.runId
        ? props.documentPath
          ? await loadAgentDocument(
              props.runId,
              props.specId,
              props.documentPath,
              controller.signal,
              props.jobId
            )
          : await loadAgentSource(props.runId, props.specId, controller.signal, props.jobId)
        : props.documentPath
          ? await loadDocument(props.specId, props.documentPath, controller.signal)
          : await loadSource(props.specId, controller.signal)
      if (!controller.signal.aborted) {
        displayedDocument = document
        displayedVersion = version
        source.value = next
        if (changedDocument || props.fragment !== emittedFragment)
          await scrollFragment(props.fragment, controller.signal)
      }
    } catch (e) {
      if (!controller.signal.aborted) error.value = errorMessage(e)
    } finally {
      if (!controller.signal.aborted) loading.value = false
    }
  },
  { immediate: true }
)
watch([() => props.fragment, () => props.active], ([fragment]) => {
  if (source.value && !loading.value && fragment !== emittedFragment) void scrollFragment(fragment)
})
function retrySource(event: MouseEvent) {
  (event.currentTarget as HTMLElement).closest<HTMLElement>('.preview')?.focus({ preventScroll: true })
  retry.value++
}
function followLink(event: MouseEvent) {
  const link =
    event.target instanceof Element
      ? event.target.closest<HTMLAnchorElement>('a[data-document-link]')
      : null
  if (!link) return
  event.preventDefault()
  try {
    const target = resolveDocumentLink(currentPath.value, link.dataset.documentLink ?? '')
    if (target.path === currentPath.value) {
      mode.value = 'preview'
      emit('position', target.fragment)
      void scrollFragment(target.fragment)
    } else emit('navigate', target.path, target.fragment)
  } catch (e) {
    error.value = errorMessage(e)
  }
}
</script>
<template>
  <Card
    class="panel preview block shadow-none"
    :class="{ 'preview-embedded': embedded }"
    role="region"
    :aria-label="documentPath ? '关联文档' : '主规格正文'"
    tabindex="-1"
  >
    <div class="section-heading">
      <h2 v-if="!embedded">{{ documentPath ? '关联文档' : '主规格' }}</h2>
      <div class="segmented" role="group" aria-label="正文显示方式">
        <Button variant="ghost" :aria-pressed="mode === 'preview'" @click="mode = 'preview'">
          预览
        </Button>
        <Button variant="ghost" :aria-pressed="mode === 'source'" @click="mode = 'source'">
          Markdown 原文
        </Button>
      </div>
    </div>
    <p v-if="!embedded" class="path-text">{{ currentPath }}</p>
    <p v-if="loading && source" class="muted" role="status">正在更新来源…</p>
    <Alert v-if="error" class="notice warning" role="alert">
      {{ error }}
      <p v-if="source">保留已读取内容：{{ dateLabel(source.readAt) }}</p>
      <Button variant="outline" :disabled="loading" @click="retrySource">重新读取</Button>
    </Alert>
    <div class="relative">
      <!-- Only markdown-it output with raw HTML, images and unsafe links disabled. -->
      <article
        v-if="source && mode === 'preview'"
        ref="article"
        class="markdown"
        @click="followLink"
        v-html="html"
      />
      <pre v-else-if="source" class="source-text">{{ source.text }}</pre>
      <LoadingSkeleton v-else-if="loading" variant="text" :rows="5" label="正在读取来源…" class="source-placeholder" />
      <Empty v-else-if="!error"><EmptyDescription>暂无正文</EmptyDescription></Empty>
      <LoadingIndicator :active="loading && !!source" placement="overlay" />
    </div>
    <Collapsible v-if="!embedded" class="preview-note">
      <CollapsibleTrigger>阅读说明</CollapsibleTrigger>
      <CollapsibleContent>
        <p>相对链接在工作台内打开，外部链接在新窗口打开。图片不会自动加载。</p>
      </CollapsibleContent>
    </Collapsible>
  </Card>
</template>
<style scoped>
.preview-embedded {
  max-width: 1080px;
  margin: 0 auto;
  padding: 24px;
  border: 0;
  border-radius: 0;
  box-shadow: none;
}
.preview-embedded .section-heading {
  justify-content: flex-end;
}
.preview {
  min-width: 0;
}
.segmented [aria-pressed='false'] {
  color: var(--brand-color-text-secondary);
}
.source-placeholder {
  min-height: 200px;
  padding-block: 20px;
}
.preview-note [data-slot='collapsible-trigger'] {
  width: fit-content;
  cursor: pointer;
}
.preview-note p {
  margin-top: 8px;
}
.markdown :deep(.mermaid-diagram) {
  margin: 16px 0;
  padding: 16px;
  overflow: auto;
  border: 1px solid #dfe3ea;
  border-radius: 8px;
  background: #fff;
}
.markdown :deep(.mermaid-diagram svg) {
  display: block;
  height: auto;
  margin: 0 auto;
}
.markdown :deep(.mermaid-diagram p) {
  margin: 0;
}
@media (width < 720px) {
  .preview-embedded {
    padding: 16px;
  }
}
</style>
