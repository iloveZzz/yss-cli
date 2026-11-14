import MarkdownIt from 'markdown-it'

const markdown = new MarkdownIt({ html: false, linkify: false, typographer: false })
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
markdown.renderer.rules.link_open = (tokens, index, options, _env, renderer) => {
  const token = tokens[index]
  if (token) {
    const href = String(token.attrGet('href') ?? '')
    if (isWebLink(href)) { token.attrSet('target', '_blank'); token.attrSet('rel', 'noopener noreferrer') }
    else { token.attrSet('data-document-link', href); token.attrSet('href', '#document') }
  }
  return renderer.renderToken(tokens, index, options)
}
markdown.renderer.rules.heading_open = (tokens, index, options, rawEnv, renderer) => {
  const env = rawEnv as { headings: Map<string, number> }
  const title = tokens[index + 1]?.children?.map(t => t.content).join('') ?? ''
  const base = title.toLowerCase().replace(/[^\p{L}\p{N}_\s-]/gu, '').replace(/\s/g, '-')
  const count = env.headings.get(base) ?? 0
  env.headings.set(base, count + 1)
  tokens[index]?.attrSet('id', count ? `${base}-${count}` : base)
  return renderer.renderToken(tokens, index, options)
}
export function renderMarkdown(text: string): string {
  return markdown.render(text.replace(/<!--\s*yss-spec\b[\s\S]*?-->/g, ''), { headings: new Map<string, number>() })
}
