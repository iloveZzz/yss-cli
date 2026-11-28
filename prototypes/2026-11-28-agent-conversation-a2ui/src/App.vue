<script setup lang="ts">
import { useMediaQuery } from '@vueuse/core'
import { computed, nextTick, ref, watch } from 'vue'
import { ArrowUp, BookOpen, Check, CircleHelp, Columns2, FolderOpen, RefreshCw, PanelLeft,
  MessageCircle, Monitor, NotebookText, PanelLeftClose, Text } from '@lucide/vue'
import { Button } from './components/ui/button'
import { InputGroup, InputGroupAddon, InputGroupTextarea } from './components/ui/input-group'
import { draftLine, examples, hosts, replyTo,
  type Example, type Host, type Option, type Question, type Round } from './conversation'
import { useExampleSessions } from './useExampleSessions'
import RoundCard from './RoundCard.vue'

const caseIds = Object.keys(examples) as Example[]
const hostIds = Object.keys(hosts) as Host[]
const icons = { clarify: CircleHelp, compare: Columns2, brief: MessageCircle, text: Text, replay: RefreshCw, receipt: NotebookText }
const { host, example, sessionId, session, refreshSession } = useExampleSessions()
const rounds = computed(() => session.value.rounds)
const latest = computed(() => rounds.value[rounds.value.length - 1])
const compact = useMediaQuery('(width < 960px)')
const desktopHistory = ref(true)
const sidebar = ref(false)
const historyVisible = computed(() => compact.value ? sidebar.value : desktopHistory.value)
const notice = ref('')
const scroll = ref<HTMLElement>()
const menuButton = ref<InstanceType<typeof Button>>()
const header = ref<HTMLElement>()
const composer = ref<HTMLFormElement>()
let lastFocusInHistory = false
function trackFocus(event: FocusEvent) {
  lastFocusInHistory = event.target instanceof Element && Boolean(event.target.closest('#case-history'))
}
watch(compact, () => {
  // A media query may hide the focused item before the viewport listener runs.
  const active = document.activeElement
  const historyHadFocus = active?.closest('#case-history') != null || (active === document.body && lastFocusInHistory)
  sidebar.value = false
  if (historyHadFocus && !historyVisible.value) {
    void nextTick(() => (menuButton.value?.$el as HTMLElement | undefined)?.focus())
  }
})
function isCurrent(round: Round) {
  return refreshSession() && round === latest.value
}

function scrollToCurrent() {
  void nextTick(() => scroll.value?.querySelector<HTMLElement>('[data-latest="true"]')?.scrollIntoView({ block: 'start' }))
}
function selectHost(value: Host) {
  if (host.value === value) return
  host.value = value
  notice.value = ''
  scrollToCurrent()
}
function selectCase(value: Example) {
  example.value = value
  notice.value = ''
  sidebar.value = false
  scrollToCurrent()
  void nextTick(() => header.value?.focus({ preventScroll: true }))
}
function toggleMenu(open: boolean) {
  if (compact.value) sidebar.value = open
  else desktopHistory.value = open
  void nextTick(() => {
    if (open) document.querySelector<HTMLElement>('.scenario-item[aria-current="true"]')?.focus()
    else (menuButton.value?.$el as HTMLElement | undefined)?.focus()
  })
}
function toggleEvidence(round: Round, key: Question) {
  round.expanded = round.expanded.includes(key) ? round.expanded.filter(item => item !== key) : [...round.expanded, key]
}
function selectOption(round: Round, value: Option) {
  if (!isCurrent(round)) return
  round.selected = value
  notice.value = ''
}
function compare(round: Round) {
  if (!isCurrent(round)) return
  round.card = 'compare'
  round.question = undefined
  void nextTick(() => scroll.value?.querySelector<HTMLElement>('[data-latest="true"] [data-node="title"]')?.focus())
}
function append(round: Round, value: Option | Question) {
  if (!isCurrent(round) || session.value.receipt || (round.card === 'compare' ? round.selected : round.question) !== value) return
  const line = draftLine(value)
  if (session.value.draft.includes(line)) {
    notice.value = '该选项已在草稿中。'
  } else {
    const previous = session.value.draft
    session.value.draft = previous + (previous && !previous.endsWith('\n') ? '\n' : '') + line
    notice.value = '已将所选内容加入草稿，发送后开始下一轮。'
  }
  void nextTick(() => {
    const field = composer.value?.querySelector('textarea')
    field?.focus({ preventScroll: true })
    if (field) field.setSelectionRange(field.value.length, field.value.length)
  })
}
function acceptSubmission(submitted: string) {
  rounds.value.push(replyTo(submitted, latest.value.id + 1))
  if (session.value.draft === submitted) session.value.draft = ''
  delete session.value.receipt
  delete session.value.sendError
  notice.value = '已发送，已生成本地示例回复。'
  scrollToCurrent()
  void nextTick(() => scroll.value?.querySelector<HTMLElement>('[data-latest="true"] .round-meta')?.focus({ preventScroll: true }))
}
function send() {
  if (!refreshSession() || session.value.receipt || !session.value.draft.trim()) return
  if (example.value === 'receipt') {
    session.value.receipt = { roundId: latest.value.id, prompt: session.value.draft }
    delete session.value.sendError
    notice.value = ''
    void nextTick(() => document.querySelector<HTMLElement>('[data-receipt-heading]')?.focus())
    return
  }
  acceptSubmission(session.value.draft)
}
function resolveReceipt(accepted: boolean) {
  if (!refreshSession()) return
  const receipt = session.value.receipt
  if (!receipt || receipt.roundId !== latest.value.id) return
  if (accepted) {
    acceptSubmission(receipt.prompt)
    return
  }
  delete session.value.receipt
  session.value.sendError = '模拟回执：本次发送未受理，草稿已保留，可编辑后再发送。'
  notice.value = ''
  void nextTick(() => composer.value?.querySelector('textarea')?.focus())
}
function onMenuKey(event: KeyboardEvent) {
  if (!sidebar.value) return
  if (event.key === 'Escape') { event.preventDefault(); toggleMenu(false); return }
  if (event.key !== 'Tab' || !compact.value) return
  const items = Array.from(document.querySelectorAll<HTMLElement>('.history button')).filter(el => el.getClientRects().length)
  if (!items.length) return
  if (event.shiftKey && document.activeElement === items[0]) { event.preventDefault(); items.at(-1)?.focus() }
  else if (!event.shiftKey && document.activeElement === items.at(-1)) { event.preventDefault(); items[0].focus() }
}
</script>

<template>
  <div class="workspace pi-workspace" :data-history="desktopHistory" :data-sidebar="sidebar ? 'open' : 'closed'" @keydown="onMenuKey" @focusin="trackFocus">
    <aside class="history pi-history-pane" id="case-history" :inert="!historyVisible" :role="compact && sidebar ? 'dialog' : undefined" :aria-modal="compact && sidebar || undefined" aria-label="示例会话">
      <div class="brand"><h2>会话</h2>
        <Button class="menu-button" aria-label="关闭会话列表" variant="ghost" size="icon-sm" @click="toggleMenu(false)"><PanelLeftClose /></Button>
      </div>
      <div class="history-heading"><FolderOpen :size="15" aria-hidden="true" /> 活动报名</div>
      <nav class="scenario-list" aria-label="示例场景">
        <Button v-for="key in caseIds" :key="key" type="button" variant="ghost" class="scenario-item"
          :aria-current="example === key ? 'true' : undefined" @click="selectCase(key)">
          <component :is="icons[key]" :size="17" aria-hidden="true" />
          <span><strong>{{ examples[key].label }}</strong><small>{{ examples[key].detail }}</small></span>
        </Button>
      </nav>
      <div class="history-foot"><Monitor :size="15" aria-hidden="true" /> 本地交互原型</div>
    </aside>
    <div v-if="sidebar" class="mobile-scrim" aria-hidden="true" @click="toggleMenu(false)"></div>
    <main class="stage pi-stage" :inert="compact && sidebar">
      <header class="bar pi-bar">
        <Button ref="menuButton" class="menu-button" variant="ghost" size="icon-sm" :aria-label="historyVisible ? '收起会话列表' : '打开会话列表'" aria-controls="case-history"
          :aria-expanded="historyVisible" @click="toggleMenu(!historyVisible)"><PanelLeft :size="18" /></Button>
        <div class="bar-title"><FolderOpen :size="16" class="pi-heading-icon" aria-hidden="true" /><h1 ref="header" tabindex="-1">{{ examples[example].label }}</h1></div>
        <div class="host-switch" role="group" aria-label="示例宿主">
          <Button v-for="name in hostIds" :key="name" type="button" variant="ghost" size="sm" :aria-pressed="name === host" @click="selectHost(name)">{{ hosts[name] }}</Button>
        </div>
      </header>
      <div ref="scroll" class="scroll pi-conversation">
        <div class="transcript pi-transcript" :key="sessionId">
          <article class="pi-round pi-turn" v-for="round in rounds" :key="round.id" :data-latest="round === latest" :aria-label="hosts[host] + ' 第 ' + round.id + ' 轮'">
            <div class="round-meta pi-round-meta" tabindex="-1"><NotebookText :size="14" aria-hidden="true" />
              <span>第 {{ round.id }} 轮</span><span aria-hidden="true">·</span><span>{{ hosts[host] }}</span>
              <span v-if="round !== latest">· 历史轮次</span></div>
            <div class="conversation-user"><p class="user-message conversation-message-body">{{ round.prompt }}</p></div>
            <p class="conversation-message-body" :class="round.card ? 'agent-intro' : 'plain-reply'">{{ round.reply }}</p>
            <RoundCard v-if="round.card" :origin="sessionId + '-round-' + round.id" :round="round" :latest="round === latest" :awaiting-receipt="Boolean(session.receipt)" :replay="example === 'replay' && round.id === 1"
              @evidence="toggleEvidence(round, $event)" @pick="selectOption(round, $event)"
              @pick-question="isCurrent(round) && (round.question = $event)" @continue-question="append(round, $event)"
              @advance-replay="isCurrent(round) && (round.replayStage = $event)"
              @basis="round.basis = !round.basis" @compare="compare(round)" @continue="append(round, $event)" />
          </article>
        </div>
      </div>
      <div class="composer-wrap pi-composer-wrap">
        <section v-if="session.receipt" class="receipt-notice" aria-label="本地回执模拟">
          <p data-receipt-heading tabindex="-1" role="status">受理结果待核实。可继续编辑草稿，核对回执后才能再次发送。</p>
          <div class="receipt-actions">
            <Button type="button" variant="outline" size="sm" @click="resolveReceipt(false)">模拟回执：未受理</Button>
            <Button type="button" variant="outline" size="sm" @click="resolveReceipt(true)">模拟回执：已受理</Button>
          </div>
        </section>
        <p v-else-if="session.sendError" class="pi-composer-error" role="alert">{{ session.sendError }}</p>
        <form ref="composer" @submit.prevent="send">
          <InputGroup class="composer pi-composer">
            <InputGroupTextarea id="pi-workspace-draft" v-model="session.draft" rows="2" aria-label="会话草稿" placeholder="继续讨论活动报名规则…" />
            <InputGroupAddon align="block-end" class="composer-tools pi-composer-tools">
              <span><BookOpen :size="14" aria-hidden="true" />{{ host === 'grok' ? '只读工程资料' : '会话草稿' }}</span>
              <Button type="submit" size="icon" class="send-button pi-send" :disabled="Boolean(session.receipt) || !session.draft.trim()" aria-label="发送" title="发送"><ArrowUp :size="19" aria-hidden="true" /></Button>
            </InputGroupAddon>
          </InputGroup>
        </form>
        <div class="composer-note pi-round-note" role="status" aria-live="polite"><Check v-if="notice" :size="13" aria-hidden="true" />{{ notice || '本地模拟会话' }}</div>
      </div>
    </main>
  </div>
</template>

<style scoped>
.receipt-notice { padding: 8px 0; color: var(--foreground); }
.receipt-actions { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 4px; }
</style>
