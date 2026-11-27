<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Maximize2, Minimize2, Minus } from '@lucide/vue'
import { useTerminalPanel } from './useTerminalPanel'
import { useTerminalSessions } from './useTerminalSessions'

const {
  layout: panelLayout, visible: panelVisible, canResize, actionPending: panelActionPending, error: panelError,
  dragging, maximizeLabel, changeLayout, startDrag, moveDrag, finishDrag, resizeWithKey,
} = useTerminalPanel()

const stage = ref<HTMLElement | null>(null)
const projectPicker = ref<HTMLDetailsElement | null>(null)
const hosts = new Map<string, HTMLElement>()
const isMac = navigator.platform.includes('Mac')
let disposed = false
let frame = 0
let resizeObserver: ResizeObserver | undefined
let composing: EventTarget | null = null

const {
  projects, tabs, active, loaded, contextError, refresh, retry, selectProject, start, close, fitActive, focusActive,
} = useTerminalSessions((projectId) => hosts.get(projectId))
const currentProjectId = computed(() => active.value?.id ?? '')

function tabId(projectId: string) {
  return `terminal-tab-${encodeURIComponent(projectId)}`
}

function rememberHost(projectId: string, element: unknown) {
  if (element instanceof HTMLElement) hosts.set(projectId, element)
  else hosts.delete(projectId)
}

function requestFit() {
  if (frame || disposed || !panelVisible.value) return
  frame = window.requestAnimationFrame(() => {
    frame = 0
    if (panelVisible.value) fitActive()
  })
}

async function startSession() {
  const projectId = currentProjectId.value
  if (!await start()) return
  await nextTick()
  if (currentProjectId.value !== projectId || !panelVisible.value || disposed) return
  focusActive()
  requestFit()
}

async function closeSession() {
  const projectId = currentProjectId.value
  if (await close() && currentProjectId.value === projectId && panelVisible.value && !disposed) {
    document.getElementById(tabId(projectId))?.focus()
  }
}

function onTabKey(event: KeyboardEvent) {
  const ordered = tabs.value
  const index = ordered.findIndex((tab) => tab.id === currentProjectId.value)
  if (index < 0 || event.altKey || event.ctrlKey || event.metaKey || event.isComposing) return
  const shortcuts: Record<string, number> = {
    ArrowLeft: (index + ordered.length - 1) % ordered.length,
    ArrowRight: (index + 1) % ordered.length,
    Home: 0,
    End: ordered.length - 1,
  }
  const next = shortcuts[event.key]
  if (next === undefined) return
  const tab = ordered[next]
  if (!tab) return
  event.preventDefault()
  selectProject(tab.id)
  document.getElementById(tabId(tab.id))?.focus()
}

function onProjectShortcut(event: KeyboardEvent) {
  if (!isMac || !event.altKey || event.ctrlKey || event.metaKey || event.shiftKey
    || event.isComposing || composing || event.getModifierState('AltGraph')) return
  const target = event.target
  const fromTerminal = target instanceof Element && !!target.closest('.terminal-host')
  if (!fromTerminal && target instanceof HTMLElement
    && (target.isContentEditable || target.closest('input, textarea'))) return
  const digit = /^Digit([1-9])$/.exec(event.code)
  const project = digit && projects.value[Number(digit[1]) - 1]
  if (!project) return
  event.preventDefault()
  event.stopPropagation()
  selectProject(project.id)
  if (projectPicker.value) projectPicker.value.open = false
  void nextTick(() => {
    if (currentProjectId.value !== project.id) return
    if (!fromTerminal || !focusActive()) document.getElementById(tabId(project.id))?.focus()
  })
}

function closeProjectPicker(focusSummary = false) {
  const picker = projectPicker.value
  if (!picker?.open) return
  picker.open = false
  if (focusSummary) picker.querySelector('summary')?.focus()
}

function pickProject(projectId: string) {
  selectProject(projectId)
  closeProjectPicker(true)
}

function dismissProjectPicker(event: PointerEvent) {
  if (event.target instanceof Node && !projectPicker.value?.contains(event.target)) closeProjectPicker()
}

function startComposition(event: Event) {
  composing = event.target
}

function endComposition(event: Event) {
  if (event.target === composing) composing = null
}

watch(currentProjectId, () => {
  if (currentProjectId.value) {
    document.getElementById(tabId(currentProjectId.value))?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }
  requestFit()
}, { flush: 'post' })
watch(panelLayout, (next, previous) => {
  requestFit()
  if (previous?.mode === 'hidden' && next?.mode !== 'hidden') refresh()
}, { flush: 'post' })

onMounted(() => {
  window.addEventListener('keydown', onProjectShortcut, true)
  window.addEventListener('compositionstart', startComposition, true)
  window.addEventListener('compositionend', endComposition, true)
  window.addEventListener('focusout', endComposition, true)
  window.addEventListener('pointerdown', dismissProjectPicker, true)
  resizeObserver = new ResizeObserver(requestFit)
  if (stage.value) resizeObserver.observe(stage.value)
})

onBeforeUnmount(() => {
  disposed = true
  window.removeEventListener('keydown', onProjectShortcut, true)
  window.removeEventListener('compositionstart', startComposition, true)
  window.removeEventListener('compositionend', endComposition, true)
  window.removeEventListener('focusout', endComposition, true)
  window.removeEventListener('pointerdown', dismissProjectPicker, true)
  window.cancelAnimationFrame(frame)
  resizeObserver?.disconnect()
})
</script>

<template>
  <div class="terminal-shell" :data-dragging="dragging">
    <div
      v-if="panelLayout?.mode === 'split'"
      class="panel-divider"
      role="separator"
      tabindex="0"
      aria-label="终端面板宽度"
      aria-orientation="vertical"
      aria-controls="terminal-panel-content"
      :aria-disabled="!canResize"
      :aria-valuemin="Math.round((panelLayout.minRatio ?? 0) * 100)"
      :aria-valuemax="Math.round((panelLayout.maxRatio ?? 1) * 100)"
      :aria-valuenow="Math.round(panelLayout.ratio * 100)"
      title="拖动或用左右方向键调整终端宽度"
      @pointerdown="startDrag"
      @pointermove="moveDrag"
      @pointerup="finishDrag"
      @pointercancel="finishDrag"
      @lostpointercapture="finishDrag"
      @keydown="resizeWithKey"
    ></div>
    <main id="terminal-panel-content" class="terminal-app">
      <header class="toolbar">
        <h1>终端</h1>
        <div v-if="projects.length" class="picker">
          <span>项目</span>
          <details ref="projectPicker" @keydown.esc.stop.prevent="closeProjectPicker(true)">
            <summary :title="active?.name" :aria-label="`选择项目，当前为 ${active?.name ?? '未选择'}`">
              <span>{{ active?.name ?? '选择项目' }}</span>
            </summary>
            <ul class="project-options" aria-label="项目目录">
              <li v-for="project in projects" :key="project.id">
                <button
                  type="button"
                  :aria-current="project.id === currentProjectId ? 'true' : undefined"
                  :title="project.rootPath"
                  @click="pickProject(project.id)"
                >{{ project.name }}</button>
              </li>
            </ul>
          </details>
        </div>
        <span v-if="isMac && projects.length > 1" class="keyboard-hint" title="按 Option 加数字键切换项目">⌥ 1–9</span>
        <div class="panel-actions" aria-label="终端面板布局" role="group">
          <span :title="maximizeLabel">
            <button
              type="button"
              class="panel-button"
              :aria-label="maximizeLabel"
              :aria-disabled="!panelLayout || panelActionPending || panelLayout.temporary"
              @click="changeLayout('toggle-maximize')"
            >
              <Minimize2 v-if="panelLayout?.mode === 'maximized'" :size="16" aria-hidden="true" />
              <Maximize2 v-else :size="16" aria-hidden="true" />
            </button>
          </span>
          <button
            type="button"
            class="panel-button"
            title="最小化终端面板"
            aria-label="最小化终端面板"
            :disabled="panelActionPending"
            @click="changeLayout('hide')"
          >
            <Minus :size="16" aria-hidden="true" />
          </button>
        </div>
      </header>
      <div v-if="tabs.length" role="tablist" aria-label="项目终端" class="tablist" @keydown="onTabKey">
        <button
          v-for="tab in tabs"
          :id="tabId(tab.id)"
          :key="tab.id"
          type="button"
          role="tab"
          :aria-selected="tab.id === currentProjectId"
          :tabindex="tab.id === currentProjectId ? 0 : -1"
          class="tab"
          @click="selectProject(tab.id)"
        >{{ tab.name }}</button>
      </div>
      <div v-if="active?.path || active?.status || active?.state" class="session-bar">
        <div v-if="active?.path" class="startup-directory">
          <span>启动目录</span>
          <code :title="active.path">{{ active.path }}</code>
        </div>
        <div v-if="active?.status || active?.state" class="actions">
          <span v-if="active?.status" class="session-status" role="status">{{ active?.status }}</span>
          <button
            v-if="active?.state === 'failed' || active?.state === 'exited'"
            type="button"
            class="button primary"
            :disabled="!active?.canStart"
            @click="startSession"
          >重新启动</button>
          <button
            v-if="active?.state"
            type="button"
            class="button"
            :disabled="!active.canClose"
            @click="closeSession"
          >{{ active.state === 'cleanupPending' ? '重试关闭' : '关闭会话' }}</button>
        </div>
      </div>
      <div v-if="panelError" class="notice" role="alert">
        {{ panelError }}
        <button type="button" class="button" :disabled="panelActionPending" @click="changeLayout('read')">重新读取面板状态</button>
      </div>
      <div v-if="contextError" class="notice" role="alert">
        {{ contextError }}
        <button type="button" class="button" @click="retry">重试</button>
      </div>
      <div v-else-if="active?.error" class="notice" role="alert">
        {{ active.error }}
      </div>
      <div ref="stage" class="stage">
        <section
          v-for="tab in tabs"
          :key="tab.id"
          :hidden="tab.id !== currentProjectId"
          :aria-labelledby="tabId(tab.id)"
          role="tabpanel"
          class="session-panel"
        >
          <div
            v-show="tab.hasRenderer || tab.starting"
            :ref="element => rememberHost(tab.id, element)"
            class="terminal-host"
          ></div>
          <div v-if="!tab.hasRenderer" class="empty-state">
            <p v-if="tab.starting" role="status">正在启动终端…</p>
            <p v-else-if="tab.state === 'running'">终端连接已断开。关闭会话后可以重新打开。</p>
            <p v-else-if="tab.state === 'closing'">终端会话正在关闭。</p>
            <p v-else-if="tab.state === 'cleanupPending'">上次终端会话仍待清理。请重试关闭后再启动终端。</p>
            <p v-else-if="tab.state === 'failed' || tab.state === 'exited'">终端会话已结束。</p>
            <button v-else type="button" class="button primary" :disabled="!active?.canStart" @click="startSession">启动终端</button>
          </div>
        </section>
        <div v-if="!tabs.length" class="empty-state">
          <p v-if="!loaded && !contextError" role="status">正在读取项目…</p>
          <p v-else-if="projects.length">选择项目以启动终端。</p>
          <p v-else-if="loaded">当前没有可以打开终端的项目。</p>
        </div>
      </div>
    </main>
  </div>
</template>
