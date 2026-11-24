<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { Badge } from '@/components/ui/badge'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import {
  Trace,
  TraceCollapsible,
  TraceCollapsibleContent,
  TraceCollapsibleTrigger,
  TraceIcon,
  TraceLine,
  TraceTitle
} from '@/components/ui/trace'
import type { JobActivity } from '@/services/workbench-platform'
import ActivityDetail from './ActivityDetail.vue'

const props = defineProps<{ jobId: string; row: JobActivity; terminal: boolean }>()
const expanded = shallowRef(false)
const operations: Record<string, string> = {
  read: '读取文件',
  ls: '列出目录',
  grep: '搜索文件',
  write: '写入文件',
  edit: '编辑文件'
}
const command = computed(() => props.row.kind === 'command')
const finished = computed(() => props.row.status !== 'inProgress')
const state = computed(() => {
  if (!finished.value) return props.terminal ? '已中断' : '执行中'
  if (props.row.status === 'canceled') return '已停止'
  return props.row.status === 'failed' || (props.row.exitCode ?? 0) !== 0 ? '失败' : '已完成'
})
const label = computed(() =>
  command.value ? '执行命令' : operations[props.row.operation ?? ''] ?? '文件操作'
)
const detail = computed(() =>
  props.row.path ?? (props.row.error && props.row.operation ? '' : props.row.text)
)
</script>

<template>
  <Trace class="conversation-tool" :data-failed="state === '失败' || undefined">
    <TraceCollapsible v-if="command && finished" v-model:open="expanded">
      <TraceCollapsibleTrigger class="conversation-tool-trigger">
        <TraceLine class="tool-line">
          <TraceIcon><span class="tool-mark" aria-hidden="true" /></TraceIcon>
          <TraceTitle>{{ label }}</TraceTitle>
          <Badge variant="secondary" class="tool-state">{{ state }}</Badge>
          <code class="tool-detail">{{ detail }}</code>
        </TraceLine>
      </TraceCollapsibleTrigger>
      <TraceCollapsibleContent v-if="expanded" class="tool-output">
        <ActivityDetail :job-id="jobId" :row="row" command-output />
      </TraceCollapsibleContent>
    </TraceCollapsible>
    <div v-else>
      <TraceLine class="tool-line">
        <TraceIcon><span class="tool-mark" aria-hidden="true" /></TraceIcon>
        <TraceTitle>{{ label }}</TraceTitle>
        <Badge variant="secondary" class="tool-state">
          <LoadingIndicator :active="!finished && !terminal" />
          {{ state }}
        </Badge>
        <code v-if="detail" class="tool-detail">{{ detail }}</code>
      </TraceLine>
      <p v-if="row.error" class="tool-error">{{ row.error }}</p>
    </div>
  </Trace>
</template>

<style scoped>
.conversation-tool {
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.tool-line {
  display: flex;
  flex-wrap: wrap;
  gap: 5px 8px;
  align-items: baseline;
  min-width: 0;
  padding-block: 3px;
}
.tool-mark {
  display: inline-block;
  width: 6px;
  height: 6px;
  margin-top: 2px;
  border-radius: 50%;
  background: currentColor;
}
.tool-detail {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  min-width: 0;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}
.tool-state {
  font-size: 11px;
  padding-inline: 6px;
  border-radius: 5px;
  background: var(--brand-color-bg-layout);
}
.conversation-tool[data-failed] .tool-state, .tool-error {
  color: var(--brand-color-error-active);
}
.tool-error {
  padding: 2px 24px;
  overflow-wrap: anywhere;
}
.tool-output {
  max-width: 760px;
}
@media (pointer: coarse) {
  .conversation-tool-trigger {
    min-height: 44px;
  }
}
</style>
