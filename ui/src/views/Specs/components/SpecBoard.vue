<script setup lang="ts">
import { computed, shallowRef, watch, useId, nextTick, useTemplateRef } from 'vue'
import { useRoute } from 'vue-router'
import {
  stages,
  statuses,
  contentStatuses,
  type WorkItem,
  type GroupBy,
  type Stage,
  type Status
} from '@/services/workbench'
import { Button } from '@/components/ui/button'
import SingleSelect from '@/components/SingleSelect.vue'
import ModalDialog from '@/components/ModalDialog.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { contentReminders } from '@/services/workbench-content'
import { useWorkActions } from '../hooks/useWorkActions'
const props = defineProps<{ items: WorkItem[]; groupBy: GroupBy; revision: number }>()
const route = useRoute()
const id = useId()
const { move, confirm, cancel, open, blocker, error, notice, saving } = useWorkActions()
const board = useTemplateRef<HTMLElement>('board')
watch(notice, async (value) => {
  if (value) {
    await nextTick()
    if (document.activeElement === document.body) board.value?.focus()
  }
})
const limits = defineModel<Record<string, number>>('limits', { required: true })
const scrollLeft = defineModel<number>('scrollLeft', { required: true })
watch(
  [board, scrollLeft],
  () => {
    if (board.value) board.value.scrollLeft = scrollLeft.value
  },
  { flush: 'post' }
)
const hideEmpty = shallowRef(true)
const reminders = contentReminders(undefined)
const dragging = shallowRef<{ item: WorkItem; revision: number; groupBy: GroupBy }>()
const hovered = shallowRef('')
const labels = computed(() => (props.groupBy === 'status' ? statuses : stages))
const columns = computed(() =>
  Object.entries(labels.value)
    .map(([key, label]) => ({
      key,
      label,
      items: props.items.filter((i) => i[props.groupBy] === key)
    }))
    .filter((c) => !hideEmpty.value || c.items.length)
)
watch([() => props.items, () => props.groupBy], () => {
  dragging.value = undefined
})
function start(event: DragEvent, item: WorkItem) {
  if (!item.active || saving.value) {
    event.preventDefault()
    return
  }
  dragging.value = { item, revision: props.revision, groupBy: props.groupBy }
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', item.id)
  }
}
async function drop(key: string) {
  const current = dragging.value
  dragging.value = undefined
  hovered.value = ''
  if (current && current.groupBy === props.groupBy)
    await move(current.item, props.groupBy, key as Stage | Status, current.revision)
}
function end() {
  dragging.value = undefined
  hovered.value = ''
}
</script>
<template>
  <div class="toolbar">
    <label class="check-label">
      <input v-model="hideEmpty" type="checkbox" />
      隐藏空列
    </label>
    <span class="muted">可拖动卡片，或用“移动到…”菜单；不改变列内排序。</span>
  </div>
  <p v-if="notice" role="status" class="notice">{{ notice }}</p>
  <p v-if="error && !open" role="alert" class="notice error">{{ error }}</p>
  <div
    ref="board"
    tabindex="0"
    role="region"
    class="board"
    @scroll.passive="scrollLeft = ($event.target as HTMLElement).scrollLeft"
    :aria-label="groupBy === 'status' ? '按状态分组的看板' : '按阶段分组的看板'"
  >
    <section
      v-for="column in columns"
      :key="column.key"
      :class="['board-column', { 'drop-target': hovered === column.key }]"
      :aria-label="column.label"
      @dragover.prevent="hovered = column.key"
      @dragleave="hovered = ''"
      @drop.prevent="drop(column.key)"
    >
      <header>
        <h2>{{ column.label }}</h2>
        <span class="count">{{ column.items.length }}</span>
      </header>
      <article
        v-for="item in column.items.slice(0, limits[column.key] ?? 50)"
        :key="item.id"
        class="board-card"
        :draggable="item.active && !saving"
        @dragstart="start($event, item)"
        @dragend="end"
      >
        <span class="row-sub">{{ item.projectName }}</span>
        <RouterLink
          :to="{ path: '/specs/' + item.specId, query: { return: route.fullPath } }"
          class="board-title"
        >
          {{ item.title }}
        </RouterLink>
        <div class="inline-meta">
          <StatusBadge :status="item.status" />
          <span>{{ stages[item.stage] }}</span>
        </div>
        <p v-if="item.contentStatus" class="card-next">
          {{ contentStatuses[item.contentStatus] }}
          <span v-if="item.needsReconfirmation" class="warning-text">· 需要重新确认</span>
        </p>
        <div class="board-reminders">
          <template v-for="reminder in reminders" :key="reminder.id">
            <RouterLink
              v-if="item.reminderCounts?.[reminder.id]"
              :to="{
                path: '/specs/' + item.specId,
                query: { return: route.fullPath, tab: 'content', category: reminder.id }
              }"
            >
              {{ reminder.label }} {{ item.reminderCounts[reminder.id] }}
            </RouterLink>
          </template>
        </div>
        <p v-if="item.nextAction" class="card-next">{{ item.nextAction }}</p>
        <p v-if="item.status === 'blocked'" class="blocker-text">{{ item.blocker }}</p>
        <p v-if="item.hasSourceProblem" class="warning-text">来源异常</p>
        <p v-if="!item.active" class="muted">已停用，请到详情恢复</p>
        <SingleSelect
          model-value=""
          placeholder="移动到…"
          :aria-label="'移动 ' + item.title + ' 到其他' + (groupBy === 'status' ? '状态' : '阶段')"
          :options="
            Object.entries(labels).map(([value, label]) => ({
              value,
              label,
              disabled: item[groupBy] === value
            }))
          "
          :disabled="!item.active || saving"
          @update:model-value="move(item, groupBy, $event as Stage | Status, revision)"
        />
      </article>
      <Button
        v-if="column.items.length > (limits[column.key] ?? 50)"
        variant="outline"
        @click="limits[column.key] = (limits[column.key] ?? 50) + 50"
      >
        加载更多（{{ column.items.length }}）
      </Button>
      <p v-if="!column.items.length" class="column-empty">暂无 Spec</p>
    </section>
  </div>
  <ModalDialog v-model="open" title="记录阻塞原因" :busy="saving">
    <form class="form-stack" @submit.prevent="confirm">
      <label :for="id + 'blocker'">阻塞说明（必填）</label>
      <textarea :id="id + 'blocker'" v-model="blocker" required rows="3" />
      <p v-if="error" role="alert" class="notice error">{{ error }}</p>
      <div class="form-actions">
        <Button variant="outline" type="button" :disabled="saving" @click="cancel">取消</Button>
        <Button :disabled="saving">确认阻塞</Button>
      </div>
    </form>
  </ModalDialog>
</template>
<style scoped>
.board {
  min-height: 0;
  align-items: start;
}
.board-column {
  min-width: 0;
  padding: 10px;
  background: #f7f8fa;
}
.board-column > header {
  margin-bottom: 8px;
}
.board-card {
  padding: 12px;
  margin-bottom: 8px;
  box-shadow: none;
}
.board-title {
  margin-top: 3px;
  overflow-wrap: anywhere;
}
.inline-meta {
  gap: 10px;
  margin-top: 7px;
}
.card-next {
  margin-top: 7px;
  overflow-wrap: anywhere;
}
.board-reminders {
  display: flex;
  flex-wrap: wrap;
  gap: 5px 8px;
  margin-top: 6px;
}
.board-reminders:empty {
  display: none;
}
.board-reminders a {
  color: #315dcf;
  font-size: 11px;
}
.column-empty {
  padding: 14px 0;
}
.toolbar {
  margin-bottom: 10px;
}
.toolbar > .muted {
  font-size: 11px;
}
</style>
