import { getMarkdown, parseMarkdownToStructure, type CodeBlockNode, type ParsedNode } from 'stream-markdown-parser'
import type { AssistantCitation } from '@/services/workbench-platform'
import {
  citationToken, installAssistantCitationRules, needsAssistantCitationRules,
  type AssistantCitationRenderState,
} from '../../SpecDetail/schemas/assistant-citation-markdown'
import { isWebLink } from '../../SpecDetail/schemas/markdown'
import { renderAssistantMarkdownFallback } from './assistant-markdown-fallback'
import { withoutAssistantUi } from './assistant-ui-markdown'

export const assistantMarkdownScope = 'yss-assistant-markdown'
const richFenceOpening = /(?:^|[\r\n]) {0,3}(?:`{3,}|~{3,})[ \t]*(?:mermaid|chart|a2ui)(?=$|[ \t\r\n])/i

export type AssistantMarkdownResult = { sources: AssistantCitation[] } & (
  { nodes: ParsedNode[]; html?: never } | { nodes?: never; html: string }
)

export interface AssistantFenceNode extends CodeBlockNode {
  closedRichFence?: 'mermaid' | 'chart' | 'a2ui'
  assistantUiClosesAtEof?: boolean
}

export function citationNumber(node: { type: string; meta?: unknown }): number | undefined {
  if (node.type !== citationToken || typeof node.meta !== 'object' || node.meta === null) return
  const number = 'number' in node.meta ? node.meta.number : undefined
  if (typeof number === 'number' && Number.isInteger(number) && number >= 1 && number <= 12) return number
}

function shownCitations(nodes: readonly unknown[], counts = new Map<number, number>()): Map<number, number> {
  for (const node of nodes) {
    if (!node || typeof node !== 'object' || !('type' in node) || typeof node.type !== 'string') continue
    if (node.type === 'link' || node.type === 'image' || node.type === 'inline_code' || node.type === 'code_block' ||
      node.type === 'html_inline' || node.type === 'html_block') continue
    const number = citationNumber({ type: node.type, meta: 'meta' in node ? node.meta : undefined })
    if (number !== undefined) counts.set(number, (counts.get(number) ?? 0) + 1)
    for (const key of ['children', 'items', 'rows', 'cells'] as const) {
      if (key in node) {
        const children = (node as Partial<Record<typeof key, unknown>>)[key]
        if (Array.isArray(children)) shownCitations(children, counts)
      }
    }
    if ('header' in node && node.header) shownCitations([node.header], counts)
    if ('term' in node && Array.isArray(node.term)) shownCitations(node.term, counts)
    if ('definition' in node && Array.isArray(node.definition)) shownCitations(node.definition, counts)
  }
  return counts
}

function hasClosedFence(node: CodeBlockNode, lines: string[], streaming: boolean): boolean {
  const range = node.sourceMap
  if (!range || range.endLine < range.startLine + 2 || (streaming && range.endLine >= lines.length)) return false
  const opening = /^ {0,3}(`{3,}|~{3,})/.exec(lines[range.startLine] ?? '')?.[1]
  const closing = /^ {0,3}(`{3,}|~{3,})[ \t]*$/.exec(lines[range.endLine - 1] ?? '')?.[1]
  return !!opening && !!closing && opening[0] === closing[0] && closing.length >= opening.length
}

export function createAssistantMarkdown(id: string) {
  const parserOptions = {
    markdownItOptions: { html: false, linkify: false, typographer: false },
    enableMath: true,
    enableContainers: false,
  }
  let plain: ReturnType<typeof getMarkdown> | undefined
  let withCitations: ReturnType<typeof getMarkdown> | undefined
  let citationState: AssistantCitationRenderState | undefined
  const closedFences = new WeakMap<ParsedNode, AssistantFenceNode>()

  function createParser(withRules: boolean) {
    const md = getMarkdown(withRules ? `${id}-citations` : id, {
      ...parserOptions,
      apply: withRules ? [parser => installAssistantCitationRules(parser, () => citationState)] : undefined,
    })
    // Only the application’s verified citation rule may turn a reference into a button.
    md.inline.ruler.disable('reference')
    return md
  }

  return (text: string, streaming: boolean, pending: boolean, candidates: AssistantCitation[], allowAssistantUi = false): AssistantMarkdownResult => {
    if (!text) return { nodes: [], sources: [] }
    const useCitationRules = candidates.length > 0 || needsAssistantCitationRules(text, pending)
    const includeSourceMap = richFenceOpening.test(text)

    try {
      citationState = useCitationRules ? { pending, verified: new Map(candidates.map(item => [item.marker, item.number])) } : undefined
      const md = useCitationRules ? (withCitations ??= createParser(true)) : (plain ??= createParser(false))
      const parsed = parseMarkdownToStructure(text, md, {
        final: !streaming, streamParse: useCitationRules ? false : 'auto',
        includeSourceMap, reuseStableTopLevelNodes: streaming && !useCitationRules && !includeSourceMap,
        validateLink: isWebLink,
      })
      const visible = candidates.length ? shownCitations(parsed) : undefined
      if (visible) {
        const counts = new Map<number, number>()
        const fallback = renderAssistantMarkdownFallback(id, allowAssistantUi ? withoutAssistantUi(text, streaming) : text, pending, candidates, counts)
        if (visible.size !== counts.size || [...counts].some(([number, count]) => visible.get(number) !== count)) return fallback
      }

      let lines: string[] | undefined
      const nodes = includeSourceMap ? parsed.map((node) => {
        if (node.type !== 'code_block') return node
        const fence = node as CodeBlockNode
        const language = fence.language.trim().split(/\s+/, 1)[0]?.toLowerCase()
        if (allowAssistantUi && fence.language.trim().toLowerCase() === 'a2ui') {
          lines ??= text.split(/\r\n?|\n/)
          const closesAtEof = hasClosedFence(fence, lines, false)
          const closed = closesAtEof && (!streaming || (fence.sourceMap?.endLine ?? lines.length) < lines.length)
          const assistantUiFence: AssistantFenceNode = closed
            ? { ...fence, closedRichFence: 'a2ui' }
            : { ...fence, loading: streaming, assistantUiClosesAtEof: closesAtEof }
          return assistantUiFence
        }
        if (language !== 'mermaid' && language !== 'chart') return node
        if (!hasClosedFence(fence, lines ??= text.split(/\r\n?|\n/), streaming)) return node
        let closed = closedFences.get(node)
        if (!closed) {
          closed = { ...fence, closedRichFence: language }
          closedFences.set(node, closed)
        }
        return closed
      }) : parsed
      return { nodes, sources: visible ? candidates.filter(source => visible.has(source.number)) : [] }
    } catch {
      return renderAssistantMarkdownFallback(id, allowAssistantUi ? withoutAssistantUi(text, streaming) : text, pending, candidates)
    } finally {
      citationState = undefined
    }
  }
}
