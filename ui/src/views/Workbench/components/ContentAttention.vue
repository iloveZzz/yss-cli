<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { Button } from '@/components/ui/button'
import { contentReminders } from '@/services/workbench-content'
import type { SpecView } from '@/services/workbench'
const props = defineProps<{ specs: readonly SpecView[] }>()
const selected = shallowRef('decision')
const summaries = computed(() => props.specs.map(spec => ({ spec, reminders: contentReminders(spec.content) })))
const counts = computed(() => contentReminders(undefined).map(group => ({ ...group, count: summaries.value.reduce((sum, item) => sum + (item.reminders.find(reminder => reminder.id === group.id)?.count ?? 0), 0) })))
const selectedLabel = computed(() => counts.value.find(group => group.id === selected.value)?.label)
const items = computed(() => summaries.value.flatMap(({ spec, reminders }) => {
  const count = reminders.find(group => group.id === selected.value)?.count ?? 0
  return count ? [{ spec, count }] : []
}))
</script>
<template>
  <section class="work-section content-attention">
    <div class="section-heading"><h2>需要你处理</h2><span class="muted">按主规格内容提醒</span></div>
    <div class="attention-filters" role="group" aria-label="内容提醒类别"><Button v-for="group in counts" :key="group.id" variant="outline" :aria-pressed="selected === group.id" @click="selected = group.id"><span>{{ group.label }}</span><strong>{{ group.count }}</strong></Button></div>
    <div class="panel attention-items"><article v-for="item in items" :key="item.spec.record.id"><RouterLink :to="{ path: `/specs/${item.spec.record.id}`, query: { return: '/workbench', tab: 'content' } }"><strong>{{ item.spec.record.title }}</strong><span>{{ selectedLabel }} {{ item.count }} →</span></RouterLink><p class="path-text">{{ item.spec.record.sourcePath }}</p></article><p v-if="!items.length" class="empty">暂无{{ selectedLabel }}事项</p></div>
    <p class="muted view-note">答复保存后进入待回写；到期或设有复查条件的延期列入需要复查，条件是否满足由你核对。提醒随工作台读取更新，不发送后台通知。</p>
  </section>
</template>
<style scoped>
.attention-filters { display: grid; grid-template-columns: repeat(5, 1fr); gap: 8px; margin-bottom: 12px; }
.attention-filters button { height: auto; min-height: 58px; display: flex; flex-direction: column; align-items: flex-start; padding: 10px 12px; gap: 2px; }
.attention-filters button[aria-pressed=true] { border-color: var(--yss-color-primary-control); background: #f3f6fd; color: var(--yss-color-primary-control); }
.attention-filters strong { font-size: 20px; font-weight: 550; }
.attention-items article { padding: 14px 18px; border-bottom: 1px solid var(--yss-colors-border); }
.attention-items article:last-child { border: 0; }
.attention-items a { display: flex; justify-content: space-between; gap: 12px; }
.attention-items a span { color: var(--yss-color-primary-control); white-space: nowrap; }
@media (width < 720px) { .attention-filters { grid-template-columns: repeat(3, 1fr); }.attention-items a { flex-wrap: wrap; } }
</style>
