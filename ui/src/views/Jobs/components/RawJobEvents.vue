<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { shallowRef, toRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import JobDisclosure from './JobDisclosure.vue'
import { dateLabel } from '@/services/workbench'
import { readableValue } from '@/services/workbench-platform'
import { useJobHistory } from '../hooks/useJobHistory'
const props = defineProps<{ jobId: string }>()
const history = useJobHistory(toRef(props, 'jobId'))
const rawOpen = shallowRef(false)
watch(rawOpen, (open) => {
  if (open && !history.loaded.value && !history.loading.value) void history.read(0)
})
</script>
<template>
  <JobDisclosure v-model="rawOpen" class="raw-history">
    <template #label>查看完整原始事件</template>
    <template v-if="rawOpen">
      <div class="history-toolbar">
        <span class="muted">第 {{ history.page.value + 1 }} 页 · 每页最多 200 条</span>
        <Button
          type="button"
          variant="ghost"
          :aria-disabled="history.loading.value"
          @click="!history.loading.value && history.read()"
          >刷新当前页</Button
        >
      </div>
      <Alert v-if="history.error.value" variant="destructive" class="notice error">
        <AlertDescription class="text-current">{{ history.error.value }}</AlertDescription>
      </Alert>
      <LoadingSkeleton v-if="!history.loaded.value && history.loading.value" variant="text" :rows="3" label="正在读取原始事件" />
      <LoadingIndicator :active="history.loaded.value && history.loading.value" label="正在读取原始事件" />
      <ol v-if="history.loaded.value" class="raw-events" role="list" aria-label="原始事件历史" :aria-busy="history.loading.value">
        <li v-for="event in history.items.value" :key="event.id">
          <JobDisclosure>
            <template #label>
              <span>#{{ event.id }} · {{ event.kind }}</span
              ><time :datetime="event.createdAt">{{ dateLabel(event.createdAt) }}</time>
            </template>
            <pre>{{ readableValue(event) }}</pre>
          </JobDisclosure>
        </li>
      </ol>
      <Empty v-if="history.loaded.value && !history.items.value.length" class="p-4">
        <EmptyDescription>本页暂无事件</EmptyDescription>
      </Empty>
      <nav class="history-toolbar" aria-label="原始事件分页">
        <Button
          type="button"
          variant="outline"
          :aria-disabled="history.loading.value || history.page.value === 0"
          @click="!history.loading.value && history.page.value > 0 && history.read(history.page.value - 1)"
          >上一页</Button
        >
        <Button
          type="button"
          variant="outline"
          :aria-disabled="history.loading.value || !history.hasMore.value"
          @click="!history.loading.value && history.hasMore.value && history.read(history.page.value + 1)"
          >下一页</Button
        >
      </nav>
    </template>
  </JobDisclosure>
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
.history-toolbar button[aria-disabled='true'] {
  opacity: 0.65;
}
.history-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin: 12px 0;
}
</style>
