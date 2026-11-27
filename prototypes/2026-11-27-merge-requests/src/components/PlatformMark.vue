<script setup lang="ts">
import { GitBranch, GitFork } from '@lucide/vue'
import type { Platform } from '@/demo'

defineProps<{ platform: Platform }>()
</script>

<template>
  <span class="platform-mark">
    <component :is="platform === 'GitHub' ? GitBranch : GitFork" :data-platform="platform" aria-hidden="true" />
    <span>{{ platform }}</span>
  </span>
</template>
