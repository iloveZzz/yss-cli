<script setup lang="ts">
import { computed } from 'vue'
import { Gauge } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import type { PiContextUsage } from '@/services/workbench-platform'

const props = defineProps<{ usage?: PiContextUsage | null; limit?: number | null }>()
const limit = computed(() => props.usage?.contextWindow ?? props.limit ?? 0)
const used = computed(() => props.usage?.usedTokens ?? null)
const percent = computed(() => used.value === null || !limit.value ? null : used.value / limit.value * 100)
const number = new Intl.NumberFormat('zh-CN')
const percentText = (value: number) => value > 0 && value < 1 ? '< 1%' : Math.round(value) + '%'
</script>

<template>
  <Popover v-if="limit > 0">
    <PopoverTrigger as-child>
      <Button type="button" variant="ghost" size="xs">
        <Gauge :size="14" aria-hidden="true" />上下文<template v-if="percent !== null"> {{ percentText(percent) }}</template>
      </Button>
    </PopoverTrigger>
    <PopoverContent align="start" :collision-padding="12" class="pi-context-popover space-y-2">
      <p class="font-medium">Pi 上下文窗口</p>
      <meter v-if="used !== null" aria-label="上下文 token 用量" :max="limit" :value="Math.min(used, limit)" class="w-full" />
      <p class="text-xs text-muted-foreground" v-if="used !== null">已用约 {{ number.format(used) }} / {{ number.format(limit) }} token</p>
      <p class="text-xs text-muted-foreground" v-else>窗口上限 {{ number.format(limit) }} token，等待 Pi 返回用量。</p>
    </PopoverContent>
  </Popover>
</template>

<style>
.pi-context-popover[data-slot='popover-content'] { background: var(--brand-color-bg-container, #fff); }
</style>
