<script setup lang="ts">
import MarkdownIt from 'markdown-it'
import { computed } from 'vue'

const props = defineProps<{ source: string }>()
const markdown = new MarkdownIt({ html: false, linkify: false, typographer: false })

function demoExternalLink(href: string) {
  try {
    const url = new URL(href)
    return url.origin === 'https://example.com'
      && url.origin !== window.location.origin
      && !url.username && !url.password
      && url.pathname === '/release-checklist'
  } catch {
    return false
  }
}

// Keep every non-external link's label, while discarding the destination.
markdown.validateLink = () => true
markdown.renderer.rules.link_open = (tokens, index, options, _env, renderer) => {
  const token = tokens[index]
  if (!demoExternalLink(String(token.attrGet('href') ?? ''))) {
    token.tag = 'span'
    token.attrs = [['class', 'markdown-inert-link']]
    const close = tokens.slice(index + 1).find(candidate => candidate.type === 'link_close' && candidate.level === token.level)
    if (close) close.tag = 'span'
  } else {
    token.attrSet('target', '_blank')
    token.attrSet('rel', 'noopener noreferrer')
  }
  return renderer.renderToken(tokens, index, options)
}
markdown.renderer.rules.image = (tokens, index, options, env, renderer) => {
  const alt = renderer.renderInlineAsText(tokens[index].children ?? [], options, env)
  return `<span class="markdown-image">[图片：${markdown.utils.escapeHtml(alt)}，未加载]</span>`
}
markdown.renderer.rules.heading_open = (tokens, index, options, _env, renderer) => {
  const heading = tokens[index]
  const element = `h${Math.min(6, 3 + Number(heading.tag.slice(1)))}`
  heading.tag = element
  if (tokens[index + 2]?.type === 'heading_close') tokens[index + 2].tag = element
  return renderer.renderToken(tokens, index, options)
}
markdown.renderer.rules.table_open = () => '<div class="markdown-table" role="region" aria-label="描述表格，可横向滚动" tabindex="0"><table>'
markdown.renderer.rules.table_close = () => '</table></div>'
markdown.renderer.rules.th_open = (tokens, index, options, _env, renderer) => {
  tokens[index].attrSet('scope', 'col')
  return renderer.renderToken(tokens, index, options)
}

const html = computed(() => markdown.render(props.source))
</script>

<template>
  <div class="request-markdown" v-html="html"></div>
</template>

<style scoped>
.request-markdown {
  min-width: 0;
  color: #3b4451;
  line-height: 1.65;
  overflow-wrap: anywhere;
}

.request-markdown :deep(:is(p, ul, ol, pre, .markdown-table) + *) { margin-block-start: 9px; }
.request-markdown :deep(:is(h4, h5, h6) + *) { margin-block-start: 5px; }
.request-markdown :deep(:is(h4, h5, h6)) { color: var(--title); font-weight: 650; }
.request-markdown :deep(h4) { font-size: 15px; }
.request-markdown :deep(blockquote) { padding-inline-start: 10px; border-inline-start: 3px solid var(--divider); color: var(--muted); }
.request-markdown :deep(ul) { padding-inline-start: 1.4em; list-style: disc; }
.request-markdown :deep(ol) { padding-inline-start: 1.4em; list-style: decimal; }
.request-markdown :deep(a) { color: #2349ad; text-decoration: underline; text-underline-offset: 2px; }
.request-markdown :deep(.markdown-inert-link) { color: inherit; }
.request-markdown :deep(.markdown-image) { color: var(--muted); }
.request-markdown :deep(code) { font-family: var(--mono); font-size: 0.92em; }
.request-markdown :deep(:not(pre) > code) { padding: 1px 3px; border-radius: 3px; background: #edf1f7; }
.request-markdown :deep(pre) { overflow-x: auto; padding: 9px 12px; border: 1px solid var(--divider); border-radius: 7px; background: #f3f5f9; }
.request-markdown :deep(.markdown-table) { max-width: 100%; overflow-x: auto; border: 1px solid var(--divider); border-radius: 7px; }
.request-markdown :deep(table) { width: 100%; min-width: 375px; border-collapse: collapse; }
.request-markdown :deep(:is(th, td)) { padding: 5px 9px; border-bottom: 1px solid var(--divider); text-align: start; }
.request-markdown :deep(th) { background: #eff2f7; color: var(--title); font-weight: 650; }
.request-markdown :deep(tbody tr:last-child td) { border-bottom: 0; }
</style>
