<script setup lang="ts">
import { computed } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import StatusBadge from './StatusBadge.vue'
import {
  dateLabel,
  stages,
  attentionLabels,
  type ProgressSummary
} from '@/services/workbench'
import type { CompactProgress } from '@/services/workbench-read'
const props = defineProps<{
  progress?: CompactProgress & Pick<ProgressSummary, 'completion'>
  compact?: boolean
}>()
const completionTitle = '总体进度'
const title = computed(() => {
  const progress = props.progress
  if (!progress) return '目标与进展待核实'
  return progress.status === 'done'
    ? progress.lastDelivery ? `目标已完成 · ${dateLabel(progress.lastDelivery.recordedAt)}` : '目标已完成'
    : `${stages[progress.primaryStage]} · ${progress.summary || '进展待核实'}`
})
const completion = computed(() => {
  const completion = props.progress?.completion
  return completion && completion.total > 0 ? completion : undefined
})
const completionPercent = computed(() => {
  const count = completion.value
  return count ? (count.completed / count.total) * 100 : null
})
const completionLabel = computed(() => {
  const count = completion.value
  if (!count) return '待核实'
  return props.progress?.status === 'done' ? '已验收 · 100%' : `已核实 ${count.completed} / ${count.total} 项`
})
</script>
<template>
  <Card
    class="progress-summary block gap-0 shadow-none"
    :class="{ compact }"
    role="region"
    aria-label="自动进展摘要"
  >
    <div class="summary-main">
      <div class="summary-heading">
        <span v-if="progress" class="summary-status">
          <span class="sr-only">有效进度</span>
          <StatusBadge :status="progress.status" />
        </span>
        <strong>{{ title }}</strong>
        <Badge v-if="progress?.hasOverride" variant="outline" class="summary-tag">人工更正</Badge>
      </div>
      <div v-if="progress?.attention.length" class="summary-attention">
        <Badge v-for="item in progress.attention" :key="item" variant="outline" class="summary-tag">
          {{ attentionLabels[item] ?? item }}
        </Badge>
      </div>
    </div>
    <div v-if="!compact" class="summary-progress">
      <div class="summary-progress-label">
        <span>{{ completionTitle }}</span>
        <span>{{ completionLabel }}</span>
      </div>
      <Progress
        :model-value="completionPercent"
        :get-value-label="() => completionTitle"
        :get-value-text="() => completionLabel"
        class="summary-progress-track"
      />
    </div>
    <dl class="summary-facts">
      <div v-if="!compact || progress?.blocker">
        <dt>阻塞</dt>
        <dd>{{ progress?.blocker || '尚无已核实阻塞' }}</dd>
      </div>
      <div>
        <dt>下一步</dt>
        <dd>
          {{
            progress?.nextAction ||
            (progress?.status === 'done' ? '—' : '补充本次目标与对应凭证')
          }}
        </dd>
      </div>
      <div v-if="!compact">
        <dt>最近核实</dt>
        <dd>
          {{
            progress?.lastVerifiedAt
              ? dateLabel(progress.lastVerifiedAt)
              : '尚未核实'
          }}
        </dd>
      </div>
    </dl>
  </Card>
</template>
<style scoped>
.progress-summary {
  min-width: 0;
  padding: 18px 20px;
  border: 1px solid var(--yss-colors-border);
  border-radius: 8px;
  background: #fff;
  margin-bottom: 20px;
}
.summary-heading {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 8px;
}
.summary-heading strong {
  font-size: 15px;
  font-weight: 600;
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.summary-status {
  display: inline-flex;
  align-items: center;
}
.summary-tag {
  font-size: 11px;
  color: #9b5a0e;
}
.summary-attention {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 12px;
  margin-top: 6px;
}
.summary-progress {
  display: grid;
  gap: 6px;
  margin-top: 14px;
}
.summary-progress-label {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 4px 12px;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.summary-progress-label > :first-child {
  font-weight: 600;
  color: var(--brand-color-text);
}
.summary-progress-track[data-state='indeterminate'] :deep([data-slot='progress-indicator']) {
  display: none;
}
@media (forced-colors: active) {
  .summary-progress-track {
    outline: 1px solid CanvasText;
    outline-offset: -1px;
  }
  .summary-progress-track :deep([data-slot='progress-indicator']) {
    forced-color-adjust: none;
    background-color: Highlight;
  }
}
.summary-facts {
  display: grid;
  grid-template-columns: 1fr 1.6fr auto;
  gap: 12px 24px;
  margin: 16px 0 0;
}
.summary-facts > div {
  min-width: 0;
}
dt {
  font-size: 11px;
  color: var(--brand-color-text-secondary);
  margin-bottom: 4px;
}
dd {
  margin: 0;
  font-size: 12px;
  line-height: 1.7;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.compact {
  border: 0;
  padding: 0;
  margin: 8px 0 0;
  background: transparent;
}
.compact .summary-heading strong {
  font-size: 12px;
  font-weight: 400;
  color: var(--brand-color-text-secondary);
}
.compact .summary-facts {
  display: block;
  margin-top: 6px;
}
.compact .summary-facts > div {
  margin-top: 5px;
}
.compact dt {
  display: inline;
  margin-right: 6px;
}
.compact dd {
  display: inline;
}
@media (width < 720px) {
  .summary-facts {
    grid-template-columns: 1fr;
    gap: 12px;
  }
  .progress-summary {
    padding: 14px;
  }
  .compact {
    padding: 0;
  }
}
</style>
