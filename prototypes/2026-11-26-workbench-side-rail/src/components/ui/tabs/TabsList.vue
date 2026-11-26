<script setup lang="ts">
import type { TabsListProps } from "reka-ui"
import type { HTMLAttributes } from "vue"
import { reactiveOmit } from "@vueuse/core"
import { TabsIndicator, TabsList } from "reka-ui"
import { cn } from "@/lib/utils"

const props = defineProps<TabsListProps & { class?: HTMLAttributes["class"]; movingIndicator?: boolean }>()

const delegatedProps = reactiveOmit(props, "class", "movingIndicator")
</script>

<template>
  <TabsList
    data-slot="tabs-list"
    :data-moving-indicator="movingIndicator || undefined"
    v-bind="delegatedProps"
    :class="cn(
      'bg-muted text-muted-foreground inline-flex h-9 w-fit items-center justify-center rounded-lg p-0.75',
      props.class,
    )"
  >
    <slot />
    <TabsIndicator v-if="movingIndicator" data-slot="tabs-indicator" aria-hidden="true" />
  </TabsList>
</template>
