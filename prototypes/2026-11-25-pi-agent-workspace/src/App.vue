<script setup lang="ts">
import { ArrowDown, ArrowRight, ArrowUp, ArrowUpRight, BookOpen, Bot, Check, ChevronDown, CircleAlert, CircleCheck, FileDiff, FileText, FlaskConical, FolderOpen, GitBranch, History, LoaderCircle, PanelLeft, PanelRight, RotateCcw, Search, Settings2, Shield, Square, Terminal, Workflow } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupTextarea } from '@/components/ui/input-group'
import { MessageScroller, MessageScrollerButton, MessageScrollerContent, MessageScrollerItem, MessageScrollerProvider, MessageScrollerViewport } from '@/components/ui/message-scroller'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import AgentTurn from './components/AgentTurn.vue'
import ArtifactPanel from './components/ArtifactPanel.vue'
import RunSettings from './components/RunSettings.vue'
import SessionList from './components/SessionList.vue'
import { useDialogFocus } from './useDialogFocus'
import { files, modelOptions, runLabels, samplePrompt, scenarios, useDemoAgent, type Configuration, type Scenario, type Turn } from './demo-agent'

const {
  scenario, sessions, activeId, current, latest, isBusy, draft, config, commands, activeModel, model, effectiveModel, readiness,
  connection, lastNotice, canSend, loadScenario, selectSession, selectModel, recheck, testConnection, send, stop, respond,
  updateArtifactReview,
} = useDemoAgent()
const compact = ref(window.matchMedia('(max-width: 1079px)').matches)
const desktopHistory = ref(true)
const showArtifacts = ref(!compact.value)
const artifactSheet = ref(false)
const artifactTab = ref<'output' | 'files'>('output')
const historySheet = ref(false)
const showSettings = ref(false)
const showModel = ref(false)
const showSource = ref(false)
const showHistory = ref(false)
const logTurn = ref<Turn>()
const showLogs = ref(false)
const modelQuery = ref('')
const resetKey = ref(0)
const form = ref<HTMLFormElement>()
const media = window.matchMedia('(max-width: 1079px)')
const invalidScenario = ref(false)
const selectedModels = computed(() => modelOptions.filter((option) => `${option.title} ${option.description}`.toLowerCase().includes(modelQuery.value.trim().toLowerCase())))
const readinessOpen = ref(false)
const reviewable = computed(() => current.value?.hasResult && latest.value?.status === 'done')
const addedLines = files.reduce((total, file) => total + file.plus, 0)
const removedLines = files.reduce((total, file) => total + file.minus, 0)
const returnFromHistorySheet = useDialogFocus(historySheet)
const returnFromArtifacts = useDialogFocus(artifactSheet)
const returnFromModel = useDialogFocus(showModel)
const returnFromLogs = useDialogFocus(showLogs)
const returnFromSource = useDialogFocus(showSource)
const returnFromHistory = useDialogFocus(showHistory)
const readinessText = computed(() => {
  if (readiness.value === 'checking') return '正在检查本机模型与凭证…'
  if (readiness.value === 'unavailable') return '在本机 Pi 中找不到默认模型。可重新检测或为新会话选择可用模型。'
  if (connection.value === 'testing') return '正在发送固定的测试消息…'
  if (connection.value === 'failed') return '方舟套餐不可用。请检查套餐状态和专属 API Key，然后重新检测。'
  if (connection.value === 'success') return '模型连接测试通过。'
  return '本机模型与凭证检查通过。'
})

function applyScenario(next: Scenario) {
  loadScenario(next)
  resetKey.value++
  showArtifacts.value = !compact.value && (next === 'default' || next === 'conflict')
  readinessOpen.value = next === 'readiness'
  artifactSheet.value = false
  historySheet.value = false
}

function changeScenario(next: Scenario) {
  invalidScenario.value = false
  window.history.replaceState(null, '', `#${next}`)
  applyScenario(next)
}

function readHash() {
  const hash = window.location.hash.slice(1) || 'default'
  if (!Object.hasOwn(scenarios, hash)) {
    invalidScenario.value = true
    return
  }
  invalidScenario.value = false
  applyScenario(hash as Scenario)
}

function updateLayout(event: MediaQueryListEvent) {
  compact.value = event.matches
  showArtifacts.value = !event.matches && Boolean(current.value?.hasResult)
  artifactSheet.value = false
  historySheet.value = false
}

function pickSession(id: string | null) {
  selectSession(id)
  historySheet.value = false
  readinessOpen.value = readiness.value !== 'ready'
  if (!compact.value) showArtifacts.value = Boolean(current.value?.hasResult)
}

function openArtifacts(tab: 'output' | 'files' = 'output') {
  artifactTab.value = tab
  artifactSheet.value = true
}

function toggleHistory() {
  if (compact.value) historySheet.value = true
  else desktopHistory.value = !desktopHistory.value
}

function toggleSummary() {
  if (compact.value) openArtifacts()
  else showArtifacts.value = !showArtifacts.value
}

function openLogs(turn: Turn) {
  logTurn.value = turn
  showLogs.value = true
}

function openModels() {
  modelQuery.value = ''
  showModel.value = true
}

function chooseModel(id: string) {
  selectModel(id)
  showModel.value = false
  readinessOpen.value = true
}

function editorKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || event.shiftKey || event.altKey || event.ctrlKey || event.metaKey || event.isComposing || event.keyCode === 229) return
  event.preventDefault()
  form.value?.requestSubmit()
}

function saveSettings(next: Configuration, allow: boolean) {
  config.value.projectIds = next.projectIds
  config.value.inputs = next.inputs
  commands.value = allow
}

function submitPrompt() {
  send()
  if (!compact.value && latest.value?.status === 'running') showArtifacts.value = true
}

onMounted(() => {
  readHash()
  media.addEventListener('change', updateLayout)
  window.addEventListener('hashchange', readHash)
})
onBeforeUnmount(() => {
  media.removeEventListener('change', updateLayout)
  window.removeEventListener('hashchange', readHash)
})
</script>

<template>
  <div class="app-shell">
    <div v-if="invalidScenario" class="invalid-scene"><CircleAlert /><h1>原型场景不存在</h1><Button @click="changeScenario('default')">返回会话与成果</Button></div>
    <div v-else class="workspace" :data-artifacts="showArtifacts && !compact" :data-compact="compact" :data-history="desktopHistory && !compact" :key="resetKey">
      <nav v-if="!compact" class="workspace-rail" aria-label="工作区导航">
        <div class="rail-mark" title="YSS 工作台"><Workflow :size="18" aria-hidden="true" /></div>
        <div class="rail-top">
          <Button type="button" variant="ghost" size="icon" class="rail-action" :title="desktopHistory ? '收起会话历史' : '打开会话历史'" :aria-label="desktopHistory ? '收起会话历史' : '打开会话历史'" :aria-expanded="desktopHistory" @click="toggleHistory"><PanelLeft /></Button>
          <Button type="button" variant="ghost" size="icon" class="rail-action" title="查看来源 Spec" aria-label="查看来源 Spec" @click="showSource = true"><FolderOpen /></Button>
          <Button type="button" variant="ghost" size="icon" class="rail-action" title="查看执行历史" aria-label="查看执行历史" @click="showHistory = true"><History /></Button>
          <Button type="button" variant="ghost" size="icon" class="rail-action" title="查看当前成果" aria-label="查看当前成果" @click="openArtifacts()"><FileDiff /></Button>
        </div>
      </nav>
      <aside v-if="desktopHistory && !compact" id="desktop-conversations" class="session-sidebar">
        <div class="sidebar-brand">
          <span class="brand-name">YSS 工作台</span>
        </div>
        <SessionList :sessions="sessions" :active-id="activeId" @select="pickSession" @source="showSource = true" @history="showHistory = true" />
      </aside>
      <main class="conversation" aria-label="Pi Agent 对话">
        <header class="conversation-head">
          <Button v-if="compact" variant="ghost" size="icon-sm" class="mobile-nav history-toggle" title="打开会话历史" aria-label="打开会话历史" :aria-expanded="historySheet" @click="toggleHistory"><PanelLeft /></Button>
          <div class="conversation-heading">
            <div class="heading-title"><FolderOpen :size="16" class="title-icon" aria-hidden="true" /><h1>{{ current?.title ?? '新建会话' }}</h1></div>
          </div>
          <div class="conversation-actions">
            <DropdownMenu>
              <DropdownMenuTrigger as-child><Button variant="ghost" size="icon-sm" class="scenarios-button" aria-label="原型场景" title="原型场景"><FlaskConical /></Button></DropdownMenuTrigger>
              <DropdownMenuContent align="end" class="scenario-menu">
                <DropdownMenuLabel>原型场景 · 模拟数据</DropdownMenuLabel>
                <DropdownMenuItem v-for="(name, key) in scenarios" :key="key" @select="changeScenario(key)"><Check :class="{ invisible: key !== scenario }" /> {{ name }}</DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem @select="changeScenario(scenario)"><RotateCcw /> 重置当前场景</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
            <Button variant="ghost" size="icon-sm" class="work-button" aria-label="运行设置" title="运行设置" @click="showSettings = true"><Settings2 /></Button>
            <Button variant="ghost" size="icon-sm" class="work-button results-icon" :title="compact ? '成果审阅' : '会话摘要'" :aria-label="compact ? '打开成果审阅' : '切换会话摘要'" :aria-pressed="compact ? undefined : showArtifacts" :aria-expanded="compact ? artifactSheet : undefined" @click="toggleSummary"><PanelRight /></Button>
          </div>
        </header>
        <div v-if="!current" class="empty-chat">
          <div class="empty-emblem"><Bot :size="24" /></div>
          <h2>让 Pi Agent 处理这份 Spec</h2>
          <p>描述任务，按需调整参与工程与模型。运行结果可在会话中审阅。</p>
          <Button variant="outline" class="prompt-example" @click="draft = samplePrompt"><BookOpen :size="16" /><span>填写示例任务</span><ArrowRight :size="14" /></Button>
        </div>
        <div v-else class="chat-area">
          <MessageScrollerProvider :key="activeId ?? undefined" auto-scroll default-scroll-position="end">
            <MessageScroller class="thread-scroll">
              <MessageScrollerViewport aria-label="会话消息" class="thread-viewport">
                <MessageScrollerContent class="thread-content">
                  <MessageScrollerItem v-for="(turn, index) in current.turns" :key="turn.id" :message-id="turn.id" :scroll-anchor="true">
                    <AgentTurn :turn="turn" :latest="index === current.turns.length - 1" @answer="respond" @result="openArtifacts" @logs="openLogs" />
                  </MessageScrollerItem>
                </MessageScrollerContent>
              </MessageScrollerViewport>
              <MessageScrollerButton class="jump-latest" size="sm" direction="end" aria-label="回到最新消息"><ArrowDown /> 回到最新</MessageScrollerButton>
            </MessageScroller>
          </MessageScrollerProvider>
        </div>
        <div class="compose-zone">
          <div class="composer-wrap">
            <Button v-if="reviewable" type="button" variant="outline" class="composer-review" @click="openArtifacts('files')"><FileDiff /><span>{{ files.length }} 个文件有变更</span><span class="success-text">+{{ addedLines }}</span><span class="danger-text">−{{ removedLines }}</span><ArrowUpRight /></Button>
            <div v-if="readinessOpen || readiness !== 'ready' || connection === 'failed'" class="readiness-panel" :data-problem="readiness === 'unavailable' || connection === 'failed'">
              <div class="readiness-copy" role="status">
                <LoaderCircle v-if="readiness === 'checking' || connection === 'testing'" :size="16" class="spin" />
                <CircleAlert v-else-if="readiness === 'unavailable' || connection === 'failed'" :size="16" />
                <CircleCheck v-else :size="16" />
                <div><strong>{{ effectiveModel }}</strong><p>{{ readinessText }}</p></div>
              </div>
              <div class="readiness-actions">
                <Button variant="outline" size="sm" :disabled="readiness === 'checking' || connection === 'testing' || isBusy" @click="recheck"><RotateCcw /> 重新检测</Button>
                <Button variant="outline" size="sm" :disabled="readiness !== 'ready' || connection === 'testing' || isBusy" @click="testConnection"><FlaskConical /> 测试模型连接</Button>
              </div>
              <small>连接测试仅发送固定短消息，可能消耗少量额度。</small>
            </div>
            <form ref="form" @submit.prevent="submitPrompt">
              <InputGroup class="prompt-group">
                <InputGroupTextarea
                  v-model="draft" class="prompt-editor" aria-label="任务要求" aria-describedby="composer-shortcut" :rows="2"
                  :placeholder="current ? '继续处理这份 Spec…' : '输入要交给 Pi Agent 的任务…'" @keydown="editorKeydown"
                />
                <InputGroupAddon align="block-end" class="prompt-bar">
                  <div class="prompt-options">
                    <InputGroupButton type="button" variant="ghost" class="prompt-option" @click="showSettings = true" :aria-label="commands ? '运行设置，已允许离线命令' : '运行设置，离线命令关闭'"><Shield /><span>{{ commands ? '本轮已授权' : '命令关闭' }}</span></InputGroupButton>
                    <InputGroupButton v-if="config.projectIds.length > 1" type="button" variant="ghost" class="prompt-option" @click="showSettings = true">{{ config.projectIds.length }} 个工程</InputGroupButton>
                  </div>
                  <div class="prompt-submit">
                    <span v-if="latest?.status === 'waiting'" class="prompt-progress">等待业务答复</span>
                    <InputGroupButton v-if="!current" type="button" variant="ghost" size="icon-xs" class="checking-access" :aria-expanded="readinessOpen" title="模型连接检查" aria-label="模型连接检查" @click="readinessOpen = !readinessOpen"><FlaskConical /></InputGroupButton>
                    <InputGroupButton v-if="!current" type="button" variant="ghost" class="prompt-option" @click="openModels"><span>{{ activeModel.label }}</span><ChevronDown class="option-caret" /></InputGroupButton>
                    <InputGroupButton v-else type="button" variant="ghost" class="prompt-option" :aria-expanded="readinessOpen" @click="readinessOpen = !readinessOpen"><span>{{ activeModel.label }}</span></InputGroupButton>
                    <InputGroupButton v-if="isBusy" type="button" size="icon-sm" class="stop-button" :disabled="latest?.status === 'stopping'" title="停止运行" aria-label="停止运行" @click="stop"><Square :size="14" /></InputGroupButton>
                    <InputGroupButton v-else type="submit" variant="default" size="icon-sm" class="send-button" :disabled="!canSend" :aria-label="current ? '发送并续接' : '发送并运行'" :title="current ? '发送并续接' : '发送并运行'"><ArrowUp :size="18" /></InputGroupButton>
                  </div>
                </InputGroupAddon>
              </InputGroup>
            </form>
            <span id="composer-shortcut" class="sr-only">Enter 发送，Shift 加 Enter 换行。</span>
            <span role="status" class="sr-only">{{ lastNotice }}</span>
          </div>
        </div>
      </main>
      <aside v-if="showArtifacts && !compact" class="artifact-sidebar" aria-labelledby="summary-title">
        <div class="summary-card">
          <h2 id="summary-title" class="sr-only">会话摘要</h2>
          <section class="summary-section" aria-label="文件变更">
            <h3>文件变更</h3>
            <Button v-if="reviewable" type="button" variant="ghost" class="summary-file-row" aria-label="审阅文件" @click="openArtifacts('files')">
              <GitBranch :size="15" /><span>yss-cli · main</span><span class="summary-file-count"><span class="success-text">+{{ addedLines }}</span> <span class="danger-text">−{{ removedLines }}</span></span>
            </Button>
            <p v-else>{{ isBusy ? '本轮结束后可审阅变更。' : '当前没有可审阅的文件。' }}</p>
          </section>
          <section v-if="latest" class="summary-section" aria-label="本轮执行">
            <h3>本轮执行</h3>
            <div class="summary-links">
              <Button type="button" variant="ghost" @click="openLogs(latest)"><Terminal /><span>本轮执行日志</span><span class="summary-status">{{ runLabels[latest.status] }}</span></Button>
            </div>
          </section>
          <section class="summary-section" aria-label="来源">
            <h3>来源</h3>
            <div class="summary-links">
              <Button type="button" variant="ghost" @click="showSource = true"><FileText /><span>工作台加载体验</span><ArrowUpRight /></Button>
            </div>
          </section>
        </div>
      </aside>
    </div>

    <Sheet v-model:open="historySheet">
      <SheetContent side="left" class="history-sheet" @close-auto-focus="returnFromHistorySheet"><SheetHeader class="sheet-heading"><SheetTitle>Pi Agent 会话</SheetTitle><SheetDescription>工作台加载体验</SheetDescription></SheetHeader><SessionList :sessions="sessions" :active-id="activeId" @select="pickSession" @source="historySheet = false; showSource = true" @history="historySheet = false; showHistory = true" /></SheetContent>
    </Sheet>
    <Sheet v-model:open="artifactSheet">
      <SheetContent side="right" class="artifact-sheet" @close-auto-focus="returnFromArtifacts"><SheetHeader class="sheet-heading"><SheetTitle>成果审阅</SheetTitle><SheetDescription>隔离工作区与原工程变更</SheetDescription></SheetHeader><ArtifactPanel :session="current" :scenario="scenario" :initial-tab="artifactTab" sheet @review-change="updateArtifactReview" /></SheetContent>
    </Sheet>
    <RunSettings v-model:open="showSettings" :config="config" :commands="commands" :existing="Boolean(current)" :busy="isBusy" @save="saveSettings" />
    <Dialog v-model:open="showModel">
      <DialogContent class="model-dialog" @close-auto-focus="returnFromModel">
        <DialogHeader><DialogTitle>选择本次模型</DialogTitle><DialogDescription>模型来自本机 Pi。此选择适用于新会话。</DialogDescription></DialogHeader>
        <div class="model-search"><Search :size="16" /><Input v-model="modelQuery" aria-label="搜索供应商或模型" placeholder="搜索供应商或模型…" /></div>
        <div class="model-list">
          <Button v-for="option in selectedModels" :key="option.id" class="model-option" variant="ghost" :aria-pressed="model === option.id" @click="chooseModel(option.id)"><Bot :size="17" /><span><strong>{{ option.title }}</strong><small>{{ option.description }}</small></span><Check v-if="model === option.id" class="model-check" /></Button>
          <p v-if="!selectedModels.length" class="model-empty">没有匹配模型</p>
        </div>
      </DialogContent>
    </Dialog>
    <Dialog v-model:open="showLogs">
      <DialogContent class="review-dialog" @close-auto-focus="returnFromLogs">
        <DialogHeader><DialogTitle>本轮执行日志</DialogTitle><DialogDescription>{{ logTurn?.id }} · {{ logTurn && runLabels[logTurn.status] }}</DialogDescription></DialogHeader>
        <ol class="log-list">
          <li><Terminal :size="16" /><span><strong>准备隔离工作区</strong><small>已读取指定工程与本轮授权</small></span></li>
          <li v-if="logTurn?.step"><FileText :size="16" /><span><strong>读取规格与组件</strong><small>workbench-loading-spec.md</small></span></li>
          <li v-if="logTurn?.commands && logTurn.step >= 2"><Terminal :size="16" /><span><strong>pnpm --dir ui typecheck</strong><small>{{ logTurn.status === 'done' ? '退出码 0' : '命令已结束或中断，核对本轮状态' }}</small></span></li>
        </ol>
      </DialogContent>
    </Dialog>
    <Dialog v-model:open="showSource">
      <DialogContent @close-auto-focus="returnFromSource"><DialogHeader><DialogTitle>工作台加载体验</DialogTitle><DialogDescription>示例 Spec · yss-cli</DialogDescription></DialogHeader><p class="source-spec">{{ samplePrompt }}</p><Button variant="outline" @click="showSource = false"><ArrowRight /> 返回 Pi Agent</Button></DialogContent>
    </Dialog>
    <Dialog v-model:open="showHistory">
      <DialogContent @close-auto-focus="returnFromHistory"><DialogHeader><DialogTitle>执行记录</DialogTitle><DialogDescription>查看与当前 Spec 关联的原执行。</DialogDescription></DialogHeader>
        <div class="log-list"><Button v-for="session in sessions" :key="session.id" variant="ghost" class="history-entry" @click="showHistory = false; pickSession(session.id)"><History /><span><strong>{{ session.title }}</strong><small>{{ session.turns.at(-1)?.id }} · {{ session.time }}</small></span><ArrowUpRight /></Button></div>
      </DialogContent>
    </Dialog>
  </div>
</template>
