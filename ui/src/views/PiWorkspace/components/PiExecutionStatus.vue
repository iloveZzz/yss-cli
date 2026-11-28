<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { useIntervalFn } from '@vueuse/core'

const props = defineProps<{ label: string; startedAt?: string }>()
const started = computed(() => Date.parse(props.startedAt ?? ''))
const now = shallowRef(Date.now())
const { pause, resume } = useIntervalFn(() => { now.value = Date.now() }, 1000, { immediate: false })
watch(started, (value) => {
  pause()
  if (Number.isFinite(value)) {
    now.value = Date.now()
    resume()
  }
}, { immediate: true })
const seconds = computed(() => Math.max(0, Math.floor((now.value - started.value) / 1000)))
const elapsed = computed(() => seconds.value < 60
  ? `${seconds.value} 秒`
  : `${Math.floor(seconds.value / 60)} 分 ${seconds.value % 60} 秒`)
</script>

<template>
  <div class="pi-top-note pi-execution-status">
    <span role="status">{{ label }}</span>
    <time v-if="Number.isFinite(started)" :datetime="'PT' + seconds + 'S'" aria-label="本轮已耗时">
      {{ elapsed }}
    </time>
  </div>
</template>
