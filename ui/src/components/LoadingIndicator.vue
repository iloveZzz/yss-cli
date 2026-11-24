<script setup lang="ts">
import { Spinner } from '@/components/ui/spinner'
import { useDelayedBusy } from '@/hooks/useDelayedBusy'

const props = withDefaults(defineProps<{
  active: boolean
  label?: string
  placement?: 'inline' | 'overlay'
  delay?: number
}>(), { placement: 'inline', delay: 180 })
const visible = useDelayedBusy(() => props.active, () => props.delay)
</script>

<template>
  <span
    v-show="active"
    data-loading-indicator
    :data-placement="placement"
    :data-visible="visible"
    class="loading-indicator"
  >
    <span v-if="label" class="sr-only" role="status">{{ visible ? label : '' }}</span>
    <span class="loading-mark" aria-hidden="true">
      <Spinner class="loading-spinner" />
      <span v-if="label">{{ label }}</span>
    </span>
  </span>
</template>

<style scoped>
.loading-indicator {
  display: inline-flex;
  align-items: center;
  flex: none;
  vertical-align: middle;
}
.loading-mark {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-height: 1em;
  white-space: nowrap;
  opacity: 0;
  transition: opacity 120ms ease-out;
}
.loading-indicator[data-visible='true'] .loading-mark { opacity: 1; }
.loading-spinner { width: 14px; height: 14px; flex: none; }
.loading-indicator[data-visible='false'] .loading-spinner { animation-play-state: paused; }
.loading-indicator[data-placement='overlay'] {
  position: absolute;
  inset: 0 0 auto;
  z-index: 2;
  display: grid;
  place-items: center;
  min-height: min(100%, 160px);
  pointer-events: none;
}
.loading-indicator[data-placement='overlay'] .loading-mark {
  min-height: 32px;
  max-width: calc(100% - 16px);
  padding: 4px 12px;
  border: 1px solid var(--yss-colors-border);
  border-radius: var(--brand-border-radius);
  background: var(--brand-color-bg-elevated);
  color: var(--brand-color-text);
  box-shadow: 0 3px 14px #17213617;
  font-size: 12px;
  white-space: normal;
}
@media (prefers-reduced-motion: reduce) {
  .loading-spinner { animation: none; }
  .loading-mark { transition: none; }
}
</style>
