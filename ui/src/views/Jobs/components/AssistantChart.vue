<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { computed, defineAsyncComponent, onErrorCaptured, ref, useId, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Table, TableBody, TableCaption, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import AssistantCodeBlock from './AssistantCodeBlock.vue'
import { parseAssistantChart } from './assistantChartData'

const props = defineProps<{ code: string }>()

const AssistantChartPlot = defineAsyncComponent({ loader: () => import('./AssistantChartPlot.vue'), timeout: 15_000 })
const chart = computed(() => parseAssistantChart(props.code))
const titleId = useId()
const plotSupported = computed(() => {
  if (!chart.value) return false
  const values = chart.value.data.map(point => point.value)
  if (chart.value.type === 'bar') values.push(0)
  const minimum = Math.min(...values)
  const maximum = Math.max(...values)
  const magnitude = Math.max(Math.abs(minimum), Math.abs(maximum))
  const span = maximum - minimum
  // D3 derives ticks with powers of ten and can overflow even when the inputs are finite.
  return magnitude === 0 || (magnitude <= 1e300 && magnitude >= 1e-298 && (span === 0 || span >= 1e-300))
})
const plotFailed = ref(false)
const tableOpen = ref(!plotSupported.value)

watch(chart, () => {
  plotFailed.value = false
  tableOpen.value = !plotSupported.value
})
onErrorCaptured(() => {
  plotFailed.value = true
  tableOpen.value = true
  return false
})
</script>

<template>
  <Collapsible v-if="chart" v-model:open="tableOpen" as-child>
    <figure class="assistant-chart" :aria-labelledby="titleId">
      <figcaption class="assistant-chart-header">
        <div :id="titleId" class="assistant-chart-caption">
          <span class="assistant-chart-title">{{ chart.title }}</span>
          <span v-if="chart.unit" class="assistant-chart-unit">（{{ chart.unit }}）</span>
        </div>
        <CollapsibleTrigger as-child>
          <Button type="button" variant="ghost" size="sm" class="assistant-chart-toggle"
            :aria-label="`数据表 · ${chart.title}`"
          >
            数据表
            <ChevronDown aria-hidden="true" class="size-4" />
          </Button>
        </CollapsibleTrigger>
      </figcaption>
      <div v-if="plotSupported && !plotFailed" class="assistant-chart-area">
        <AssistantChartPlot :chart="chart" />
      </div>
      <p v-else-if="!plotSupported" class="assistant-chart-status" role="status">当前数值范围无法绘图，可查看数据表。</p>
      <p v-else class="assistant-chart-status" role="status">图表加载失败，可查看数据表。</p>
      <CollapsibleContent class="assistant-chart-details">
        <Table scrollable :scroll-label="`${chart.title}的数据表`" class="assistant-chart-table">
          <TableCaption class="sr-only">{{ chart.title }}</TableCaption>
          <TableHeader>
            <TableRow>
              <TableHead scope="col">项目</TableHead>
              <TableHead scope="col" class="text-right">数值<span v-if="chart.unit">（{{ chart.unit }}）</span></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="(point, index) in chart.data" :key="index">
              <th scope="row" class="assistant-chart-label">{{ point.label }}</th>
              <TableCell class="text-right tabular-nums">{{ point.value }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </CollapsibleContent>
    </figure>
  </Collapsible>
  <AssistantCodeBlock v-else :code="code" language="chart" />
</template>

<style scoped>
.assistant-chart {
  --assistant-chart-plot-height: 188px;
  min-width: 0;
  margin: 0;
  padding: 0.625rem 0.875rem 0.75rem;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  background: var(--color-background);
}
.assistant-chart-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 0.5rem;
}
.assistant-chart-caption {
  align-self: center;
  min-width: 0;
  overflow-wrap: anywhere;
}
.assistant-chart-title {
  font-size: 15px;
  font-weight: 650;
}
.assistant-chart-unit,
.assistant-chart-status {
  color: var(--color-muted-foreground);
  font-size: 12px;
}
.assistant-chart-toggle {
  min-height: 34px;
  color: var(--color-muted-foreground);
}
.assistant-chart-toggle:hover,
.assistant-chart-toggle[data-state='open'] {
  color: var(--color-foreground);
}
.assistant-chart-toggle[data-state='open'] svg {
  rotate: 180deg;
}
.assistant-chart-area {
  min-height: var(--assistant-chart-plot-height);
  margin-block-start: 0.25rem;
}
.assistant-chart-status {
  margin: 0.375rem 0 0;
}
.assistant-chart-details[data-state='open'] {
  margin-block-start: 0.5rem;
  border-block-start: 1px solid var(--color-border);
}
.assistant-chart-details :deep(.table-scroll) {
  max-height: 20rem;
}
.assistant-chart-table {
  font-size: 13px;
}
.assistant-chart-table :deep(thead) {
  position: sticky;
  inset-block-start: 0;
  background: var(--color-background);
}
.assistant-chart-table :deep(th) {
  white-space: normal;
  overflow-wrap: anywhere;
}
.assistant-chart-label {
  padding: 0.5rem;
  text-align: start;
  font-weight: 400;
  color: var(--color-foreground);
}
@media (max-width: 720px), (any-pointer: coarse) {
  .assistant-chart-toggle {
    min-height: 44px;
  }
}
@media (max-width: 720px) {
  .assistant-chart {
    --assistant-chart-plot-height: 172px;
    padding-inline: 0.625rem;
  }
}
</style>
