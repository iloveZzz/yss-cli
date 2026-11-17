<script setup lang="ts">
import { shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import HistoryList from '@/components/HistoryList.vue'
import { getHistory } from '@/services/workbench-platform'
import { errorMessage, type ChangeEvent } from '@/services/workbench'
const props = defineProps<{ specId: string }>()
const events = shallowRef<ChangeEvent[]>([])
const total = shallowRef(0)
const loading = shallowRef(false)
const error = shallowRef('')
async function read(append = false) {
  loading.value = true
  error.value = ''
  try {
    const page = await getHistory(props.specId, append ? events.value.length : 0, 30)
    events.value = append ? [...events.value, ...page.items] : page.items
    total.value = page.total
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    loading.value = false
  }
}
watch(
  () => props.specId,
  () => void read(),
  { immediate: true }
)
</script>
<template>
  <section class="panel">
    <p v-if="error" class="notice error" role="alert">
      {{ error }}
      <Button variant="outline" @click="read()">重试</Button>
    </p>
    <HistoryList :events="events" />
    <div v-if="events.length < total" class="load-more">
      <Button variant="outline" :disabled="loading" @click="read(true)">
        加载更多历史（{{ events.length }} / {{ total }}）
      </Button>
    </div>
  </section>
</template>
