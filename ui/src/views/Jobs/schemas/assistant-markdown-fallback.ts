import type { AssistantCitation } from '@/services/workbench-platform'
import { needsAssistantCitationRules } from '../../SpecDetail/schemas/assistant-citation-markdown'
import { renderMarkdown } from '../../SpecDetail/schemas/markdown'

export function renderAssistantMarkdownFallback(
  id: string, text: string, pending: boolean, candidates: AssistantCitation[], rendered = new Map<number, number>(),
) {
  const useCitationRules = candidates.length > 0 || needsAssistantCitationRules(text, pending)
  const html = renderMarkdown(text, {
    documentLinks: false,
    hideSpecMetadata: false,
    headingPrefix: `${id}-`,
    scrollTables: true,
    assistantCitations: useCitationRules
      ? { pending, verified: new Map(candidates.map(item => [item.marker, item.number])), rendered }
      : undefined,
  })
  return { html, sources: candidates.filter(item => rendered.has(item.number)) }
}
