<script setup lang="ts">
import { Button } from '@/components/ui/button'
import type { Stage } from '@/services/workbench'
defineProps<{ specId: string; lifecycleStage?: Stage }>()
</script>

<template>
  <section class="platform-section">
    <Button variant="outline" as-child>
      <RouterLink :to="{ name: 'pi-workspace', params: { specId }, query: { stage: lifecycleStage } }">
        打开 Pi 会话工作区
      </RouterLink>
    </Button>
  </section>
</template>
