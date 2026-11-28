import type { AgentToRendererMessage } from '@a2ui/web_core/v1_0'
import { catalogId } from './surface'
import { options, optionNotes, questions, type Round } from './conversation'

type Node = NonNullable<Extract<AgentToRendererMessage, { updateComponents: unknown }>['updateComponents']['components']>[number]

// The Core requires UAX #31 identifiers. Keep business labels independent of protocol IDs.
const protocolId = (id: string) => id.replaceAll('-', '_')
const text = (id: string, value: string): Node => ({ id: protocolId(id), component: 'Text', text: value })
const column = (id: string, children: string[]): Node => ({ id: protocolId(id), component: 'Column', children: children.map(protocolId) })
function action(id: string, label: string, name: string, argument = '', variant: 'primary' | 'borderless' | 'default' = 'default'): Node[] {
  return [
    { id: protocolId(id), component: 'Button', child: protocolId(id + '-label'), variant,
      action: { event: { name, context: { origin: { '@path': '/origin' }, argument } } } },
    text(id + '-label', label),
  ]
}

export function sceneMessages(origin: string, round: Round, latest: boolean, awaitingReceipt = false): AgentToRendererMessage[] {
  const canChange = latest && !awaitingReceipt
  const nodes: Node[] = []
  const content = ['eyebrow', 'title']
  const comparing = round.card === 'compare'
  nodes.push(text('eyebrow', comparing ? '方案比较' : '需求澄清'), text('title', '活动报名截止后能否补报'))
  if (comparing) {
    content.push('compare-grid', 'evidence-basis')
    nodes.push(...action('evidence-basis', round.basis ? '收起方案依据' : '展开方案依据', 'compareBasis', '', 'borderless'))
    if (round.basis) {
      content.push('reason-basis')
      nodes.push(text('reason-basis', '两种方案都在截止时关闭自助入口。若最终名单必须立即交付，统一截止可以减少核对；若希望接纳临时到场者，管理员补录需要有人负责名额和名单。'))
    }
    content.push('footer')
    const keys = ['closed', 'admin'] as const
    nodes.push(column('compare-grid', keys.map(key => 'option-static-' + key)))
    for (const key of keys) {
      const controls = ['option-title-' + key, 'option-note-' + key]
      nodes.push(text(controls[0], options[key]), text(controls[1], optionNotes[key]))
      if (canChange) {
        controls.push('pick-' + key)
        nodes.push(...action('pick-' + key, round.selected === key ? '已选此方案' : '选择此方案', 'pick', key))
      }
      nodes.push(column('option-static-' + key, controls))
    }
    const footer = ['selection']
    nodes.push({ id: 'selection', component: 'Text', text: { '@path': '/selection' } })
    if (canChange && round.selected) {
      footer.push('continue')
      nodes.push(...action('continue', '继续讨论', 'continue', round.selected, 'primary'))
    }
    nodes.push(column('footer', footer))
  } else {
    const keys = ['scope', 'owner'] as const
    content.push('questions')
    nodes.push(column('questions', keys.map(key => 'question-' + key)))
    for (const key of keys) {
      const controls = ['question-title-' + key, 'evidence-' + key]
      const expanded = round.expanded.includes(key)
      nodes.push(text(controls[0], questions[key].title),
        ...action(controls[1], expanded ? '收起依据' : '展开依据', 'evidence', key, 'borderless'))
      if (canChange) {
        controls.push('pick-' + key)
        nodes.push(...action('pick-' + key, key === round.question ? '已选此问题' : '选择此问题', 'pickQuestion', key))
      }
      if (expanded) {
        controls.push('reason-' + key)
        nodes.push(text('reason-' + key, questions[key].reason))
      }
      nodes.push(column('question-' + key, controls))
    }
    if (latest || round.question) {
      content.push('footer')
      const footer = ['selection']
      nodes.push({ id: 'selection', component: 'Text', text: { '@path': '/selection' } })
      if (canChange && round.question) {
        footer.push('continue')
        nodes.push(...action('continue', '继续讨论', 'continueQuestion', round.question, 'primary'))
      }
      if (canChange) {
        footer.push('compare')
        nodes.push(...action('compare', '比较两个方案', 'compare'))
      }
      nodes.push(column('footer', footer))
    }
  }
  const selectedText = round.selected ? '已选「' + options[round.selected] + '」' : '选择一个方案，带入下一轮讨论。'
  const comparisonStatus = latest ? selectedText : round.selected ? '当时选择了「' + options[round.selected] + '」' : '本轮没有选择方案。'
  const questionStatus = round.question ? (latest ? '已选问题' : '当时选择的问题') + ' · ' + questions[round.question].title : '选择想追问的问题。'
  let selection = comparing ? comparisonStatus : questionStatus
  if (awaitingReceipt && latest) {
    const choice = comparing ? round.selected && options[round.selected] : round.question && questions[round.question].title
    selection = choice ? '已选「' + choice + '」 · 发送结果待核实。' : '发送结果待核实，可以继续查看本轮依据。'
  }
  return [
    { version: '1.0', createSurface: { surfaceId: origin, catalogId, dataModel: { origin, selection: '' },
      components: [{ id: 'root', component: 'Card', child: 'content' }, column('content', ['title']), text('title', '活动报名截止后能否补报')] } },
    { version: '1.0', updateComponents: { surfaceId: origin, components: [column('content', content), ...nodes] } },
    { version: '1.0', updateDataModel: { surfaceId: origin, path: '/selection', value: selection } },
  ]
}
