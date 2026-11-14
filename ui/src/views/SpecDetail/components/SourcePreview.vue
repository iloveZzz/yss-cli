<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { loadSource, errorMessage, dateLabel, type SourceDocument } from '@/services/workbench'
import { useWorkbench } from '@/hooks/useWorkbench'
import { renderMarkdown } from '../schemas/markdown'
const props = defineProps<{ specId: string }>()
const { snapshot } = useWorkbench()
const source = shallowRef<SourceDocument>(); const error = shallowRef(''); const loading = shallowRef(false); const retry = shallowRef(0)
const mode = shallowRef<'preview' | 'source'>('preview')
const html = computed(() => renderMarkdown(source.value?.text ?? ''))
watch(() => [props.specId, snapshot.value?.readAt, retry.value], async (_value, _old, cleanup) => {
  const controller = new AbortController(); cleanup(() => controller.abort())
  if (source.value?.specId !== props.specId) source.value = undefined
  loading.value = true
  try { const next = await loadSource(props.specId, controller.signal); if (!controller.signal.aborted) { source.value = next; error.value = '' } }
  catch (e) { if (!controller.signal.aborted) error.value = errorMessage(e) }
  finally { if (!controller.signal.aborted) loading.value = false }
}, { immediate: true })
</script>
<template><section class="panel preview"><div class="section-heading"><h2>主规格预览</h2><div class="segmented" aria-label="正文显示方式"><Button variant="ghost" :aria-pressed="mode === 'preview'" @click="mode = 'preview'">预览</Button><Button variant="ghost" :aria-pressed="mode === 'source'" @click="mode = 'source'">Markdown 原文</Button></div></div><p v-if="loading" role="status" class="muted">正在读取来源…</p><div v-if="error" class="notice warning" role="alert">{{ error }}<p v-if="source">保留上次读取内容：{{ dateLabel(source.readAt) }}</p><Button variant="outline" :disabled="loading" @click="retry++">重新读取</Button></div><!-- Only markdown-it output with raw HTML, images and unsafe links disabled. --><article v-if="source && mode === 'preview'" class="markdown" v-html="html" /><pre v-else-if="source" class="source-text">{{ source.text }}</pre><p v-else-if="!loading && !error" class="empty">暂无正文</p><p class="preview-note">图片不会自动加载，本地 HTML 不在工作台中执行。</p></section></template>
