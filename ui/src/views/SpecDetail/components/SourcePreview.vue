<script setup lang="ts">
import { computed, nextTick, shallowRef, useTemplateRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { loadSource, loadDocument, errorMessage, dateLabel, type SourceDocument } from '@/services/workbench'
import { useWorkbench } from '@/hooks/useWorkbench'
import { renderMarkdown, resolveDocumentLink } from '../schemas/markdown'
const props = defineProps<{ specId: string; sourcePath: string; documentPath?: string; fragment?: string }>()
const emit = defineEmits<{ navigate: [path: string, fragment: string] }>()
const { snapshot } = useWorkbench()
const source = shallowRef<SourceDocument>(); const error = shallowRef(''); const loading = shallowRef(false); const retry = shallowRef(0)
const mode = shallowRef<'preview' | 'source'>('preview')
const article = useTemplateRef<HTMLElement>('article')
const currentPath = computed(() => props.documentPath ?? props.sourcePath)
const html = computed(() => renderMarkdown(source.value?.text ?? ''))
async function scrollFragment(fragment?: string) {
  if (!fragment) return
  await nextTick()
  const target = Array.from(article.value?.querySelectorAll('[id]') ?? []).find(element => element.id === fragment)
  if (target instanceof HTMLElement) { target.scrollIntoView({ block: 'start' }); target.tabIndex = -1; target.focus({ preventScroll: true }) }
  else error.value = `已打开文档，但没有找到章节“${fragment}”。`
}
watch(() => [props.specId, currentPath.value, snapshot.value?.readAt, retry.value], async (_value, old, cleanup) => {
  const controller = new AbortController(); cleanup(() => controller.abort())
  if (old?.[0] !== props.specId || old?.[1] !== currentPath.value) source.value = undefined
  loading.value = true; error.value = ''
  try {
    const next = props.documentPath ? await loadDocument(props.specId, props.documentPath, controller.signal) : await loadSource(props.specId, controller.signal)
    if (!controller.signal.aborted) { source.value = next; await scrollFragment(props.fragment) }
  } catch (e) { if (!controller.signal.aborted) error.value = errorMessage(e) }
  finally { if (!controller.signal.aborted) loading.value = false }
}, { immediate: true })
watch(() => props.fragment, scrollFragment)
function followLink(event: MouseEvent) {
  const link = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>('a[data-document-link]') : null
  if (!link) return
  event.preventDefault()
  try {
    const target = resolveDocumentLink(currentPath.value, link.dataset.documentLink ?? '')
    if (target.path === currentPath.value) { mode.value = 'preview'; void scrollFragment(target.fragment) }
    else emit('navigate', target.path, target.fragment)
  } catch (e) { error.value = errorMessage(e) }
}
</script>
<template>
  <section class="panel preview">
    <div class="section-heading"><h2>{{ documentPath ? '关联文档' : '主规格' }}</h2><div class="segmented" role="group" aria-label="正文显示方式"><Button variant="ghost" :aria-pressed="mode === 'preview'" @click="mode = 'preview'">预览</Button><Button variant="ghost" :aria-pressed="mode === 'source'" @click="mode = 'source'">Markdown 原文</Button></div></div>
    <p class="path-text">{{ currentPath }}</p>
    <p v-if="loading" class="muted">正在读取来源…</p>
    <div v-if="error" class="notice warning" role="alert">{{ error }}<p v-if="source">保留已读取内容：{{ dateLabel(source.readAt) }}</p><Button variant="outline" :disabled="loading" @click="retry++">重新读取</Button></div>
    <!-- Only markdown-it output with raw HTML, images and unsafe links disabled. -->
    <article v-if="source && mode === 'preview'" ref="article" class="markdown" @click="followLink" v-html="html" />
    <pre v-else-if="source" class="source-text">{{ source.text }}</pre>
    <p v-else-if="!loading && !error" class="empty">暂无正文</p>
    <p class="preview-note">相对链接在工作台内打开；外部链接在新窗口打开。图片不会自动加载。</p>
  </section>
</template>
