export const citationToken = 'workbench_citation'
const citationPrefix = '{{cite:'
const displayPrefix = '{{yss-citation:'
const rawCitation = /^\{\{cite:[a-zA-Z0-9_-]{1,64}(?::\d{1,18}-\d{1,18})?\}\}/
const partialCitation = /^\{\{cite:[a-zA-Z0-9_-]{0,64}(?::\d{0,18}(?:-\d{0,18})?)?\}?$/
const verifiedCitation = /^\{\{yss-citation:[0-9a-f]{32}:\d{1,2}\}\}/
const partialDisplayCitation = /^\{\{yss-citation:(?:[0-9a-f]{0,32}|[0-9a-f]{32}:\d{0,2}\}?)$/

export interface AssistantCitationRenderState {
  pending: boolean
  verified: ReadonlyMap<string, number>
  rendered?: Map<number, number>
}

type CitationToken = {
  type: string
  content: string
  meta?: { number?: number; partial?: boolean } | null
}
type InlineState = {
  src: string
  pos: number
  posMax: number
  env: unknown
  push: (type: string, tag: string, nesting: 0) => CitationToken
}
type CoreState = {
  src: string
  env: unknown
  tokens: { children?: CitationToken[] | null }[]
}
type CitationRulesTarget = {
  inline: { ruler: { before: (before: string, name: string, rule: (state: InlineState, silent: boolean) => boolean) => void } }
  core: { ruler: { after: (after: string, name: string, rule: (state: CoreState) => void) => void } }
}

export function needsAssistantCitationRules(text: string, pending: boolean): boolean {
  if (!pending) return false
  const tail = text.trimEnd()
  if (tail.includes(citationPrefix) || tail.includes(displayPrefix) || tail.endsWith('{')) return true
  const start = tail.lastIndexOf('{{')
  return start !== -1 && isPartialCitation(tail.slice(start))
}

function isPartialCitation(value: string): boolean {
  return citationPrefix.startsWith(value) || displayPrefix.startsWith(value) ||
    partialCitation.test(value) || partialDisplayCitation.test(value)
}

export function installAssistantCitationRules(
  target: CitationRulesTarget,
  options: (env: unknown) => AssistantCitationRenderState | undefined,
) {
  target.inline.ruler.before('text', citationToken, (state, silent) => {
    const citations = options(state.env)
    if (!citations || state.src[state.pos] !== '{') return false
    const tail = state.src.slice(state.pos, Math.min(state.posMax, state.pos + 128))
    const verified = verifiedCitation.exec(tail)?.[0]
    const number = verified ? citations.verified.get(verified) : undefined
    let marker = number ? verified : undefined
    let partial = false
    if (!marker && citations.pending) {
      marker = verified ?? rawCitation.exec(tail)?.[0]
      if (!marker && state.pos + tail.length === state.posMax && isPartialCitation(tail)) {
        marker = tail
        partial = true
      }
    }
    if (!marker) return false
    if (!silent) {
      const token = state.push(citationToken, '', 0)
      token.content = marker
      token.meta = { number, partial }
    }
    state.pos += marker.length
    return true
  })

  target.core.ruler.after('inline', citationToken, (state) => {
    const citations = options(state.env)
    if (!citations) return
    const tail = citations.pending ? state.src.trimEnd() : ''
    for (const block of state.tokens) {
      let linkDepth = 0
      for (const token of block.children ?? []) {
        if (token.type === 'link_open') linkDepth++
        if (token.type === 'link_close') linkDepth--
        if (token.type !== citationToken) continue
        if (linkDepth || (token.meta?.partial && !tail.endsWith(token.content))) {
          token.type = 'text'
        }
      }
    }
  })
}
