import { computed, onBeforeUnmount, ref } from 'vue'

export const scenarios = {
  default: '会话与成果', new: '新建会话', readiness: '模型恢复', question: '等待业务答复', conflict: '接收冲突',
} as const
export type Scenario = keyof typeof scenarios
export type RunState = 'running' | 'stopping' | 'waiting' | 'done' | 'stopped' | 'interrupted'
export const runLabels: Record<RunState, string> = {
  running: '运行中', stopping: '正在停止', waiting: '待答复', done: '已结束', stopped: '已停止', interrupted: '已中断',
}
export const projects = [
  { id: 'yss-cli', base: '13374bb', label: '主工程' },
  { id: 'yss-spec-project-template', base: '38c2535', label: '关联工程' },
]
export const samplePrompt = '补齐规格原型预览的加载反馈。首次加载展示骨架，刷新时保留上一次内容，失败后可以重试。请先检查现有组件，再修改和验证。'
export const modelOptions = [
  { id: 'default', title: '使用 Pi 默认模型', description: '读取 Pi 当前设置', label: 'Pi 默认模型' },
  { id: 'openai/gpt-6.1-sol', title: 'GPT-6.1 Sol', description: 'openai/gpt-6.1-sol', label: 'GPT-6.1 Sol' },
  { id: 'agent-plan/ark-code-latest', title: 'Ark Code', description: 'agent-plan/ark-code-latest', label: 'Ark Code' },
]
export const files = [
  {
    id: 'skeleton', name: 'LoadingSkeleton.vue', path: 'ui/src/components/LoadingSkeleton.vue', plus: 22, minus: 0, type: '新增',
    diff: [
      { line: '01', type: 'context', code: '<template>' },
      { line: '+02', type: 'add', code: '  <div class="prototype-skeleton" aria-busy="true">' },
      { line: '+03', type: 'add', code: '    <Skeleton class="h-5 w-2/5" />' },
      { line: '+04', type: 'add', code: '    <Skeleton class="h-36 w-full" />' },
      { line: '+05', type: 'add', code: '  </div>' },
      { line: '06', type: 'context', code: '</template>' },
    ],
  },
  {
    id: 'detail', name: 'index.vue', path: 'ui/src/views/SpecDetail/index.vue', plus: 16, minus: 7, type: '修改',
    diff: [
      { line: '79', type: 'context', code: '<section aria-label="原型预览">' },
      { line: '−80', type: 'remove', code: '  <Spinner v-if="loading" />' },
      { line: '+80', type: 'add', code: '  <LoadingSkeleton v-if="loading && !preview" />' },
      { line: '+81', type: 'add', code: '  <RetryNotice v-if="error" @retry="refresh" />' },
      { line: '82', type: 'context', code: '  <PrototypeView v-if="preview" :source="preview" />' },
      { line: '83', type: 'context', code: '</section>' },
    ],
  },
]

export interface ArtifactReview {
  conflict: 'initial' | 'stale' | 'review' | 'verified'
  inspected: string[]
  accepted: string[]
}

function initialArtifactReview(): ArtifactReview {
  return { conflict: 'initial', inspected: [], accepted: [] }
}

export interface Turn {
  id: string
  prompt: string
  text: string
  time: string
  status: RunState
  step: number
  commands: boolean
  demoDetails?: boolean
  answer?: string
  previous?: boolean
  artifactReview?: ArtifactReview
}
export interface Configuration {
  projectIds: string[]
  inputs: Record<string, string>
}
export interface Session {
  id: string
  title: string
  time: string
  model: string
  config: Configuration
  turns: Turn[]
  draft: string
  commands: boolean
  hasResult: boolean
}

export function initialConfiguration(): Configuration {
  return { projectIds: ['yss-cli'], inputs: {} }
}

function initialSessions(scenario: Scenario): Session[] {
  const example: Session = {
    id: 'session-03', title: '补齐原型加载反馈', time: '今天 14:28', model: 'openai/gpt-6.1-sol', config: initialConfiguration(),
    draft: '', commands: false, hasResult: true,
    turns: [{
      id: 'run-03', prompt: samplePrompt, time: '14:26', status: 'done', step: 3, commands: true, demoDetails: true,
      text: '已在隔离工作区补齐预览加载状态。首次打开显示 Skeleton，刷新时保留上一次预览，读取失败后可以原位重试。',
      artifactReview: initialArtifactReview(),
    }],
  }
  const other: Session = {
    id: 'session-02', title: '核对规格详情入口', time: '今天 11:06', model: 'openai/gpt-6.1-sol', config: initialConfiguration(),
    draft: '', commands: false, hasResult: false,
    turns: [{ id: 'run-02', prompt: '核对规格详情中的原型入口与空状态。', text: '原型入口位于「关联资源」。当主规格未登记可预览的资源时，页面显示空状态，并保留返回规格正文的入口。', time: '11:06', status: 'done', step: 1, commands: false }],
  }
  const older: Session = {
    id: 'session-01', title: '回看加载方案取舍', time: '昨天 17:42', model: 'openai/gpt-6.1-sol', config: initialConfiguration(),
    draft: '', commands: false, hasResult: false,
    turns: [{ id: 'run-01', prompt: '回看加载方案取舍，整理成可独立验收的规则。', text: '建议区分首次打开、已有预览刷新和加载失败三种场景。对应反馈为 Skeleton、局部加载指示和区内重试。', time: '17:42', status: 'interrupted', step: 1, commands: false }],
  }

  if (scenario === 'question') {
    example.hasResult = false
    Object.assign(example.turns[0]!, { step: 1, status: 'waiting', text: '我已找到原型预览和共享加载组件。改动前需要确认一个行为。' })
  }

  const history = [
    { title: '检查窄屏里的原型操作', time: '昨天 16:31', prompt: '检查窄屏预览里关闭、展开和重试的位置。', text: '关闭按钮保留在标题栏，重新读取跟随预览错误显示。展开预览时正文仍可通过返回按钮访问。' },
    { title: '梳理预览读取失败的提示', time: '昨天 14:08', prompt: '梳理读取失败时的界面反馈。', text: '读取失败时在预览区内显示原因并提供重新读取。用户可以保留当前规格页的位置。' },
    { title: '复核隔离稿文件范围', time: '昨天 13:42', prompt: '复核隔离稿里哪些文件需要交给工作台接收。', text: '预览变更只涉及详情视图与共享加载组件。文件接收时还需要核对原工程当前版本。' },
    { title: '检查原型入口的键盘顺序', time: '昨天 11:24', prompt: '确认键盘能访问打开、关闭和重试。', text: '从关联资源按 Tab 可进入预览入口，使用 Escape 关闭后回到该入口。' },
    { title: '梳理加载组件的使用位置', time: '昨天 10:55', prompt: '梳理详情页哪些区域需要加载指示。', text: '主规格正文和原型预览独立读取。用户打开预览时，只在预览区反馈加载进度。' },
    { title: '建立原型评审样例', time: '昨天 10:03', prompt: '准备可观察的预览加载评审样例。', text: '准备首次读取、刷新时已有内容和读取失败三个样例，便于分别检查页面反馈。' },
  ].map(({ title, time, prompt, text }, index): Session => ({
    id: `session-history-${index}`, title, time, model: example.model, config: initialConfiguration(),
    draft: '', commands: false, hasResult: false,
    turns: [{ id: `run-history-${index}`, prompt, text, time, status: 'done', step: 1, commands: false }],
  }))
  return [example, other, older, ...history]
}

export function useDemoAgent() {
  const scenario = ref<Scenario>('default')
  const sessions = ref<Session[]>([])
  const activeId = ref<string | null>(null)
  const newDraft = ref('')
  const newConfig = ref(initialConfiguration())
  const newCommands = ref(false)
  const newModel = ref('default')
  const readiness = ref<'ready' | 'unavailable' | 'checking'>('ready')
  const connection = ref<'idle' | 'testing' | 'success' | 'failed'>('idle')
  const lastNotice = ref('')
  const answer = ref('')
  const timers = new Set<number>()
  const runTimers = new Map<string, Set<number>>()
  let nonce = 0

  const current = computed(() => sessions.value.find((session) => session.id === activeId.value))
  const latest = computed(() => current.value?.turns.at(-1))
  const isBusy = computed(() => latest.value?.status === 'running' || latest.value?.status === 'stopping' || latest.value?.status === 'waiting')
  const model = computed(() => current.value?.model ?? newModel.value)
  const activeModel = computed(() => modelOptions.find((option) => option.id === model.value) ?? modelOptions[0]!)
  const draft = computed({
    get: () => current.value?.draft ?? newDraft.value,
    set: (value: string) => {
      if (current.value) current.value.draft = value
      else newDraft.value = value
    },
  })
  const config = computed(() => current.value?.config ?? newConfig.value)
  const commands = computed({
    get: () => current.value?.commands ?? newCommands.value,
    set: (value: boolean) => {
      if (current.value) current.value.commands = value
      else newCommands.value = value
    },
  })
  const effectiveModel = computed(() => model.value === 'default' ? (scenario.value === 'readiness' ? 'agent-plan/ark-code-latest' : 'openai/gpt-6.1-sol') : model.value)
  const canSend = computed(() => Boolean(draft.value.trim()) && !isBusy.value && readiness.value === 'ready' && (connection.value === 'idle' || connection.value === 'success'))

  function schedule(action: () => void, delay: number, turn?: Turn) {
    const token = window.setTimeout(() => {
      timers.delete(token)
      if (turn) runTimers.get(turn.id)?.delete(token)
      action()
    }, delay)
    timers.add(token)
    if (turn) {
      const existing = runTimers.get(turn.id) ?? new Set<number>()
      existing.add(token)
      runTimers.set(turn.id, existing)
    }
  }

  function clearRun(turn: Turn) {
    for (const token of runTimers.get(turn.id) ?? []) {
      window.clearTimeout(token)
      timers.delete(token)
    }
    runTimers.delete(turn.id)
  }

  function reset(next: Scenario) {
    timers.forEach((token) => window.clearTimeout(token))
    timers.clear()
    runTimers.clear()
    scenario.value = next
    sessions.value = initialSessions(next)
    activeId.value = next === 'new' || next === 'readiness' ? null : 'session-03'
    newDraft.value = next === 'readiness' ? samplePrompt : ''
    newConfig.value = initialConfiguration()
    newCommands.value = false
    newModel.value = 'default'
    readiness.value = next === 'readiness' ? 'unavailable' : 'ready'
    connection.value = 'idle'
    answer.value = ''
    lastNotice.value = ''
  }

  function selectSession(id: string | null) {
    if (activeId.value === id) return
    nonce++
    activeId.value = id
    readiness.value = !id && scenario.value === 'readiness' && newModel.value === 'default' ? 'unavailable' : 'ready'
    connection.value = 'idle'
    answer.value = ''
    lastNotice.value = ''
  }

  function selectModel(id: string) {
    if (current.value || newModel.value === id) return
    nonce++
    newModel.value = id
    readiness.value = scenario.value === 'readiness' && id === 'default' ? 'unavailable' : 'ready'
    connection.value = 'idle'
    lastNotice.value = ''
  }

  function recheck() {
    if (readiness.value === 'checking') return
    const action = ++nonce
    readiness.value = 'checking'
    connection.value = 'idle'
    schedule(() => {
      if (action !== nonce) return
      readiness.value = 'ready'
      lastNotice.value = '本机模型与凭证检查通过。'
    }, 850)
  }

  let failedOnce = false
  function testConnection() {
    if (readiness.value !== 'ready' || connection.value === 'testing') return
    const action = ++nonce
    connection.value = 'testing'
    schedule(() => {
      if (action !== nonce) return
      if (scenario.value === 'readiness' && effectiveModel.value.startsWith('agent-plan/') && !failedOnce) {
        failedOnce = true
        connection.value = 'failed'
        return
      }
      connection.value = 'success'
      lastNotice.value = '模型连接测试通过。'
    }, 1050)
  }

  function completeRun(session: Session, turn: Turn, afterAnswer = false) {
    const text = afterAnswer
      ? ['我已记录本轮业务答复。', ' 预览刷新时沿用核对后的行为，并补齐加载和失败反馈。', ' 可以打开成果审阅核对本轮变更。']
      : ['已读取本轮任务和规格材料。', ' 我检查了预览加载、刷新和失败恢复路径。', ' 本轮结果已保存在隔离工作区，可在成果审阅中查看文件变更。']
    schedule(() => { turn.step = 1; turn.text = text[0]! }, 650, turn)
    schedule(() => { turn.step = 2; turn.text += text[1] }, 1450, turn)
    schedule(() => { turn.step = 3; turn.text += text[2] }, 2700, turn)
    schedule(() => {
      turn.status = 'done'
      session.hasResult = true
      turn.artifactReview = initialArtifactReview()
      if (current.value?.id === session.id) lastNotice.value = '本轮已结束，成果待审阅。'
      runTimers.delete(turn.id)
    }, 4200, turn)
  }

  function send() {
    if (!canSend.value) return
    const prompt = draft.value.trim()
    let session = current.value
    if (!session) {
      const id = `session-live-${++nonce}`
      sessions.value.unshift({
        id, title: prompt.length > 16 ? `${prompt.slice(0, 16)}…` : prompt, time: '刚刚', model: effectiveModel.value,
        config: { projectIds: [...newConfig.value.projectIds], inputs: { ...newConfig.value.inputs } },
        draft: '', commands: newCommands.value, hasResult: false, turns: [],
      })
      activeId.value = id
      session = current.value!
      newDraft.value = ''
      newCommands.value = false
    }
    const canRunCommands = session.commands
    session.commands = false
    session.draft = ''
    session.time = '刚刚'
    const id = `run-live-${++nonce}`
    session.turns.push({ id, prompt, text: '', time: '刚刚', status: 'running', step: 0, commands: canRunCommands })
    lastNotice.value = '本轮运行已启动。'
    completeRun(session, session.turns.at(-1)!)
  }

  function stop() {
    const turn = latest.value
    if (!turn || (turn.status !== 'running' && turn.status !== 'waiting')) return
    clearRun(turn)
    turn.status = 'stopping'
    lastNotice.value = '已请求停止本轮。'
    schedule(() => {
      turn.status = 'stopped'
      lastNotice.value = '本轮已停止。'
      runTimers.delete(turn.id)
    }, 650, turn)
  }

  function respond(value: string) {
    const text = value.trim()
    const session = current.value
    const turn = latest.value
    if (!text || !session || turn?.status !== 'waiting') return
    turn.answer = text
    turn.status = 'running'
    lastNotice.value = '答复已提交，本轮继续运行。'
    completeRun(session, turn, true)
  }

  function loadScenario(next: Scenario) {
    nonce++
    failedOnce = false
    reset(next)
  }

  function updateArtifactReview(turnId: string, review: ArtifactReview) {
    const turn = latest.value
    if (turn?.id === turnId && turn.status === 'done' && turn.artifactReview) {
      turn.artifactReview = review
    }
  }

  onBeforeUnmount(() => timers.forEach((token) => window.clearTimeout(token)))

  return {
    scenario, sessions, activeId, current, latest, isBusy, draft, config, commands, activeModel, model, effectiveModel, readiness,
    connection, lastNotice, canSend, answer, loadScenario, selectSession, selectModel, recheck, testConnection, send, stop, respond,
    updateArtifactReview,
  }
}
