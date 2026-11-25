<script setup lang="ts">
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

withDefaults(defineProps<{ label: string; side?: 'top' | 'right' | 'bottom' | 'left' }>(), {
  side: 'bottom'
})
</script>

<template>
  <Tooltip>
    <TooltipTrigger as-child>
      <slot />
    </TooltipTrigger>
    <TooltipContent :side="side" class="max-w-64">{{ label }}</TooltipContent>
  </Tooltip>
</template>
