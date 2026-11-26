<script setup lang="ts">
import type { HTMLAttributes } from "vue"
import { cn } from "@/lib/utils"

const props = defineProps<{
  class?: HTMLAttributes["class"]
  scrollable?: boolean
  scrollLabel?: string
}>()

defineOptions({ inheritAttrs: false })
</script>

<template>
  <div
    v-if="scrollable"
    data-slot="table-container"
    class="table-scroll relative w-full"
    :role="scrollLabel ? 'region' : undefined"
    :aria-label="scrollLabel"
    tabindex="0"
  >
    <table data-slot="table" v-bind="$attrs" :class="cn('w-full caption-bottom text-sm', props.class)">
      <slot />
    </table>
  </div>
  <table v-else data-slot="table" v-bind="$attrs" :class="cn('w-full caption-bottom text-sm', props.class)">
    <slot />
  </table>
</template>
