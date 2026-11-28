<script lang="ts">
import { h, type FunctionalComponent } from 'vue'
import { MarkdownRender, setCustomComponents } from 'markstream-vue'
import type { ImageNode, LinkNode, MathInlineNode, MathBlockNode, ParsedNode } from 'stream-markdown-parser'
import { isWebLink } from '../../SpecDetail/schemas/markdown'
import { citationToken } from '../../SpecDetail/schemas/assistant-citation-markdown'
import { assistantMarkdownScope, citationNumber, type AssistantFenceNode } from '../schemas/assistant-markdown'
import AssistantCodeBlock from './AssistantCodeBlock.vue'
import AssistantChart from './AssistantChart.vue'
import AssistantMath from './AssistantMath.vue'
import AssistantMarkdownTable from './AssistantMarkdownTable.vue'

export { createAssistantMarkdown } from '../schemas/assistant-markdown'

const AssistantLink: FunctionalComponent<{ node: LinkNode }> = ({ node }) => {
  const children = node.children.length
    ? h(MarkdownRender, { nodes: node.children, customId: assistantMarkdownScope, renderAsFragment: true, domMode: 'minimal', htmlPolicy: 'escape', typewriter: false })
    : node.text
  const attrs = !node.loading && isWebLink(node.href)
    ? { href: node.href, target: '_blank', rel: 'noopener noreferrer', title: node.title ?? undefined }
    : null
  return h(attrs ? 'a' : 'span', attrs, [children])
}
const AssistantImage: FunctionalComponent<{ node: ImageNode }> = ({ node }) =>
  h('span', { class: 'muted' }, `[图片：${node.alt}，未加载]`)
const AssistantCitation: FunctionalComponent<{ node: { type: string; meta?: unknown } }> = ({ node }) => {
  const number = citationNumber(node)
  if (number === undefined) return null
  return h('button', {
    type: 'button', class: 'pi-inline-citation', 'data-assistant-citation': number, 'aria-label': `查看来源 ${number}`,
  }, `[${number}]`)
}
const AssistantFence: FunctionalComponent<{ node: AssistantFenceNode }> = ({ node }) =>
  node.closedRichFence === 'chart'
    ? h(AssistantChart, { code: node.code })
    : h(AssistantCodeBlock, { code: node.code, language: node.language, diagram: node.closedRichFence === 'mermaid' })
const AssistantFormula: FunctionalComponent<{ node: MathInlineNode | MathBlockNode }> = ({ node }) => {
  const delimiter = node.type === 'math_block' ? '$$' : '$'
  if (node.markup !== delimiter) return node.raw
  if (node.loading) return node.raw.endsWith(delimiter) ? node.raw.slice(0, -delimiter.length) : node.raw
  return h(AssistantMath, { expression: node.content, raw: node.raw, display: node.type === 'math_block' })
}

setCustomComponents(assistantMarkdownScope, {
  [citationToken]: AssistantCitation,
  code_block: AssistantFence,
  mermaid: AssistantFence,
  chart: AssistantFence,
  math_inline: AssistantFormula,
  math_block: AssistantFormula,
  table: AssistantMarkdownTable,
  link: AssistantLink,
  image: AssistantImage,
})
</script>

<script setup lang="ts">
defineProps<{ nodes: ParsedNode[]; streaming?: boolean }>()
</script>

<template>
  <MarkdownRender :nodes="nodes" :custom-id="assistantMarkdownScope" :final="!streaming"
    mode="chat" dom-mode="minimal" html-policy="escape" :typewriter="false" :smooth-streaming="false"
    :batch-rendering="false" :defer-nodes-until-visible="false" :node-virtual="false" :max-live-nodes="0" :fade="false"
    :show-tooltips="false" />
</template>
