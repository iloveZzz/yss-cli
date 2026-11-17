<script setup lang="ts">
import { computed } from 'vue'
import { dateLabel, type ChangeEvent } from '@/services/workbench'
import { eventDescription, historyFieldLabel, historyValue } from '@/services/workbench-history'
const props = defineProps<{ events: readonly ChangeEvent[] }>()
const specEvents = computed(() => props.events.filter((event) => !event.taskId))
</script>
<template>
  <ol class="history-list">
    <li v-for="event in specEvents" :key="event.id">
      <strong>{{ eventDescription(event) }}</strong>
      <time>{{ dateLabel(event.at) }}</time>
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
  <p v-if="!specEvents.length" class="empty">暂无个人变更记录，接入前过程尚未记录。</p>
</template>
