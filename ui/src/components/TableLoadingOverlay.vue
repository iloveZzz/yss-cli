<script setup lang="ts">
import { computed, onScopeDispose, shallowRef, useTemplateRef, watch, type CSSProperties } from 'vue'
import { useEventListener, useResizeObserver } from '@vueuse/core'
import { Marker, MarkerContent, MarkerIcon } from '@/components/ui/marker'
import { Spinner } from '@/components/ui/spinner'
import { useDelayedBusy } from '@/hooks/useDelayedBusy'

const props = defineProps<{ active: boolean; label: string }>()
const visible = useDelayedBusy(() => props.active, () => 180)
const mask = useTemplateRef<HTMLElement>('mask')
const scrollport = computed(() => mask.value?.parentElement)
const listener = computed(() => scrollport.value ? window : undefined)
const bounds = shallowRef<CSSProperties>()
let frame = 0

function measure() {
  const port = scrollport.value
  if (!port) return
  const rect = port.getBoundingClientRect()
  const root = document.documentElement
  const minTop = Number.parseFloat(getComputedStyle(root).scrollPaddingBlockStart) || 0
  const x = rect.left + port.clientLeft
  const y = rect.top + port.clientTop
  const left = Math.max(0, x)
  const top = Math.max(minTop, y)
  const width = Math.max(0, Math.min(root.clientWidth, x + port.clientWidth) - left)
  const height = Math.max(0, Math.min(root.clientHeight, y + port.clientHeight) - top)
  bounds.value = {
    left: port.scrollLeft + left - x + 'px',
    top: port.scrollTop + top - y + 'px',
    width: width + 'px',
    height: height + 'px',
    visibility: width > 0 && height > 0 ? 'visible' : 'hidden'
  }
}

function scheduleMeasure() {
  if (frame) return
  frame = requestAnimationFrame(() => {
    frame = 0
    measure()
  })
}

watch(scrollport, (port) => {
  if (port) measure()
  else bounds.value = undefined
}, { flush: 'post' })
useEventListener(listener, 'scroll', scheduleMeasure, { passive: true, capture: true })
useEventListener(listener, 'resize', scheduleMeasure, { passive: true })
useResizeObserver(scrollport, scheduleMeasure)
onScopeDispose(() => cancelAnimationFrame(frame))
</script>

<template>
  <Transition name="workbench-feedback">
    <div v-if="visible" ref="mask" class="table-loading-mask" data-table-loading aria-hidden="true" :style="bounds">
      <Marker class="table-loading-marker">
        <MarkerIcon><Spinner /></MarkerIcon>
        <MarkerContent>{{ label }}</MarkerContent>
      </Marker>
    </div>
  </Transition>
</template>

<style scoped>
.table-loading-mask {
  position: absolute;
  inset: 0 auto auto 0;
  z-index: 2;
  display: grid;
  place-items: center;
  padding: 8px;
  visibility: hidden;
  overflow: clip;
  pointer-events: none;
  background: color-mix(in srgb, var(--brand-color-bg-container) 14%, transparent);
}
.table-loading-marker {
  border: 1px solid var(--yss-colors-border);
  border-radius: var(--brand-border-radius);
  padding: 6px 10px;
  width: max-content;
  max-width: 100%;
  background: var(--brand-color-bg-elevated);
  color: var(--brand-color-text);
  font-size: 12px;
  box-shadow: 0 3px 14px #17213614;
}
.table-loading-marker :deep([data-slot='spinner']) { color: var(--yss-color-primary-control); }
@media (forced-colors: active) {
  .table-loading-mask { background: transparent; }
  .table-loading-marker { border-color: CanvasText; }
}
</style>
