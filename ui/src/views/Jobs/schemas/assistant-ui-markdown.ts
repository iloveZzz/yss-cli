import MarkdownIt from 'markdown-it'

const fenceParser = new MarkdownIt({ html: false })
const maybeAssistantUi = /(?:`{3,}|~{3,})[ \t]*a2ui(?=$|[ \t\r\n])/i
export const assistantUiUnavailable = '此回复未提供可阅读的界面内容。可继续请 Agent 用文字说明。'

export function wantsPlainAssistantReply(prompt?: string | null) {
  if (!prompt) return false
  const request = prompt.replace(/(?:不要|别|不用|不能|勿|无需)\s*(?:只|仅)?(?:用|要|以)?\s*(?:纯)?(?:文字|文本)(?:回答|说明|回复)?/g, '')
    .replace(/\b(?:not|avoid|don't use|do not use)\s+(?:only\s+)?(?:plain[ -]text|text[ -]only)\b/gi, '')
  return /(?:用|要|只|请|输出|回复|提供|返回|显示|展示)\s*纯(?:文字|文本)|纯(?:文字|文本)(?:回答|说明|回复|格式)|(?:只|仅)(?:用|要|需|限)?(?:文字|文本)|(?:用|以)(?:文字|文本)(?:回答|说明|回复)|(?:不要|别用|不使用|不用)(?:任何)?(?:交互)?卡片|\b(?:plain[ -]text(?: only)?|text[ -]only|only(?: use)? text|no (?:ui|cards))\b/i.test(request)
    || /^纯(?:文字|文本)(?:比较|总结|概括|[。！!\s]|$)/.test(request.trim())
}

// Suppress protocol text in the plain view and during the lazy Markdown renderer's fallback.
export function withoutAssistantUi(text: string, streaming: boolean) {
  if (!maybeAssistantUi.test(text)) return text
  const lines = text.split(/\r\n?|\n/)
  let end = 0
  const readable: string[] = []
  for (const token of fenceParser.parse(text, {})) {
    if (token.type !== 'fence' || !/^a2ui(?:\s|$)/i.test(token.info.trim()) || !token.map) continue
    readable.push(lines.slice(end, token.map[0]).join('\n'))
    end = token.map[1]
  }
  if (!end) return text
  readable.push(lines.slice(end).join('\n'))
  return readable.join('\n\n').trim() || (streaming ? '' : assistantUiUnavailable)
}

export function plainAssistantUiContent(text: string) {
  // A completed reply containing just one fenced card can still supply readable labels and evidence.
  const block = /^(?:[ \t]*\r?\n)* {0,3}(`{3,}|~{3,})[ \t]*a2ui[ \t]*\r?\n([\s\S]*?)\r?\n {0,3}(`{3,}|~{3,})[ \t\r\n]*$/i.exec(text)
  if (!block || block[1][0] !== block[3][0] || block[1].length > block[3].length) return
  return block[2]
}
