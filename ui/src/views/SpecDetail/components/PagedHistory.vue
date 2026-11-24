<script setup lang="ts">
import { Alert } from '@/components/ui/alert'
import { onBeforeUnmount, shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import HistoryList from '@/components/HistoryList.vue'
import { getHistory } from '@/services/workbench-platform'
import { errorMessage, type ChangeEvent } from '@/services/workbench'
const props = defineProps<{ specId: string }>()
const events = shallowRef<ChangeEvent[]>([])
const total = shallowRef(0)
const pending = shallowRef<'replace' | 'append'>()
const error = shallowRef('')
let controller: AbortController | undefined
onBeforeUnmount(() => controller?.abort())
async function read(append = false) {
  if (pending.value) return
  controller?.abort()
  const request = new AbortController()
  controller = request
  const specId = props.specId
  pending.value = append ? 'append' : 'replace'
  error.value = ''
  try {
    const page = await getHistory(specId, append ? events.value.length : 0, 30, request.signal)
    if (request.signal.aborted || specId !== props.specId) return
    events.value = append ? [...events.value, ...page.items] : page.items
    total.value = page.total
  } catch (cause) {
    if (!request.signal.aborted) error.value = errorMessage(cause)
  } finally {
    if (controller === request && !request.signal.aborted) pending.value = undefined
  }
}
watch(
  () => props.specId,
  () => {
    pending.value = undefined
    events.value = []
    total.value = 0
    void read()
  },
  { immediate: true }
)
</script>
<template>
  <p v-if="pending" role="status" class="muted mb-2">{{ pending === 'append' ? '正在加载更多历史…' : '正在读取变更历史…' }}</p>
  <Card class="panel gap-0 p-0 shadow-none" :aria-busy="!!pending">
    <Alert v-if="error" class="notice error" role="alert">
      {{ error }}
      <Button variant="outline" @click="read()">重试</Button>
    </Alert>
    <LoadingSkeleton v-if="pending === 'replace' && !events.length" variant="text" :rows="3" class="p-5" />
    <div v-else-if="!error || events.length" class="relative">
      <HistoryList :events="events" />
      <LoadingIndicator :active="pending === 'replace'" placement="overlay" />
    </div>
    <div v-if="events.length < total" class="load-more">
      <Button variant="outline" :disabled="!!pending" @click="read(true)">
        <LoadingIndicator :active="pending === 'append'" />
        {{ pending === 'append' ? '正在加载更多…' : '加载更多历史' }}（{{ events.length }} / {{ total }}）
      </Button>
    </div>
  </Card>
</template>
