<script setup lang="ts">
import { computed, useId } from 'vue'
import { useRoute } from 'vue-router'
import StatusBadge from './StatusBadge.vue'
import { dateLabel, stages, contentStatuses, type WorkItem } from '@/services/workbench'
import { contentReminders } from '@/services/workbench-content'
const props = withDefaults(defineProps<{ items: WorkItem[]; caption?: string }>(), {
  caption: 'Spec 查询结果'
})
const route = useRoute()
const id = useId()
const reminders = contentReminders(undefined)
const rows = computed(() =>
  props.items.map((item) => ({
    item,
    reminders: reminders.flatMap((reminder) => {
      const count = item.reminderCounts?.[reminder.id] ?? 0
      return count ? [{ ...reminder, count }] : []
    })
  }))
)
function detail(item: WorkItem, query: Record<string, string> = {}) {
  return { path: `/specs/${item.specId}`, query: { return: route.fullPath, ...query } }
}
</script>
<template>
  <div class="table-scroll work-items-scroll" tabindex="0" role="region" :aria-labelledby="id">
    <table class="spec-table work-items">
      <caption :id="id" class="sr-only">{{ caption }}</caption>
      <thead>
        <tr>
          <th scope="col">Spec / 工程</th>
          <th scope="col">个人进度</th>
          <th scope="col">内容与待办</th>
          <th scope="col">下一步与阻塞</th>
          <th scope="col">最近更新</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="{ item, reminders: pending } in rows" :key="item.id">
          <th scope="row" class="item-title">
            <RouterLink :to="detail(item)" :title="item.sourcePath">{{ item.title }}</RouterLink>
            <span class="row-sub">{{ item.projectName }}{{ !item.active ? ' · 已停用' : '' }}</span>
            <RouterLink
              v-if="item.hasSourceProblem"
              :to="detail(item, { tab: 'resources' })"
              class="source-warning"
            >
              来源异常 · 查看
            </RouterLink>
          </th>
          <td class="item-progress">
            <StatusBadge :status="item.status" />
            <span class="row-sub">{{ stages[item.stage] }}</span>
          </td>
          <td class="item-content">
            <RouterLink :to="detail(item, { tab: 'content' })" class="content-state">
              {{ item.contentStatus ? contentStatuses[item.contentStatus] : '未标记' }}
            </RouterLink>
            <RouterLink
              v-if="item.needsReconfirmation"
              :to="detail(item, { tab: 'content' })"
              class="reconfirm-link"
            >
              需要重新确认
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
          </td>
          <td class="item-next">
            <p v-if="item.status === 'blocked'" class="row-blocker">
              {{ item.blocker || '待补充阻塞说明' }}
            </p>
            <p :class="{ 'row-sub': item.status === 'blocked' }">
              {{ item.nextAction || (item.status === 'blocked' ? '下一步尚未填写' : '—') }}
            </p>
          </td>
          <td class="item-updated">
            <time :datetime="item.updatedAt">{{ dateLabel(item.updatedAt) }}</time>
            <RouterLink :to="detail(item)" class="row-sub row-open">打开 Spec →</RouterLink>
          </td>
        </tr>
      </tbody>
    </table>
    <slot v-if="!items.length" name="empty">
      <p class="empty">当前筛选没有匹配记录，可清除条件或查看全部记录。</p>
    </slot>
  </div>
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
.row-blocker {
  color: #9b5a0e;
}
.item-updated {
  min-width: 145px;
  color: #858d99;
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
    min-width: 170px;
    max-width: 220px;
  }
  .work-items {
    min-width: 800px;
  }
}
</style>
