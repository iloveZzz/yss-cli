export type Host = 'pi' | 'grok'
export type Example = 'clarify' | 'compare' | 'brief' | 'text' | 'replay' | 'receipt'
export type Option = 'closed' | 'admin'
export type Question = 'scope' | 'owner'
export type CardMode = 'clarify' | 'compare'

export const hosts = { pi: 'Pi', grok: 'Grok Build' }
export const options: Record<Option, string> = { closed: '统一截止', admin: '允许管理员补录' }
export const examples: Record<Example, { label: string; detail: string; prompt: string }> = {
  clarify: { label: '需求澄清', detail: '展开问题依据', prompt: '活动报名截止后还能补报吗？我们需要先明确什么？' },
  compare: { label: '方案比较', detail: '选择后继续讨论', prompt: '活动报名截止后能否补报？帮我比较统一截止和允许管理员补录。' },
  brief: { label: '简短问答', detail: '一句话回复', prompt: '统一截止是什么意思？' },
  replay: { label: '消息恢复', detail: '分段与无效更新', prompt: '请用卡片说明活动补报需要澄清的问题。' },
  receipt: { label: '发送回执', detail: '核对受理结果', prompt: '我想看看两种补报方案，再选择一项继续讨论。' },
  text: { label: '只用文字', detail: '纯文字比较', prompt: '只用文字比较活动统一截止和允许管理员补录。' },
}
export const questions: Record<Question, { title: string; reason: string }> = {
  scope: { title: '截止时间约束谁？', reason: '如果截止时间只关闭参与者入口，管理员可能仍能补录。如果名单在截止时就锁定，两种入口都应关闭。' },
  owner: { title: '谁负责处理例外？', reason: '允许补录后，需要确定谁有权操作、何时核对名额，以及由谁更新最终名单。' },
}
export const optionNotes: Record<Option, string> = {
  closed: '截止后统一关闭报名和补录。名单可立即交付，临时到场者需要等待下次活动。',
  admin: '截止后关闭自助报名，管理员可代为补录。能接纳临时到场者，运营需核对名额和名单。',
}

export interface Round {
  id: number
  prompt: string
  reply: string
  card?: CardMode
  replayStage?: 0 | 1 | 2 | 3 | 4
  question?: Question
  basis?: boolean
  selected?: Option
  expanded: Question[]
}
export interface Session {
  draft: string
  rounds: Round[]
  receipt?: { roundId: number; prompt: string }
  sendError?: string
}

export function newSession(example: Example): Session {
  const reply = {
    clarify: '先明确截止范围和例外处理人，再决定要不要开放补录。',
    replay: '已收到问题清单的说明，卡片内容将继续到达。',
    compare: '两种做法都会关闭自助报名，差别在于截止后谁还能改动名单。',
    receipt: '两种做法都会关闭自助报名。先选择一个方案加入草稿，发送后可以模拟核对同一次请求的回执。',
    brief: '统一截止是指到达截止时间后，参与者和管理员都不能再新增报名。',
    text: '统一截止便于在截止时锁定名单，适合需要提前排座或备料的活动。允许管理员补录可以接纳临时到场者，但运营需要逐笔核对剩余名额，并同步最终名单。',
  }[example]
  return { draft: '', rounds: [{ id: 1, prompt: examples[example].prompt, reply,
    card: example === 'receipt' ? 'compare' : example === 'replay' ? 'clarify'
      : example === 'clarify' || example === 'compare' ? example : undefined, expanded: [] }] }
}

export function draftLine(choice: Option | Question) {
  if (choice === 'scope' || choice === 'owner') return '我想继续讨论这个问题：' + questions[choice].title
  return '我想继续讨论「' + options[choice] + '」方案。'
}

function exampleReply(prompt: string, id: number): Round {
  const base = { id, prompt, expanded: [] }
  if (/一句话|什么意思|什么是统一截止/.test(prompt)) {
    return { ...base, reply: newSession('brief').rounds[0].reply }
  }
  if (prompt.includes(draftLine('admin'))) {
    return { ...base, reply: '按「允许管理员补录」继续讨论。运营需要逐笔核对名额和最终名单。接下来要确定补录权限，以及谁负责收口。', card: 'clarify' }
  }
  if (prompt.includes(draftLine('closed'))) {
    return { ...base, reply: '按「统一截止」继续讨论。运营可以按时交付名单，还需要明确如何向临时到场者说明截止规则。', card: 'clarify' }
  }
  if (prompt.includes(draftLine('scope'))) return { ...base, reply: '可以先确认截止后哪类入口仍然开放，再决定是否允许管理员修改名单。', card: 'compare' }
  if (prompt.includes(draftLine('owner'))) return { ...base, reply: '我们可以先讨论谁接收补录申请、谁核对名额。方案不同，运营承担的工作也不同。', card: 'compare' }
  if (/比较|对照|方案/.test(prompt)) return { ...base, reply: newSession('compare').rounds[0].reply, card: 'compare' }
  if (/补报|报名|澄清/.test(prompt)) return { ...base, reply: newSession('clarify').rounds[0].reply, card: 'clarify' }
  return { ...base, reply: '已收到这条讨论内容。这是本地模拟会话，可以继续比较两种报名方案，或要求只用文字回答。' }
}

export function replyTo(prompt: string, id: number): Round {
  const reply = exampleReply(prompt, id)
  const textOnly = /纯文字|纯文本|(?:只|仅)(?:用|要|需|限)?(?:文字|文本)|(?:用|以)(?:文字|文本)(?:回答|说明|回复)|不要卡片/.test(prompt)
  if (!textOnly || !reply.card) return reply
  const details = reply.card === 'compare'
    ? (Object.keys(options) as Option[]).map(key => options[key] + '：' + optionNotes[key])
    : Object.values(questions).map(question => question.title + ' ' + question.reason)
  return { id, prompt, expanded: [], reply: [reply.reply, ...details].join('\n\n') }
}
