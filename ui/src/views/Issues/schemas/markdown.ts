import MarkdownIt from 'markdown-it'
import { descriptionLink } from '@/views/MergeRequests/schemas/description-links'

const markdown = new MarkdownIt({ html: false, breaks: true, linkify: false })

markdown.core.ruler.after('inline', 'readonly_task_list', ({ tokens, Token }) => {
  for (let index = 2; index < tokens.length; index++) {
    const item = tokens[index - 2]
    const inline = tokens[index]
    if (item?.type !== 'list_item_open' || inline?.type !== 'inline') continue
    const first = inline.children?.[0]
    const match = first?.type === 'text' && /^\[([ xX])\]\s+/u.exec(first.content)
    if (!match || !first || !inline.children) continue
    const checkbox = new Token('readonly_checkbox', '', 0)
    checkbox.markup = match[1] === ' ' ? '' : ' checked'
    checkbox.content = inline.content.slice(match[0].length)
    first.content = first.content.slice(match[0].length)
    inline.children.unshift(checkbox)
    item.attrJoin('class', 'issue-task')
  }
})

markdown.renderer.rules.readonly_checkbox = (tokens, index) => {
  const token = tokens[index]!
  const label = markdown.utils.escapeHtml(token.content)
  return `<input type="checkbox" disabled${token.markup} aria-label="${label}"> `
}

markdown.renderer.rules.image = (tokens, index) => {
  const text = markdown.utils.escapeHtml(tokens[index]?.content || '未提供替代文字')
  return `<span class="issue-image">图片 · ${text}（在原平台查看）</span>`
}

markdown.renderer.rules.link_open = (tokens, index, options, env, renderer) => {
  const token = tokens[index]!
  const href = token.attrGet('href')
  const url = typeof href === 'string' ? descriptionLink(href, location.hostname) : undefined
  const destination = url ?? (typeof env?.original === 'string' ? env.original : undefined)
  if (!destination) {
    return '<a class="issue-reference">（链接不可用）'
  }
  token.attrs = [['href', destination], ['target', '_blank'], ['rel', 'noopener noreferrer']]
  const opening = renderer.renderToken(tokens, index, options)
  return url ? opening : opening + '<span class="issue-reference">在原平台查看</span> '
}

markdown.renderer.rules.table_open = () => '<div class="issue-markdown-table" role="region" aria-label="正文表格" tabindex="0"><table>'
markdown.renderer.rules.table_close = () => '</table></div>\n'

export function renderIssueMarkdown(body: string, original?: string) {
  return markdown.render(body, { original })
}
