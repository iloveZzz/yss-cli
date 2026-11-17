<script setup lang="ts">
import { computed, nextTick, shallowRef, toRef, useTemplateRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { dateLabel } from '@/services/workbench'
import { readableValue, type JobEvent } from '@/services/workbench-platform'
import { jobActivity } from '../schemas/job'
import { useJobHistory } from '../hooks/useJobHistory'

const props = defineProps<{
  jobId: string
  events: JobEvent[]
  status: string
  error: string
  complete: boolean
  dropped: number
}>()
const entries = computed(() => jobActivity(props.events))
const log = useTemplateRef<HTMLOListElement>('activity')
const following = shallowRef(true)
const history = useJobHistory(toRef(props, 'jobId'))
const rawOpen = shallowRef(false)
function scrolled() {
  const element = log.value
  if (element)
    following.value = element.scrollHeight - element.scrollTop - element.clientHeight < 80
}
function latest() {
  if (log.value) log.value.scrollTop = log.value.scrollHeight
  following.value = true
}
watch(
  () => props.events.at(-1)?.id,
  async () => {
    if (!following.value) return
    await nextTick()
    latest()
  }
)
function toggleRaw(event: Event) {
  rawOpen.value = (event.target as HTMLDetailsElement).open
  if (rawOpen.value && !history.loaded.value) void history.read(0)
}
</script>

<template>
  <section class="platform-section activity-section">
    <div class="section-heading">
      <h2>消息与操作</h2>
      <span class="muted connection-state">{{ status }}</span>
    </div>
    <p v-if="error" role="alert" class="notice warning">{{ error }}</p>
    <div class="activity-toolbar">
      <span class="muted">{{ dropped ? '正在显示近期记录' : `${events.length} 条运行事件` }}</span>
      <Button type="button" variant="ghost" :disabled="!entries.length" @click="latest">
        {{ following ? '已跟随最新消息' : '跳到最新消息' }}
      </Button>
    </div>
    <ol
      ref="activity"
      class="activity-feed"
      role="list"
      tabindex="0"
      aria-label="近期运行消息与操作"
      @scroll.passive="scrolled"
    >
      <li
        v-for="entry in entries"
        :key="entry.id"
        :class="['activity-entry', `entry-${entry.kind}`]"
      >
        <div class="activity-meta">
          <strong>{{ entry.title }}</strong>
          <span v-if="entry.status && entry.kind === 'command'">{{ entry.status }}</span>
          <time>{{ dateLabel(entry.createdAt) }}</time>
        </div>
        <p
          v-if="entry.text"
          :class="['activity-text', { 'command-text': entry.kind === 'command' }]"
        >
          {{ entry.text }}
        </p>
        <details v-if="entry.output" class="command-output">
          <summary>命令输出</summary>
          <pre>{{ entry.output }}</pre>
        </details>
      </li>
      <li v-if="!entries.length" class="empty">
        {{ complete ? '此执行暂无可展示的消息，可查看原始事件。' : '等待运行消息…' }}
      </li>
    </ol>
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
  </section>
</template>

<style scoped>
.activity-section {
  min-width: 0;
}
.activity-toolbar,
.history-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 12px 0;
  flex-wrap: wrap;
}
.connection-state {
  min-width: 112px;
  text-align: right;
}
.activity-feed {
  list-style: none;
  margin: 0;
  padding: 0;
  max-height: 540px;
  overflow: auto;
  overscroll-behavior: contain;
}
.activity-entry {
  padding: 14px 4px;
  border-bottom: 1px solid var(--yss-colors-border);
}
.activity-entry:last-child {
  border-bottom: 0;
}
.activity-meta {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 8px 12px;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.activity-meta strong {
  color: var(--brand-color-text);
  font-weight: 550;
}
.activity-meta time {
  margin-left: auto;
  font-size: 11px;
}
.activity-text {
  margin: 7px 0 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.75;
}
.command-text {
  font-family: ui-monospace, SFMono-Regular, monospace;
  font-size: 12px;
}
.entry-status {
  padding-block: 10px;
}
.entry-error .activity-meta strong {
  color: var(--yss-colors-error-text);
}
.command-output {
  margin-top: 8px;
}
.command-output pre,
.raw-events pre {
  overflow: auto;
  max-height: 360px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.raw-history {
  border-top: 1px solid var(--yss-colors-border);
  padding-top: 14px;
  margin-top: 14px;
}
.raw-events {
  list-style: none;
  padding: 0;
  margin: 0;
  max-height: 520px;
  overflow: auto;
}
.raw-events > li {
  border-bottom: 1px solid var(--yss-colors-border);
  padding: 9px 0;
}
.raw-events summary {
  display: list-item;
  cursor: pointer;
}
.raw-events time {
  margin-inline-start: 12px;
  color: var(--brand-color-text-secondary);
  font-size: 11px;
}
@media (width < 720px) {
  .activity-feed {
    max-height: 430px;
  }
  .activity-meta time {
    margin-left: 0;
  }
  .raw-events time {
    display: block;
    margin-inline-start: 0;
    margin-top: 4px;
  }
}
</style>
