<script setup lang="ts">
import { computed } from 'vue'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import { dateLabel, type ChangeEvent } from '@/services/workbench'
import { eventDescription, historyFieldLabel, historyValue } from '@/services/workbench-history'
const props = defineProps<{ events: readonly ChangeEvent[] }>()
const specEvents = computed(() => props.events.filter((event) => !event.taskId))
</script>
<template>
  <ol v-if="specEvents.length" class="history-list">
    <li v-for="event in specEvents" :key="event.id">
      <strong>{{ eventDescription(event) }}</strong>
      <time :datetime="event.at">{{ dateLabel(event.at) }}</time>
      <dl>
        <div v-for="(change, field) in event.changes" :key="field">
          <dt>{{ historyFieldLabel(field) }}</dt>
          <dd>
            <span class="muted">{{ historyValue(field, change.before) }}</span>
            <span aria-label="改为">→</span>
            {{ historyValue(field, change.after) }}
          </dd>
        </div>
      </dl>
    </li>
  </ol>
  <Empty v-else class="border-0">
    <EmptyDescription>暂无个人变更记录，接入前过程尚未记录。</EmptyDescription>
  </Empty>
</template>
