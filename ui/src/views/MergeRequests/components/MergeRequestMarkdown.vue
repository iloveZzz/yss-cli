<script setup lang="ts">
import { computed } from 'vue'
import { renderMarkdown } from '@/views/SpecDetail/schemas/markdown'
import { descriptionLink } from '../schemas/description-links'

const props = defineProps<{ source: string }>()
const html = computed(() => renderMarkdown(props.source, {
  hideSpecMetadata: false,
  staticExternalLink: href => descriptionLink(href, window.location.hostname)
}))

function scrollWithArrows(event: KeyboardEvent) {
  if ((event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return
  const target = event.target
  if (!(target instanceof HTMLElement) || !target.matches('.markdown-code-scroll, .markdown-table-scroll') || target.scrollWidth <= target.clientWidth) return
  // WebKit may leave an explicitly focused horizontal scrolling region stationary on arrow keys.
  event.preventDefault()
  target.scrollBy({ left: event.key === 'ArrowLeft' ? -40 : 40 })
}
</script>

<template>
  <div class="request-markdown" v-html="html" @keydown="scrollWithArrows" />
</template>

<style scoped>
.request-markdown { min-width: 0; line-height: 1.65; overflow-wrap: anywhere; }
.request-markdown :deep(:is(p, ul, ol, blockquote, .markdown-code-scroll, .markdown-table-scroll, hr) + *) { margin-block-start: 9px; }
.request-markdown :deep(:is(h4, h5, h6) + *) { margin-block-start: 5px; }
.request-markdown :deep(:is(h4, h5, h6)) { color: var(--yss-colors-text-title); font-weight: 650; }
.request-markdown :deep(h4) { font-size: 15px; }
.request-markdown :deep(blockquote) { padding-inline-start: 10px; border-inline-start: 3px solid var(--yss-colors-border); color: var(--brand-color-text-secondary); }
.request-markdown :deep(ul), .request-markdown :deep(ol) { padding-inline-start: 1.45em; }
.request-markdown :deep(ul) { list-style: disc; }
.request-markdown :deep(ol) { list-style: decimal; }
.request-markdown :deep(a) { color: var(--yss-color-primary-control-active); text-decoration: underline; text-underline-offset: 2px; }
.request-markdown :deep(code) { font-family: var(--font-mono); font-size: 0.92em; }
.request-markdown :deep(:not(pre) > code) { padding: 1px 3px; border-radius: 3px; background: var(--brand-color-fill-secondary); }
.request-markdown :deep(:is(.markdown-code-scroll, .markdown-table-scroll)) {
  max-inline-size: 100%; overflow-x: auto; overscroll-behavior-x: contain;
  border: 1px solid var(--yss-colors-border); border-radius: 7px;
}
.request-markdown :deep(:is(.markdown-code-scroll, .markdown-table-scroll):focus-visible) { outline-offset: -2px; }
.request-markdown :deep(.markdown-code-scroll) { background: var(--brand-color-bg-layout); }
.request-markdown :deep(pre) { min-inline-size: max-content; padding: 9px 12px; }
.request-markdown :deep(table) { inline-size: 100%; min-inline-size: 375px; border-collapse: collapse; }
.request-markdown :deep(:is(th, td)) { padding: 5px 9px; border-block-end: 1px solid var(--yss-colors-border); text-align: start; overflow-wrap: normal; }
.request-markdown :deep(th) { background: var(--brand-color-bg-layout); color: var(--yss-colors-text-title); font-weight: 650; white-space: nowrap; }
.request-markdown :deep(tbody tr:last-child td) { border-block-end: 0; }
</style>
