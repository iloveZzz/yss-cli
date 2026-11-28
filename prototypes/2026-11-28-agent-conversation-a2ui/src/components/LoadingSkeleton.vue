<script setup lang="ts">
import { Skeleton } from '@/components/ui/skeleton'
import { useDelayedBusy } from '@/hooks/useDelayedBusy'

const props = withDefaults(defineProps<{
  variant?: 'table' | 'form' | 'detail' | 'cards' | 'text'
  rows?: number
  label?: string
  delay?: number
}>(), { variant: 'text', rows: 3, delay: 160 })
const visible = useDelayedBusy(() => true, () => props.delay)
</script>

<template>
  <div data-loading-skeleton :data-variant="variant" :data-visible="visible" class="loading-skeleton">
    <span v-if="label" class="sr-only" role="status">{{ visible ? label : '' }}</span>
    <div aria-hidden="true" class="skeleton-layout">
      <template v-if="variant === 'table'">
        <div class="skeleton-table-head">
          <Skeleton class="h-3 w-2/5" />
          <Skeleton class="h-3 w-1/2" />
          <Skeleton class="h-3 w-1/3" />
        </div>
        <div v-for="row in rows" :key="row" class="skeleton-table-row">
          <Skeleton class="h-3.5 w-3/4" />
          <Skeleton class="h-3.5 w-1/2" />
          <Skeleton class="h-3.5 w-2/3" />
        </div>
      </template>
      <template v-else-if="variant === 'form'">
        <div v-for="row in rows" :key="row" class="skeleton-field">
          <Skeleton class="h-3 w-24 max-w-full" />
          <Skeleton class="skeleton-control" />
        </div>
      </template>
      <template v-else-if="variant === 'detail'">
        <div class="skeleton-detail-heading">
          <Skeleton class="h-6 w-2/5" />
          <Skeleton class="h-3 w-3/5" />
        </div>
        <div class="skeleton-detail-body">
          <Skeleton class="h-4 w-1/4" />
          <Skeleton v-for="row in rows" :key="row" class="skeleton-line" />
        </div>
      </template>
      <template v-else-if="variant === 'cards'">
        <div v-for="row in rows" :key="row" class="skeleton-card">
          <Skeleton class="h-4 w-2/5" />
          <Skeleton class="h-3 w-4/5" />
          <Skeleton class="h-3 w-3/5" />
        </div>
      </template>
      <div v-else class="skeleton-text">
        <Skeleton v-for="row in rows" :key="row" class="skeleton-line" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.loading-skeleton { position: relative; min-width: 0; }
.skeleton-layout { opacity: 0; transition: opacity 140ms ease-out; }
.loading-skeleton[data-visible='true'] .skeleton-layout { opacity: 1; }
.loading-skeleton[data-visible='false'] :deep([data-slot='skeleton']) { animation-play-state: paused; }
.skeleton-table-head, .skeleton-table-row {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 1fr) minmax(0, 1fr);
  align-items: center;
  gap: 12px;
  min-height: 48px;
  padding: 0 16px;
  border-bottom: 1px solid var(--yss-colors-border);
  background: var(--brand-color-bg-container);
}
.skeleton-table-head { min-height: 36px; background: var(--brand-color-bg-layout); }
[data-variant='table'] .skeleton-layout, .skeleton-card, .skeleton-detail-body {
  overflow: hidden;
  border: 1px solid var(--yss-colors-border);
  border-radius: var(--brand-border-radius-lg);
  background: var(--brand-color-bg-container);
}
.skeleton-table-row:last-child { border-bottom: 0; }
.skeleton-field { display: grid; gap: 6px; padding-block-end: 14px; }
.skeleton-control { min-height: var(--yss-control-min-height, 34px); }
.skeleton-detail-heading { display: grid; gap: 10px; margin-bottom: 18px; }
.skeleton-detail-body, .skeleton-card { display: grid; gap: 14px; padding: 20px; }
.skeleton-text { display: grid; gap: 10px; }
.skeleton-line { height: 12px; width: 86%; }
.skeleton-line:nth-child(even) { width: 67%; }
.skeleton-line:last-child { width: 56%; }
[data-variant='cards'] .skeleton-layout {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 210px), 1fr));
  gap: 14px;
}
@media (prefers-reduced-motion: reduce) {
  .skeleton-layout { transition: none; }
}
</style>
