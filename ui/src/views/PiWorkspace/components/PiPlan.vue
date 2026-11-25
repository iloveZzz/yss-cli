<script setup lang="ts">
import { computed } from 'vue'
import { Circle, CircleCheck, CircleDot, ListChecks } from '@lucide/vue'
import { Trace, TraceCollapsible, TraceCollapsibleContent, TraceCollapsibleTrigger,
  TraceIcon, TraceLine, TraceTitle } from '@/components/ui/trace'
import type { PiPlanStep } from '@/services/workbench-platform'

const props = defineProps<{ steps: PiPlanStep[]; terminal: boolean }>()
const done = computed(() => props.steps.filter((step) => step.status === 'completed').length)
</script>

<template>
  <Trace class="pi-live-plan" aria-label="本轮执行计划">
    <TraceCollapsible>
      <TraceCollapsibleTrigger>
        <TraceLine>
          <TraceIcon><ListChecks :size="15" /></TraceIcon>
          <TraceTitle>执行计划</TraceTitle>
          <span class="pi-plan-summary">{{ done }} / {{ steps.length }} 已完成</span>
        </TraceLine>
      </TraceCollapsibleTrigger>
      <TraceCollapsibleContent>
        <ol class="pi-plan-steps">
          <li v-for="(step, index) in steps" :key="index">
            <TraceLine :status="step.status === 'inProgress' && !terminal ? 'running' : step.status === 'completed' ? undefined : 'pending'">
              <TraceIcon>
                <CircleCheck v-if="step.status === 'completed'" :size="14" />
                <CircleDot v-else-if="step.status === 'inProgress' && !terminal" :size="14" />
                <Circle v-else :size="14" />
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
.pi-plan-steps { padding-inline-start: 23px; overflow-wrap: anywhere; }
.pi-plan-summary { margin-inline-start: 7px; color: var(--brand-color-text-secondary); font-size: 12px; }
</style>
