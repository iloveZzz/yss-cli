<script setup lang="ts">
import { ArrowDown, Copy, Pause } from '@lucide/vue'
import { computed, nextTick, onMounted, shallowRef, useId, useTemplateRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { jobStatusLabels, type JobActivity } from '@/services/workbench-platform'
import { activityFailed, commandFailureResult } from '../schemas/job'

const props = defineProps<{ row: JobActivity; terminal?: boolean; followInitially: boolean }>()
const output = computed(() => props.row.output ?? '')
const running = computed(() => props.row.status === 'inProgress' && !props.terminal)
const following = shallowRef(props.followInitially)
const viewport = useTemplateRef<HTMLPreElement>('viewport')
const outputId = useId()
const copyNotice = shallowRef('')
const exitCode = computed(() => {
  const code = props.row.exitCode
  return code == null || code === -1 ? '' : `退出码 ${code}`
})
const termination = computed(() => props.row.terminationSignal || props.row.exitCode === -1
  ? commandFailureResult(props.row) : '')
const state = computed(() => {
  if (props.row.status === 'inProgress') return running.value ? '执行中' : '已中断'
  if (props.row.status === 'canceled' || props.row.status === 'cancelled') return '已停止'
  if (activityFailed(props.row)) return '失败'
  return jobStatusLabels[props.row.status] ?? '已完成'
})

function scrollToLatest() {
  const node = viewport.value
  if (following.value && node) node.scrollTop = node.scrollHeight
}

function pauseIfScrolledUp() {
  const node = viewport.value
  if (following.value && node && node.scrollHeight - node.clientHeight - node.scrollTop > 2) {
    following.value = false
  }
}

function toggleFollowing() {
  following.value = !(running.value && following.value)
  scrollToLatest()
}

onMounted(scrollToLatest)
watch(output, () => {
  // Check the old viewport too, in case a new response arrives before its scroll event.
  pauseIfScrolledUp()
  void nextTick(scrollToLatest)
})

async function copyOutput() {
  const retained = output.value
  if (!retained) return
  copyNotice.value = ''
  try {
    await navigator.clipboard.writeText(retained)
    copyNotice.value = '已复制命令输出'
  } catch {
    copyNotice.value = '无法复制，可选择日志文本后手动复制。'
  }
}
</script>

<template>
  <section class="command-terminal" aria-label="命令终端">
    <div class="terminal-bar">
      <div class="terminal-summary">
        <strong>命令输出</strong>
        <span class="terminal-state" role="status">
          {{ state }}<template v-if="exitCode"> · {{ exitCode }}</template><template v-if="termination"> · {{ termination }}</template>
        </span>
      </div>
      <div class="terminal-actions">
        <Button
          type="button"
          size="sm"
          variant="ghost"
          :aria-controls="outputId"
          :aria-disabled="!running && !output"
          @click="(running || output) && toggleFollowing()"
        >
          <Pause v-if="running && following" aria-hidden="true" />
          <ArrowDown v-else aria-hidden="true" />
          {{ running && following ? '暂停跟随' : '回到最新' }}
        </Button>
        <Button type="button" size="sm" variant="ghost" :disabled="!output" @click="copyOutput">
          <Copy aria-hidden="true" />
          复制输出
        </Button>
      </div>
    </div>
    <pre :id="outputId" ref="viewport" class="terminal-output" role="region" tabindex="0"
      aria-label="命令输出" @scroll.passive="pauseIfScrolledUp"
    >{{ output || (running ? '等待命令输出…' : '暂无已留存的输出') }}</pre>
    <p class="copy-notice" role="status">{{ copyNotice }}</p>
  </section>
</template>

<style scoped>
.command-terminal {
  min-inline-size: 0;
  margin-block-start: 8px;
}
.terminal-bar,
.terminal-summary,
.terminal-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 8px;
}
.terminal-bar {
  justify-content: space-between;
  padding-block-end: 4px;
}
.terminal-state,
.copy-notice {
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.terminal-output {
  max-block-size: 24rem;
  margin: 0;
  padding: 10px 12px;
  overflow: auto;
  overflow-anchor: none;
  overflow-wrap: anywhere;
  overscroll-behavior-y: contain;
  border-radius: 7px;
  background: #242526;
  color: #f0f0f0;
  color-scheme: dark;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  white-space: pre-wrap;
}
.terminal-output:focus-visible {
  outline: 2px solid var(--ring);
  outline-offset: 2px;
}
.copy-notice:not(:empty) {
  margin-block-start: 4px;
}
@media (width < 720px) {
  .terminal-actions button { min-block-size: 44px; }
}
@media (forced-colors: active) {
  .terminal-output { border: 1px solid CanvasText; }
  .terminal-output:focus-visible { outline-color: Highlight; }
}
</style>
