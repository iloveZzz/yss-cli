<script setup lang="ts">
import { computed } from 'vue'
import { Circle, CircleCheck, CircleDot, ListChecks } from '@lucide/vue'
import { Trace, TraceCollapsible, TraceCollapsibleContent, TraceCollapsibleTrigger,
  TraceIcon, TraceLine, TraceTitle } from '@/components/ui/trace'
import type { PiPlanStep } from '@/services/workbench-platform'

const props = defineProps<{ steps: PiPlanStep[]; terminal: boolean }>()
const done = computed(() => props.steps.filter((step) => step.status === 'completed').length)
const current = computed(() => {
  if (props.terminal) return
  return props.steps.find((step) => step.status === 'inProgress') ?? props.steps.find((step) => step.status === 'pending')
})
</script>

<template>
  <Trace class="pi-live-plan" aria-label="本轮执行计划">
    <TraceCollapsible>
      <TraceCollapsibleTrigger class="pi-plan-trigger">
        <span class="pi-plan-heading" data-pi-plan-summary>
          <TraceLine>
            <TraceIcon><ListChecks :size="15" aria-hidden="true" /></TraceIcon>
            <TraceTitle>执行计划</TraceTitle>
            <span class="pi-plan-summary">{{ done }} / {{ steps.length }} 已完成</span>
          </TraceLine>
          <span v-if="current" class="pi-plan-current">
            <span class="pi-plan-current-label">{{ current.status === 'inProgress' ? '正在处理' : '下一步' }}</span>
            <span>{{ current.title }}</span>
          </span>
        </span>
      </TraceCollapsibleTrigger>
      <TraceCollapsibleContent>
        <ol class="pi-plan-steps">
          <li v-for="(step, index) in steps" :key="index">
            <TraceLine class="pi-plan-step" :status="step.status === 'inProgress' && !terminal ? 'running' : undefined">
              <TraceIcon>
                <CircleCheck v-if="step.status === 'completed'" :size="14" aria-hidden="true" />
                <CircleDot v-else-if="step.status === 'inProgress' && !terminal" :size="14" aria-hidden="true" />
                <Circle v-else :size="14" aria-hidden="true" />
              </TraceIcon>
              <TraceTitle>{{ step.title }}</TraceTitle>
              <span class="pi-plan-summary">{{ step.status === 'completed' ? '已完成' : terminal ? '未完成' : step.status === 'inProgress' ? '正在处理' : '待处理' }}</span>
            </TraceLine>
          </li>
        </ol>
      </TraceCollapsibleContent>
    </TraceCollapsible>
  </Trace>
</template>

<style scoped>
.pi-live-plan { padding-block: 3px; }
.pi-plan-heading { display: grid; gap: 3px; min-width: 0; }
.pi-plan-current { display: flex; align-items: baseline; gap: 7px; margin-inline-start: 23px; font-size: 12px; color: var(--foreground); overflow-wrap: anywhere; }
.pi-plan-current-label, .pi-plan-summary { flex: none; color: var(--brand-color-text-secondary); font-size: 12px; }
.pi-plan-summary { font-variant-numeric: tabular-nums; }
.pi-plan-steps { display: grid; gap: 3px; }
.pi-plan-step { align-items: baseline; }
.pi-plan-step :deep([data-slot='trace-title']) { white-space: normal; overflow-wrap: anywhere; }
@media (pointer: coarse) {
  .pi-plan-trigger { min-height: 44px; }
}
</style>
