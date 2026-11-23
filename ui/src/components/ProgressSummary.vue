<script setup lang="ts">
import { computed } from 'vue'
import StatusBadge from './StatusBadge.vue'
import {
  dateLabel,
  stages,
  attentionLabels,
  type ProgressSummary
} from '@/services/workbench'
const props = defineProps<{ progress?: ProgressSummary; compact?: boolean }>()
const title = computed(() => {
  const progress = props.progress
  if (!progress) return '目标与进展待核实'
  return progress.status === 'done'
    ? progress.lastDelivery ? `已验收 · ${dateLabel(progress.lastDelivery.recordedAt)}` : '目标已完成'
    : `${stages[progress.primaryStage]} · ${progress.summary || '进展待核实'}`
})
const active = computed(() =>
  props.progress?.activeStages.map((stage) => stages[stage]).join('、')
)
</script>
<template>
  <section
    class="progress-summary"
    :class="{ compact }"
    aria-label="自动进展摘要"
  >
    <div class="summary-main">
      <div class="summary-heading">
        <span v-if="progress" class="summary-status">
          <span class="sr-only">有效进度</span>
          <StatusBadge :status="progress.status" />
        </span>
        <strong>{{ title }}</strong>
        <span v-if="progress?.hasOverride" class="summary-tag">人工更正</span>
      </div>
      <p v-if="!compact && active" class="summary-active">
        当前并行 · {{ active }}
      </p>
      <div v-if="progress?.attention.length" class="summary-attention">
        <span v-for="item in progress.attention" :key="item">
          {{ attentionLabels[item] ?? item }}
        </span>
      </div>
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
  </section>
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
.summary-tag,
.summary-attention span {
  font-size: 11px;
  color: #9b5a0e;
}
.summary-active {
  font-size: 12px;
  color: #717780;
  margin-top: 5px;
}
.summary-attention {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 12px;
  margin-top: 6px;
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
  color: #858d99;
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
  color: #555d6a;
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
