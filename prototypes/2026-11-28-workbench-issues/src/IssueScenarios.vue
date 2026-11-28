<script lang="ts">
export type ReadState = 'normal' | 'partial' | 'denied' | 'network' | 'unavailable'
export type Scenario = ReadState | 'none'
</script>

<script setup lang="ts">
import { Check, ChevronDown, FlaskConical } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

defineProps<{ scenario: Scenario; compact?: boolean }>()
const emit = defineEmits<{ select: [scenario: Scenario] }>()

const scenarios: { value: Scenario; label: string }[] = [
  { value: 'normal', label: '正常读取' },
  { value: 'denied', label: '仓库权限不足' },
  { value: 'partial', label: '部分结果' },
  { value: 'network', label: '网络失败' },
  { value: 'unavailable', label: '单条详情不可用' },
  { value: 'none', label: '无可用来源' },
]
</script>

<template>
  <DropdownMenu :modal="false">
    <DropdownMenuTrigger as-child>
      <Button
        variant="ghost"
        :size="compact ? 'icon' : 'default'"
        :class="compact ? 'drawer-scenario-trigger' : 'scenario-trigger'"
        aria-label="演示场景"
        :title="compact ? '演示场景' : undefined"
      >
        <FlaskConical aria-hidden="true" />
        <template v-if="!compact"><span>演示场景</span><ChevronDown aria-hidden="true" /></template>
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" :side-offset="4" class="issues-menu">
      <DropdownMenuLabel>原型演示场景</DropdownMenuLabel>
      <DropdownMenuSeparator />
      <DropdownMenuItem v-for="item in scenarios" :key="item.value" @select="emit('select', item.value)">
        {{ item.label }}<Check v-if="scenario === item.value" class="menu-check" aria-hidden="true" />
      </DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
