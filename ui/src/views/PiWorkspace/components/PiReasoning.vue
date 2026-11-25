<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { Brain } from '@lucide/vue'
import { Trace, TraceCollapsible, TraceCollapsibleContent, TraceCollapsibleTrigger,
  TraceIcon, TraceLine, TraceTitle } from '@/components/ui/trace'
import type { JobActivity } from '@/services/workbench-platform'

const props = defineProps<{ row: JobActivity; terminal: boolean }>()
const open = shallowRef<boolean>()
const running = computed(() => props.row.status === 'inProgress' && !props.terminal)
const title = computed(() => {
  if (running.value) return 'Pi 正在推理'
  if (['inProgress', 'canceled', 'failed'].includes(props.row.status)) return '推理已中断'
  if (!props.row.text) return '推理完成 · 无可展示内容'
  const ms = props.row.durationMs
  return ms === undefined ? '推理完成' : ms < 1000 ? '推理不到 1 秒' : `推理 ${Math.round(ms / 1000)} 秒`
})
</script>

<template>
  <Trace class="pi-reasoning">
    <TraceCollapsible v-if="row.text" :open="open ?? running" @update:open="open = $event">
      <TraceCollapsibleTrigger>
        <TraceLine :status="running ? 'running' : undefined">
          <TraceIcon><Brain :size="15" aria-hidden="true" /></TraceIcon>
          <TraceTitle>{{ title }}</TraceTitle>
        </TraceLine>
      </TraceCollapsibleTrigger>
      <TraceCollapsibleContent class="pi-reasoning-text">{{ row.text }}</TraceCollapsibleContent>
    </TraceCollapsible>
    <TraceLine v-else :status="running ? 'running' : undefined">
      <TraceIcon><Brain :size="15" aria-hidden="true" /></TraceIcon>
      <TraceTitle>{{ title }}</TraceTitle>
    </TraceLine>
  </Trace>
</template>

<style scoped>
.pi-reasoning-text { padding: 4px 8px 5px 23px; color: var(--brand-color-text-secondary); white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
