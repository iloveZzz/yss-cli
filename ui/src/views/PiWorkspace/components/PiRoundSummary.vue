<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { useIntervalFn } from '@vueuse/core'
import { ChevronRight } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { useMessageScrollerContext } from '@/components/ui/message-scroller/useMessageScroller'
import type { ActivityPage, PiPlanStep } from '@/services/workbench-platform'
import { activityLabels } from '@/views/Jobs/schemas/job'

const props = defineProps<{
  jobId: string
  status: string
  startedAt?: string
  terminal: boolean
  summary?: ActivityPage['piRoundSummary']
  plan?: PiPlanStep[] | null
  canReview: boolean
}>()
const emit = defineEmits<{ review: [] }>()
const { userScrollIntent } = useMessageScrollerContext()

const started = computed(() => Date.parse(props.startedAt ?? ''))
const now = shallowRef(Date.now())
const { pause, resume } = useIntervalFn(() => { now.value = Date.now() }, 1000, { immediate: false })
watch(() => !props.terminal && Number.isFinite(started.value), (running) => {
  if (running) {
    now.value = Date.now()
    resume()
  } else pause()
}, { immediate: true })
const elapsed = computed(() => {
  const end = props.terminal ? Date.parse(props.summary?.finishedAt ?? '') : now.value
  const seconds = Math.floor((end - started.value) / 1000)
  if (!Number.isFinite(seconds) || seconds < 0) return ''
  if (seconds < 60) return `${seconds} 秒`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`
  return `${Math.floor(seconds / 3600)} 小时 ${Math.floor(seconds / 60) % 60} 分`
})
const done = computed(() => props.plan?.filter((step) => step.status === 'completed').length ?? 0)
const latestFailure = computed(() => props.summary?.recentFailures[0])
function logLink(activity?: number) {
  return { path: '/jobs/' + props.jobId, query: { detail: '1', tab: 'log', activity: activity?.toString() } }
}
</script>

<template>
  <Collapsible class="pi-round-summary" @update:open="userScrollIntent">
    <div class="summary-header">
      <CollapsibleTrigger as-child>
        <Button type="button" size="sm" variant="ghost" class="summary-trigger" data-pi-round-summary-trigger>
          <ChevronRight class="summary-chevron" :size="14" aria-hidden="true" />
          <span>执行摘要</span>
          <span class="summary-facts">
            {{ status }}<template v-if="elapsed"> · {{ elapsed }}</template><template v-if="summary">
              · <span :class="{ 'summary-failed': summary.failedCount > 0 }">{{ summary.failedCount }} 项失败</span>
            </template>
          </span>
        </Button>
      </CollapsibleTrigger>
      <Button v-if="latestFailure" as-child size="sm" variant="link" class="summary-link">
        <RouterLink :to="logLink(latestFailure.id)">查看最近失败</RouterLink>
      </Button>
    </div>
    <CollapsibleContent>
      <div class="summary-detail">
        <p v-if="plan?.length" class="summary-facts">计划完成 {{ done }} / {{ plan.length }} 项</p>
        <ul v-if="latestFailure" class="summary-failures" aria-label="最近失败">
          <li v-for="failure in summary?.recentFailures" :key="failure.id">
            <RouterLink :to="logLink(failure.id)">{{ failure.text || activityLabels[failure.kind] }}</RouterLink>
            <p v-if="failure.exitCode != null || failure.diagnostic" class="summary-facts">
              <template v-if="failure.exitCode != null && failure.exitCode !== -1">退出码 {{ failure.exitCode }}</template>
              <template v-if="failure.exitCode === -1">异常终止</template>
              <template v-if="failure.diagnostic">{{ failure.exitCode != null ? ' · ' : '' }}{{ failure.diagnostic }}</template>
            </p>
          </li>
        </ul>
        <div class="summary-actions">
          <Button as-child size="sm" variant="ghost" class="summary-link">
            <RouterLink :to="logLink()">查看本轮日志</RouterLink>
          </Button>
          <Button v-if="canReview" type="button" size="sm" variant="ghost" class="summary-link" @click="emit('review')">
            查看本轮成果
          </Button>
        </div>
      </div>
    </CollapsibleContent>
  </Collapsible>
</template>

<style scoped>
.summary-header, .summary-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 3px 8px; }
.summary-trigger {
  justify-content: flex-start; height: auto; min-height: 34px; min-width: 0;
  padding-inline: 6px; white-space: normal; text-align: start; font-size: 13px;
}
.summary-facts { color: var(--quiet); font-size: 12px; font-variant-numeric: tabular-nums; }
.summary-failed { color: var(--destructive); }
.summary-trigger[data-state='open'] .summary-chevron { transform: rotate(90deg); }
.summary-detail { display: grid; gap: 6px; padding: 3px 0 4px 12px; border-inline-start: 1px solid var(--border); }
.summary-failures { display: grid; gap: 6px; margin: 0; padding: 0; }
.summary-failures li { min-width: 0; overflow-wrap: anywhere; }
.summary-failures a { color: var(--primary); font-size: 13px; text-decoration: underline; text-underline-offset: 3px; }
.summary-link { height: auto; min-height: 34px; padding-inline: 6px; font-size: 12px; }
@media (width < 720px), (pointer: coarse) {
  .summary-trigger, .summary-link { min-height: 44px; }
  .summary-failures a { display: flex; align-items: center; min-height: 44px; }
}
</style>
