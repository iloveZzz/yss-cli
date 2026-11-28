<script setup lang="ts">
import { Button } from '@/components/ui/button'
import type { useLifecycleGoalChange } from '../hooks/useLifecycleGoalChange'

const props = defineProps<{
  change: ReturnType<typeof useLifecycleGoalChange>
  kind: 'cancelLifecycleGoal' | 'reopenLifecycleGoal'
  onAccepted: () => void
}>()
async function recover(retry: boolean) {
  const attempt = props.change.pending.value.find(({ body }) => body.action.kind === props.kind)
  if (!attempt || attempt.status === 'sending') return
  if (await (retry ? props.change.retry(attempt.body) : props.change.check(attempt.body))) props.onAccepted()
}
</script>
<template>
  <div class="notice warning">
    <p role="status">结果待核对 · 原请求 ID 和提交内容已保留。</p>
    <p class="muted break-all">
      {{ change.pending.value.find(({ body }) => body.action.kind === kind)?.body.requestId }}
    </p>
    <div class="inline-actions">
      <Button type="button" variant="outline" :disabled="change.busy.value" @click="recover(false)">核对回执</Button>
      <Button type="button" variant="outline" :disabled="change.busy.value" @click="recover(true)">按原请求重试</Button>
    </div>
  </div>
</template>
