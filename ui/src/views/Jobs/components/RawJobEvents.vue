<script setup lang="ts">
import { shallowRef, toRef } from 'vue'
import { Button } from '@/components/ui/button'
import { dateLabel } from '@/services/workbench'
import { readableValue } from '@/services/workbench-platform'
import { useJobHistory } from '../hooks/useJobHistory'
const props = defineProps<{ jobId: string }>()
const history = useJobHistory(toRef(props, 'jobId'))
const rawOpen = shallowRef(false)
function toggleRaw(event: Event) {
  rawOpen.value = (event.target as HTMLDetailsElement).open
  if (rawOpen.value && !history.loaded.value && !history.loading.value) void history.read(0)
}
</script>
<template>
  <details class="raw-history" @toggle="toggleRaw">
    <summary>查看完整原始事件</summary>
    <template v-if="rawOpen">
      <div class="history-toolbar">
        <span class="muted">第 {{ history.page.value + 1 }} 页 · 每页最多 200 条</span>
        <Button
          type="button"
          variant="ghost"
          :disabled="history.loading.value"
          @click="history.read()"
          >刷新当前页</Button
        >
      </div>
      <p v-if="history.error.value" role="alert" class="notice error">
        {{ history.error.value }}
      </p>
      <ol class="raw-events" role="list" aria-label="原始事件历史">
        <li v-for="event in history.items.value" :key="event.id">
          <details>
            <summary>
              <span>#{{ event.id }} · {{ event.kind }}</span
              ><time>{{ dateLabel(event.createdAt) }}</time>
            </summary>
            <pre>{{ readableValue(event) }}</pre>
          </details>
        </li>
      </ol>
      <p v-if="history.loaded.value && !history.items.value.length" class="empty">本页暂无事件</p>
      <nav class="history-toolbar" aria-label="原始事件分页">
        <Button
          type="button"
          variant="outline"
          :disabled="history.loading.value || history.page.value === 0"
          @click="history.read(history.page.value - 1)"
          >上一页</Button
        >
        <Button
          type="button"
          variant="outline"
          :disabled="history.loading.value || !history.hasMore.value"
          @click="history.read(history.page.value + 1)"
          >下一页</Button
        >
      </nav>
    </template>
  </details>
</template>
<style scoped>
.raw-history {
  border-top: 1px solid var(--yss-colors-border);
  padding-top: 12px;
  margin-top: 14px;
}
.raw-events {
  list-style: none;
  padding: 0;
  max-height: 480px;
  overflow: auto;
}
.raw-events li {
  padding: 8px 0;
  border-bottom: 1px solid var(--yss-colors-border);
}
.raw-events time {
  margin-left: 12px;
  color: var(--brand-color-text-secondary);
}
.raw-events pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 360px;
  overflow: auto;
}
.history-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  margin: 12px 0;
}
</style>
