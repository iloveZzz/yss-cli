import MarkdownIt from 'markdown-it'

const markdown = new MarkdownIt({ html: false, linkify: false, typographer: false })
type MarkdownOptions = {
  documentLinks?: boolean
  headingPrefix?: string
  hideSpecMetadata?: boolean
  assistantCitations?: {
    pending: boolean
    verified: ReadonlyMap<string, number>
    rendered: Set<number>
  }
}
type MarkdownEnvironment = MarkdownOptions & { headings: Map<string, number> }
const citationToken = 'workbench_citation'
const citationPrefix = '{{cite:'
const rawCitation = /^\{\{cite:[a-zA-Z0-9_-]{1,64}(?::\d{1,18}-\d{1,18})?\}\}/
const partialCitation = /^\{\{cite:[a-zA-Z0-9_-]{0,64}(?::\d{0,18}(?:-\d{0,18})?)?\}?$/
const verifiedCitation = /^\{\{yss-citation:[0-9a-f]{32}:\d{1,2}\}\}/

markdown.inline.ruler.before('text', citationToken, (state, silent) => {
  const citations = (state.env as MarkdownEnvironment).assistantCitations
  if (!citations || state.src[state.pos] !== '{') return false
  const tail = state.src.slice(state.pos, Math.min(state.posMax, state.pos + 128))
  const verified = verifiedCitation.exec(tail)?.[0]
  const number = verified ? citations.verified.get(verified) : undefined
  let marker = number ? verified : undefined
  let partial = false
  if (!marker && citations.pending) {
    marker = rawCitation.exec(tail)?.[0]
    if (!marker && state.pos + tail.length === state.posMax &&
      (citationPrefix.startsWith(tail) || partialCitation.test(tail))) {
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

markdown.core.ruler.after('inline', citationToken, (state) => {
  const citations = (state.env as MarkdownEnvironment).assistantCitations
  if (!citations) return
  const tail = citations.pending ? state.src.trimEnd() : ''
  for (const block of state.tokens) {
    let linkDepth = 0
    for (const token of block.children ?? []) {
      if (token.type === 'link_open') linkDepth++
      if (token.type === 'link_close') linkDepth--
      if (token.type !== citationToken) continue
      const number = token.meta?.number
      if (linkDepth || (token.meta?.partial && !tail.endsWith(token.content))) {
        token.type = 'text'
      } else if (typeof number === 'number') {
        citations.rendered.add(number)
      }
    }
  }
})

markdown.renderer.rules[citationToken] = (tokens, index) => {
  const number = tokens[index]?.meta?.number
  if (typeof number !== 'number' || !Number.isSafeInteger(number) || number < 1 || number > 12) return ''
  return `<button type="button" class="pi-inline-citation" data-assistant-citation="${number}" aria-label="查看来源 ${number}">[${number}]</button>`
}

export function isWebLink(url: string): boolean {
  try { const parsed = new URL(url); return ['http:', 'https:'].includes(parsed.protocol) && !parsed.username && !parsed.password } catch { return false }
}
export function isRelativeLink(url: string): boolean {
  return !!url && !/^[a-z][a-z\d+.-]*:/i.test(url) && !/^[\/\\]/.test(url) && !/[\u0000-\u001f\\?]/.test(url)
}
export function resolveDocumentLink(sourcePath: string, href: string): { path: string; fragment: string } {
  if (!isRelativeLink(href)) throw new Error('仅支持项目内相对路径或 http(s) 外部链接。')
  const separator = href.indexOf('#')
  const raw = separator < 0 ? href : href.slice(0, separator)
  const fragment = separator < 0 ? '' : decodeURIComponent(href.slice(separator + 1))
  if (!raw) return { path: sourcePath, fragment }
  const path = decodeURIComponent(raw)
  if (!isRelativeLink(path)) throw new Error('链接包含不支持的路径格式。')
  const parts = sourcePath.split('/').slice(0, -1)
  for (const part of path.split('/')) {
    if (!part || part === '.') continue
    if (part === '..') { if (!parts.length) throw new Error('链接超出当前项目，无法打开。'); parts.pop() }
    else parts.push(part)
  }
  return { path: parts.join('/'), fragment }
}
markdown.validateLink = url => isWebLink(url) || isRelativeLink(url)
markdown.renderer.rules.image = (tokens, index) => `<span class="muted">[图片：${markdown.utils.escapeHtml(tokens[index]?.content ?? '')}，未加载]</span>`
markdown.renderer.rules.link_open = (tokens, index, options, rawEnv, renderer) => {
  const token = tokens[index]
  if (token) {
    const href = String(token.attrGet('href') ?? '')
    if (isWebLink(href)) { token.attrSet('target', '_blank'); token.attrSet('rel', 'noopener noreferrer') }
    else if ((rawEnv as MarkdownEnvironment).documentLinks === false) {
      token.attrs = token.attrs?.filter(([name]) => name !== 'href') ?? null
    } else { token.attrSet('data-document-link', href); token.attrSet('href', '#document') }
  }
  return renderer.renderToken(tokens, index, options)
}
markdown.renderer.rules.heading_open = (tokens, index, options, rawEnv, renderer) => {
  const env = rawEnv as MarkdownEnvironment
  const title = tokens[index + 1]?.children?.map(t => t.content).join('') ?? ''
  const base = title.toLowerCase().replace(/[^\p{L}\p{N}_\s-]/gu, '').replace(/\s/g, '-')
  const count = env.headings.get(base) ?? 0
  env.headings.set(base, count + 1)
  tokens[index]?.attrSet('id', (env.headingPrefix ?? '') + (count ? `${base}-${count}` : base))
  return renderer.renderToken(tokens, index, options)
}
export function renderMarkdown(text: string, options?: MarkdownOptions): string {
  const source = options?.hideSpecMetadata === false
    ? text
    : text.replace(/<!--\s*yss-spec\b[\s\S]*?-->/g, '')
  return markdown.render(source, {
    ...options,
    headings: new Map<string, number>()
  })
}
