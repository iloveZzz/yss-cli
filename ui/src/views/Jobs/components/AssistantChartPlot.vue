<script setup lang="ts">
import { computed } from 'vue'
import { CurveType } from '@unovis/ts/types/curve'
import VisAxis from '@unovis/vue/components/axis'
import VisGroupedBar from '@unovis/vue/components/grouped-bar'
import VisLine from '@unovis/vue/components/line'
import VisScatter from '@unovis/vue/components/scatter'
import VisXYContainer from '@unovis/vue/containers/xy-container'
import type { AssistantChartData } from './assistantChartData'

const props = defineProps<{ chart: AssistantChartData }>()
type Point = { index: number; value: number }

const data = computed<Point[]>(() => props.chart.data.map(({ value }, index) => ({ index, value })))
const tickValues = computed(() => data.value.map(point => point.index))
const x = (point: Point) => point.index
const y = (point: Point) => point.value
const color = 'var(--color-primary)'
const plotPadding = { bar: {}, line: { top: 10, right: 12, bottom: 8, left: 12 } }
const numberFormatter = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 6 })

// Unovis's native axis sets SVG text, and receives neither HTML nor a tooltip template.
function formatLabel(tick: number | Date) {
  return typeof tick === 'number' ? props.chart.data[tick]?.label ?? '' : ''
}

function axisLabel(tick: number) {
  const magnitude = Math.abs(tick)
  return magnitude >= 1e7 || (magnitude > 0 && magnitude < 1e-3)
    ? tick.toExponential(2)
    : numberFormatter.format(tick)
}

function formatValue(tick: number | Date, _index: number, ticks: number[] | Date[]) {
  if (typeof tick !== 'number') return ''
  const label = axisLabel(tick)
  return ticks.some(other => typeof other === 'number' && other !== tick && axisLabel(other) === label)
    ? String(tick)
    : label
}

const flatDomain = computed<[number, number] | undefined>(() => {
  const first = data.value[0]?.value
  if (first === undefined || !data.value.every(point => point.value === first)) return undefined
  if (props.chart.type === 'bar') return first === 0 ? [0, 1] : undefined
  const padding = Math.abs(first) * 0.1 || 1
  return [first - padding, first + padding]
})
</script>

<template>
  <div class="assistant-chart-plot" aria-hidden="true">
    <VisXYContainer class="assistant-chart-surface" :data="data" :duration="0" :y-domain="flatDomain"
      :padding="plotPadding[chart.type]"
    >
      <VisGroupedBar v-if="chart.type === 'bar'" :x="x" :y="y" :color="color" :group-max-width="54"
        :group-padding="0.22" :rounded-corners="2" :bar-min-height="0" />
      <template v-else>
        <VisLine :x="x" :y="y" :color="color" :curve-type="CurveType.Linear" />
        <VisScatter :x="x" :y="y" :color="color" :size="4" />
      </template>
      <VisAxis type="x" :tick-values="tickValues" :tick-format="formatLabel" tick-text-fit-mode="trim"
        tick-text-trim-type="end" :tick-text-adaptive-sets="true" :tick-text-hide-overlapping="true"
        :tick-line="false" :grid-line="false" :domain-line="false" />
      <VisAxis type="y" :tick-format="formatValue" :num-ticks="2" :tick-text-adaptive-sets="true"
        :tick-line="false" :domain-line="false" />
    </VisXYContainer>
  </div>
</template>

<style scoped>
.assistant-chart-plot {
  --vis-font-family: var(--font-sans);
  --vis-axis-grid-color: var(--color-border);
  --vis-axis-tick-label-color: var(--color-muted-foreground);
  --vis-axis-tick-label-font-size: 11px;
  --vis-axis-tick-label-hide-transition: none;
  min-width: 0;
  overflow: hidden;
}
.assistant-chart-surface {
  block-size: var(--assistant-chart-plot-height);
}
</style>
