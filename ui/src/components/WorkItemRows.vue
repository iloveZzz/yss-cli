<script setup lang="ts">
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import { computed, useId } from 'vue'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import { useRoute } from 'vue-router'
import StatusBadge from './StatusBadge.vue'
import ProgressSummary from './ProgressSummary.vue'
import TableLoadingOverlay from './TableLoadingOverlay.vue'
import { dateLabel, stages, contentStatuses } from '@/services/workbench'
import type { CompactWorkItem } from '@/services/workbench-read'
import { contentReminders } from '@/services/workbench-content'
const props = withDefaults(defineProps<{
  items: CompactWorkItem[]
  caption?: string
  refreshing?: boolean
  refreshLabel?: string
}>(), {
  caption: 'Spec 查询结果',
  refreshLabel: '正在刷新 Spec 结果'
})
const route = useRoute()
const id = useId()
const reminders = contentReminders(undefined).filter((reminder) => reminder.id !== 'ready')
const rows = computed(() =>
  props.items.map((item) => ({
    item,
    reminders: reminders.flatMap((reminder) => {
      const count = item.reminderCounts?.[reminder.id] ?? 0
      return count ? [{ ...reminder, count }] : []
    })
  }))
)
function detail(item: CompactWorkItem, query: Record<string, string> = {}) {
  return { path: `/specs/${item.specId}`, query: { return: route.fullPath, ...query } }
}
</script>
<template>
  <div class="table-scroll work-items-scroll" tabindex="0" role="region" :aria-labelledby="id" :aria-busy="refreshing">
    <Table class="spec-table work-items">
      <TableCaption :id="id" class="sr-only">{{ caption }}</TableCaption>
      <TableHeader>
        <TableRow>
          <TableHead scope="col">Spec / 工程</TableHead>
          <TableHead scope="col">自动进度</TableHead>
          <TableHead scope="col">内容与待办</TableHead>
          <TableHead scope="col">下一步与阻塞</TableHead>
          <TableHead scope="col">最近更新</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="{ item, reminders: pending } in rows" :key="item.id" :data-spec-id="item.specId">
          <TableHead scope="row" class="item-title">
            <RouterLink :to="detail(item)" :title="item.sourcePath">{{ item.title }}</RouterLink>
            <span class="row-sub">{{ item.projectName }}{{ !item.active ? ' · 已停用' : '' }}</span>
            <RouterLink
              v-if="item.hasSourceProblem"
              :to="detail(item, { tab: 'resources' })"
              class="source-warning"
            >
              来源异常 · 查看
            </RouterLink>
          </TableHead>
          <TableCell class="item-progress">
            <StatusBadge :status="item.status" />
            <span class="row-sub">{{ stages[item.stage] }}</span>
          </TableCell>
          <TableCell class="item-content">
            <RouterLink
              :to="detail(item, { tab: 'content', category: 'ready' })"
              :class="item.needsReconfirmation || item.materialReview ? 'reconfirm-link' : 'content-state'"
            >
              {{ item.needsReconfirmation ? '新版待确认' : item.materialReview
                ? '材料待核实' : contentStatuses[item.contentStatus ?? 'unmarked'] }}
            </RouterLink>
            <div v-if="pending.length" class="reminder-links">
              <RouterLink
                v-for="reminder in pending"
                :key="reminder.id"
                :to="detail(item, { tab: 'content', category: reminder.id })"
              >
                {{ reminder.label }} {{ reminder.count }}
              </RouterLink>
            </div>
          </TableCell>
          <TableCell class="item-next">
            <ProgressSummary :progress="item.progress" compact />
          </TableCell>
          <TableCell class="item-updated">
            <time :datetime="item.updatedAt">{{ dateLabel(item.updatedAt) }}</time>
            <RouterLink :to="detail(item)" class="row-sub row-open">打开 Spec →</RouterLink>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
    <TableLoadingOverlay :active="!!refreshing && !!items.length" :label="refreshLabel" />
  </div>
  <slot v-if="!items.length" name="empty">
      <Empty><EmptyDescription>当前筛选没有匹配记录，可清除条件或查看全部记录。</EmptyDescription></Empty>
  </slot>
</template>
<style scoped>
.work-items-scroll {
  max-width: 100%;
  border-radius: inherit;
}
.work-items {
  min-width: 840px;
  width: 100%;
  border-collapse: separate;
  border-spacing: 0;
}
.work-items thead th {
  white-space: nowrap;
}
.work-items thead th:first-child {
  position: sticky;
  inset-inline-start: 0;
  z-index: 2;
  background: #f7f8fa;
}
.work-items tbody th {
  position: sticky;
  inset-inline-start: 0;
  z-index: 1;
  background: #fff;
  text-align: left;
  font-weight: 400;
}
.work-items tbody tr:hover th {
  background: #fafbfc;
}
.work-items .item-title {
  min-width: 200px;
  max-width: 310px;
  padding: 13px 16px;
  vertical-align: top;
}
.item-title > a:first-child {
  display: block;
  color: #303743;
  font-size: 13px;
  font-weight: 550;
  overflow-wrap: anywhere;
}
.item-title > a:first-child:hover {
  color: var(--yss-color-primary-control);
}
.item-progress {
  min-width: 100px;
  vertical-align: top;
}
.item-content {
  min-width: 165px;
  max-width: 250px;
  vertical-align: top;
}
.content-state {
  display: inline-block;
  font-size: 12px;
}
.reconfirm-link,
.source-warning {
  display: block;
  margin-top: 5px;
  color: #9b5a0e;
  font-size: 11px;
}
.reminder-links {
  display: flex;
  flex-wrap: wrap;
  gap: 5px 10px;
  margin-top: 7px;
}
.reminder-links a {
  font-size: 11px;
  color: #315dcf;
  white-space: nowrap;
}
.item-next {
  min-width: 200px;
  max-width: 340px;
  vertical-align: top;
  font-size: 12px;
  overflow-wrap: anywhere;
  white-space: normal;
}
.item-updated {
  min-width: 145px;
  color: var(--brand-color-text-secondary);
  font-size: 11px;
  vertical-align: top;
  white-space: nowrap;
}
.row-open {
  margin-top: 7px;
  color: #315dcf;
}
@media (width < 720px) {
  .work-items .item-title {
    min-width: 150px;
    max-width: 190px;
    white-space: normal;
  }
  .work-items {
    min-width: 800px;
  }
}
</style>
