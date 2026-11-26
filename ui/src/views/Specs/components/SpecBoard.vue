<script setup lang="ts">
import { Alert } from '@/components/ui/alert'
import { Checkbox } from '@/components/ui/checkbox'
import { Textarea } from '@/components/ui/textarea'
import { computed, shallowRef, watch, useId, nextTick, useTemplateRef } from 'vue'
import { useRoute } from 'vue-router'
import {
  stages,
  statuses,
  contentStatuses,
  type GroupBy,
  type Stage,
  type Status
} from '@/services/workbench'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import TableLoadingOverlay from '@/components/TableLoadingOverlay.vue'
import SingleSelect from '@/components/SingleSelect.vue'
import ModalDialog from '@/components/ModalDialog.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import ProgressSummary from '@/components/ProgressSummary.vue'
import ProgressOverrideForm from '@/components/ProgressOverrideForm.vue'
import type { CompactWorkItem } from '@/services/workbench-read'
import { contentReminders } from '@/services/workbench-content'
import { useWorkActions } from '../hooks/useWorkActions'
const props = defineProps<{
  items: CompactWorkItem[]
  columnTotals: Record<string, number>
  groupBy: GroupBy
  revision: number
  progressBasis: 'effective' | 'personal'
  readonlyResults?: boolean
  busy?: boolean
  refreshing?: boolean
  refreshAfter: (revision?: number) => Promise<unknown>
}>()
const emit = defineEmits<{ loadMore: [column: string] }>()
const route = useRoute()
const id = useId()
const { move, confirm, cancel, open, blocker, error, notice, saving, overrideOpen, correction, corrected, pending, stale, reloadBlocker } =
  useWorkActions(() => props.progressBasis, (revision) => props.refreshAfter(revision))
const correctionItem = computed(() => props.items.find((item) => item.id === correction.value?.item.id))
const blockerItem = computed(() => props.items.find((item) => item.id === pending.value?.item.id))
const board = useTemplateRef<HTMLElement>('board')
watch(notice, async (value) => {
  if (value) {
    await nextTick()
    if (document.activeElement === document.body) board.value?.focus()
  }
})
const scrollLeft = defineModel<number>('scrollLeft', { required: true })
watch(
  [board, scrollLeft],
  () => {
    if (board.value) board.value.scrollLeft = scrollLeft.value
  },
  { flush: 'post' }
)
const hideEmpty = shallowRef(true)
const reminders = contentReminders(undefined).filter((reminder) => reminder.id !== 'ready')
const dragging = shallowRef<{ item: CompactWorkItem; revision: number; groupBy: GroupBy }>()
const hovered = shallowRef('')
const labels = computed(() => (props.groupBy === 'status' ? statuses : stages))
const columns = computed(() =>
  Object.entries(labels.value)
    .map(([key, label]) => ({
      key,
      label,
      total: props.columnTotals[key] ?? 0,
      items: props.items.filter((i) => i[props.groupBy] === key)
    }))
    .filter((c) => !hideEmpty.value || c.total > 0)
)
watch([() => props.items, () => props.groupBy, () => props.readonlyResults], end)
function start(event: DragEvent, item: CompactWorkItem) {
  if (props.readonlyResults || !item.active || saving.value) {
    event.preventDefault()
    return
  }
  dragging.value = { item, revision: props.revision, groupBy: props.groupBy }
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', item.id)
  }
}
function moveCard(item: CompactWorkItem, value: Stage | Status) {
  if (props.readonlyResults || !item.active) return
  return move(item, props.groupBy, value, props.revision)
}
async function drop(key: string) {
  const current = dragging.value
  dragging.value = undefined
  hovered.value = ''
  if (!props.readonlyResults && current && current.groupBy === props.groupBy)
    await move(current.item, props.groupBy, key as Stage | Status, current.revision)
}
function end() {
  dragging.value = undefined
  hovered.value = ''
}
</script>
<template>
  <div v-if="items.length" class="toolbar">
    <label class="check-label">
      <Checkbox :model-value="hideEmpty" @update:model-value="hideEmpty = $event === true" />
      隐藏空列
    </label>
    <span class="muted">可拖动卡片，或用“移动到…”菜单；不改变列内排序。</span>
  </div>
  <p v-if="notice" role="status" class="notice">{{ notice }}</p>
  <Alert v-if="error && !open" role="alert" class="notice error">{{ error }}</Alert>
  <div v-if="items.length" class="relative">
    <div
      ref="board"
      tabindex="0"
      role="region"
      :aria-busy="busy"
      class="board"
      data-workbench-reveal
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
          <Badge variant="secondary" class="count">{{ column.total }}</Badge>
        </header>
        <TransitionGroup name="workbench-list" tag="div" class="board-cards">
        <Card
          v-for="item in column.items"
          :key="item.id"
          :data-spec-id="item.specId"
          class="board-card block shadow-none"
          :draggable="item.active && !saving && !readonlyResults"
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
          <RouterLink
            v-if="item.contentStatus"
            :to="{ path: '/specs/' + item.specId, query: { return: route.fullPath, tab: 'content', category: 'ready' } }"
            :class="['card-next', { 'warning-text': item.needsReconfirmation || item.materialReview }]"
          >
            {{ item.needsReconfirmation ? '新版待确认' : item.materialReview
              ? '材料待核实' : contentStatuses[item.contentStatus] }}
          </RouterLink>
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
          <ProgressSummary v-if="progressBasis === 'effective'" :progress="item.progress" compact />
          <p v-if="progressBasis === 'personal' && item.nextAction" class="card-next">{{ item.nextAction }}</p>
          <p v-if="progressBasis === 'personal' && item.status === 'blocked'" class="blocker-text">{{ item.blocker }}</p>
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
            :disabled="!item.active || saving || readonlyResults"
            @update:model-value="moveCard(item, $event as Stage | Status)"
          />
        </Card>
        </TransitionGroup>
        <Button
          v-if="column.items.length < column.total"
          variant="outline"
          :disabled="busy || readonlyResults"
          @click="emit('loadMore', column.key)"
        >
          加载更多（{{ column.items.length }} / {{ column.total }}）
        </Button>
        <p v-if="!column.items.length" class="column-empty">暂无 Spec</p>
      </section>
      <TableLoadingOverlay :active="!!refreshing" label="正在刷新看板" />
    </div>
  </div>
  <ProgressOverrideForm v-if="correction" v-model="overrideOpen" :spec-id="correction.item.specId"
    :progress="correctionItem?.progress ?? correction.item.progress" :stage="correction.stage"
    :initial-progress="correction.progress" :expected-revision="revision" :refresh-after="refreshAfter"
    :write-disabled="readonlyResults || !correctionItem?.active" @saved="corrected" />
  <ModalDialog v-model="open" title="记录阻塞原因" :busy="saving">
    <form class="form-stack" @submit.prevent="confirm">
      <label :for="id + 'blocker'">阻塞说明（必填）</label>
      <Textarea :id="id + 'blocker'" v-model="blocker" required rows="3" />
      <Alert v-if="error" role="alert" class="notice error">{{ error }}</Alert>
      <Button v-if="stale" type="button" variant="outline" :disabled="readonlyResults || !blockerItem?.active"
        @click="blockerItem && reloadBlocker(blockerItem, revision)">
        采用最新记录，保留阻塞说明
      </Button>
      <div class="form-actions">
        <Button variant="outline" type="button" :disabled="saving" @click="cancel">取消</Button>
        <Button :disabled="saving || stale || readonlyResults || !blockerItem?.active">
          <LoadingIndicator :active="saving" />
          {{ saving ? '正在记录…' : '确认阻塞' }}
        </Button>
      </div>
    </form>
  </ModalDialog>
</template>
<style scoped>
.board { position: relative; }
.board-cards { min-width: 0; }
.board {
  min-height: 0;
  align-items: start;
  scroll-padding-inline: 10px;
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
@media (width < 720px) {
  .board {
    scroll-snap-type: x proximity;
  }
  .board-column {
    scroll-snap-align: start;
    max-width: calc(100vw - 48px);
  }
  .toolbar {
    flex-wrap: wrap;
    gap: 4px 12px;
  }
}
</style>
