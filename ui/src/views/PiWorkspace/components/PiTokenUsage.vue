<script setup lang="ts">
import { computed } from 'vue'
import type { PiUsage } from '@/services/workbench-platform'

const props = defineProps<{ usage: PiUsage }>()
const number = new Intl.NumberFormat('zh-CN')
const currency = new Intl.NumberFormat('zh-CN', { style: 'currency', currency: 'USD', maximumFractionDigits: 6 })
const rows = computed(() => [
  ...(props.usage.turn ? [{ label: '本轮', tally: props.usage.turn }] : []),
  { label: '本机 Pi 会话累计', tally: props.usage.session }
])
</script>

<template>
  <details class="pi-token-usage">
    <summary>Pi 用量<template v-if="usage.turn"> · {{ number.format(usage.turn.total) }} tokens</template></summary>
    <div v-for="{ label, tally } in rows" :key="label">
      <strong>{{ label }}</strong>
      <dl>
        <dt>输入</dt><dd>{{ number.format(tally.input) }}</dd>
        <dt>输出</dt><dd>{{ number.format(tally.output) }}</dd>
        <dt>缓存读取</dt><dd>{{ number.format(tally.cacheRead) }}</dd>
        <dt>缓存写入</dt><dd>{{ number.format(tally.cacheWrite) }}</dd>
        <dt>合计</dt><dd>{{ number.format(tally.total) }}</dd>
        <dt>Pi 报告费用</dt><dd>{{ currency.format(tally.cost) }}</dd>
      </dl>
    </div>
  </details>
</template>

<style scoped>
.pi-token-usage { color: var(--quiet); font-size: 12px; }
.pi-token-usage summary { cursor: pointer; }
.pi-token-usage div { margin-top: 8px; }
.pi-token-usage dl { display: grid; grid-template-columns: max-content 1fr; gap: 4px 12px; margin: 4px 0; }
</style>
