import MarkdownIt from 'markdown-it'
import { citationToken, installAssistantCitationRules, type AssistantCitationRenderState } from './assistant-citation-markdown'

const markdown = new MarkdownIt({ html: false, linkify: false, typographer: false })
type MarkdownOptions = {
  documentLinks?: boolean
  headingPrefix?: string
  hideSpecMetadata?: boolean
  assistantCitations?: AssistantCitationRenderState
  scrollTables?: boolean
}
type MarkdownEnvironment = MarkdownOptions & { headings: Map<string, number> }
installAssistantCitationRules(markdown, env => (env as MarkdownEnvironment).assistantCitations)

markdown.renderer.rules[citationToken] = (tokens, index, _options, rawEnv) => {
  const number = tokens[index]?.meta?.number
  if (typeof number !== 'number' || !Number.isSafeInteger(number) || number < 1 || number > 12) return ''
  const rendered = (rawEnv as MarkdownEnvironment).assistantCitations?.rendered
  rendered?.set(number, (rendered.get(number) ?? 0) + 1)
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
markdown.renderer.rules.table_open = (tokens, index, options, env, renderer) => {
  const table = renderer.renderToken(tokens, index, options)
  if (!(env as MarkdownEnvironment).scrollTables) return table
  let columns = 0
  for (let i = index + 1; i < tokens.length; i++) {
    const type = tokens[i]?.type
    if (type === 'thead_close' || type === 'table_close') break
    if (type === 'th_open') columns++
  }
  const className = columns === 2 ? 'assistant-table-scroll assistant-simple-table' : 'assistant-table-scroll'
  return `<div class="${className}" role="region" aria-label="可横向滚动的数据表格" tabindex="0">${table}`
}
markdown.renderer.rules.table_close = (tokens, index, options, env, renderer) => {
  const table = renderer.renderToken(tokens, index, options)
  return (env as MarkdownEnvironment).scrollTables ? `${table}</div>` : table
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
