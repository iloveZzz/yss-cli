<script setup lang="ts">
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import type { ErrorFeedback } from '@/services/workbench'

defineProps<{ feedback: ErrorFeedback }>()
</script>

<template>
  <Alert class="notice error" variant="destructive">
    <AlertTitle>{{ feedback.message }}</AlertTitle>
    <AlertDescription v-if="feedback.hint">处理建议：{{ feedback.hint }}</AlertDescription>
    <slot />
  </Alert>
</template>
