<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'

const open = defineModel<boolean>({ default: false })
</script>

<template>
  <Collapsible v-model:open="open" class="job-disclosure">
    <CollapsibleTrigger as-child>
      <Button type="button" variant="ghost" size="sm" class="job-disclosure-trigger">
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
          <path d="m6 4 4 4-4 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        <span class="job-disclosure-label"><slot name="label" /></span>
      </Button>
    </CollapsibleTrigger>
    <CollapsibleContent class="job-disclosure-content">
      <slot />
    </CollapsibleContent>
  </Collapsible>
</template>

<style scoped>
.job-disclosure,
.job-disclosure-content {
  min-width: 0;
}
.job-disclosure-trigger {
  max-width: 100%;
  height: auto;
  min-height: var(--yss-control-min-height);
  justify-content: flex-start;
  padding-block: 6px;
  color: var(--yss-color-primary-control);
  text-align: start;
  white-space: normal;
}
.job-disclosure-trigger svg {
  flex: none;
}
.job-disclosure-trigger[data-state='open'] svg {
  transform: rotate(90deg);
}
.job-disclosure-label {
  min-width: 0;
  overflow-wrap: anywhere;
}
.job-disclosure-content :deep(pre) {
  margin-block: 6px 12px;
  padding: 12px;
  border-radius: 8px;
  background: var(--brand-color-bg-layout);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 24rem;
  overflow: auto;
  font-size: 12px;
  line-height: 1.6;
}
@media (width < 720px), (pointer: coarse) {
  .job-disclosure-trigger {
    min-height: 44px;
  }
}
</style>
