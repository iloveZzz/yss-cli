import MarkdownIt from 'markdown-it'
const markdown = new MarkdownIt({ html: false, linkify: false, typographer: false })
markdown.validateLink = (url: string) => { try { const parsed = new URL(url); return ['http:', 'https:'].includes(parsed.protocol) && !parsed.username && !parsed.password } catch { return false } }
markdown.renderer.rules.image = (tokens, index) => `<span class="muted">[图片：${markdown.utils.escapeHtml(tokens[index]?.content ?? '')}，未加载]</span>`
markdown.renderer.rules.link_open = (tokens, index, options, _env, renderer) => { const token = tokens[index]; if (token) { token.attrSet('target', '_blank'); token.attrSet('rel', 'noopener noreferrer') }; return renderer.renderToken(tokens, index, options) }
export function renderMarkdown(text: string): string { return markdown.render(text) }
