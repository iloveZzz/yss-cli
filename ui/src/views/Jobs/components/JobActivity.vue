<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, shallowRef, useTemplateRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { dateLabel, errorMessage } from '@/services/workbench'
import { getJobActivities, type ActivityPage } from '@/services/workbench-platform'
import { activityLabels, stepResult } from '../schemas/job'
import ActivityDetail from './ActivityDetail.vue'
import RawJobEvents from './RawJobEvents.vue'

const props = defineProps<{
  jobId: string
  latest?: ActivityPage
  status: string
  error: string
  visible: boolean
}>()
const emit = defineEmits<{ retry: [] }>()
const log = useTemplateRef<HTMLElement>('log')
const bounds = shallowRef<number[]>([])
const history = shallowRef<ActivityPage>()
const frozen = shallowRef<ActivityPage>()
const open = shallowRef<number>()
const busy = shallowRef(false)
const historyError = shallowRef('')
const page = computed(() => history.value ?? frozen.value ?? props.latest)
const following = computed(() => !history.value && !frozen.value)
const rows = computed(() => page.value?.items ?? [])
let controller: AbortController | undefined
onBeforeUnmount(() => controller?.abort())
async function navigate(nextBounds: number[]) {
  controller?.abort()
  const request = new AbortController()
  controller = request
  busy.value = true
  historyError.value = ''
  try {
    const before = nextBounds.at(-1)
    const result =
      before === undefined ? undefined : await getJobActivities(props.jobId, before, request.signal)
    if (request.signal.aborted) return
    bounds.value = nextBounds
    history.value = result
    frozen.value = undefined
    open.value = undefined
    await nextTick()
    if (log.value) log.value.scrollTop = result ? 0 : log.value.scrollHeight
  } catch (cause) {
    if (!request.signal.aborted) historyError.value = errorMessage(cause)
  } finally {
    if (!request.signal.aborted) busy.value = false
  }
}
function older() {
  const first = rows.value[0]
  if (first) void navigate([...bounds.value, first.id])
}
function scrolled() {
  const el = log.value
  if (!el || !props.visible || history.value || busy.value) return
  if (el.scrollHeight - el.scrollTop - el.clientHeight > 24) frozen.value ??= props.latest
  else if (!open.value) frozen.value = undefined
}
function toggle(id: number) {
  open.value = open.value === id ? undefined : id
  if (open.value && !history.value) frozen.value ??= props.latest
  else scrolled()
}
// Keep the same page boundaries while reading. An older, long-running command may
// still receive output after it has fallen off the latest page.
watch(
  () => [props.latest?.cursor, props.visible],
  async (_, __, onCleanup) => {
    if (!props.visible || busy.value) return
    if (following.value) {
      await nextTick()
      if (log.value) log.value.scrollTop = log.value.scrollHeight
      return
    }
    const pinned = history.value ?? frozen.value
    const last = pinned?.items.at(-1)
    if (!last || pinned?.cursor === props.latest?.cursor) return
    const request = new AbortController()
    onCleanup(() => request.abort())
    try {
      const result = await getJobActivities(props.jobId, last.id + 1, request.signal)
      if (request.signal.aborted) return
      if (history.value === pinned) history.value = result
      else if (frozen.value === pinned) frozen.value = result
    } catch (cause) {
      if (!request.signal.aborted) historyError.value = errorMessage(cause)
    }
  },
  { immediate: true, flush: 'post' }
)
function summary(text: string, command: boolean) {
  if (!command) return text
  const shell = /^(?:\/[^ ]*\/)?(?:zsh|bash|sh) -[a-z]*c\s+/.exec(text)
  if (!shell) return text
  const body = text.slice(shell[0].length)
  const quote = body[0]
  if (quote !== '"' && quote !== "'") return body
  return body.endsWith(quote) ? body.slice(1, -1) : body.slice(1)
}
</script>
<template>
  <section class="platform-section log-section">
    <div class="section-heading">
      <div class="log-heading">
        <h2>执行活动</h2>
        <span v-if="page" class="muted">{{ page.total }} 条</span>
      </div>
      <span class="muted">{{ status }}</span>
    </div>
    <p v-if="error || historyError" role="alert" class="notice warning">
      {{ error || historyError }}
      <Button v-if="error" type="button" variant="ghost" @click="emit('retry')">重新连接</Button>
    </p>
    <div
      ref="log"
      class="activity-scroll"
      role="region"
      aria-label="执行活动列表"
      tabindex="0"
      @scroll.passive="scrolled"
    >
      <table class="activity-table">
        <caption class="sr-only">
          本轮执行活动，按时间顺序排列
        </caption>
        <colgroup>
          <col class="time-col" />
          <col class="type-col" />
          <col />
          <col class="result-col" />
        </colgroup>
        <thead>
          <tr>
            <th scope="col">时间</th>
            <th scope="col">活动</th>
            <th scope="col">内容摘要</th>
            <th scope="col">单步结果</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="row in rows" :key="row.id">
            <tr>
              <td>
                <time :datetime="row.createdAt" :title="dateLabel(row.createdAt)">{{
                  dateLabel(row.createdAt).split(' ').at(-1)
                }}</time>
              </td>
              <th scope="row">{{ activityLabels[row.kind] }}</th>
              <td class="summary-cell">
                <button
                  type="button"
                  :aria-expanded="open === row.id"
                  :aria-label="`${open === row.id ? '收起' : '展开'}${activityLabels[row.kind]}：${row.text.slice(0, 80)}`"
                  @click="toggle(row.id)"
                >
                  <span aria-hidden="true" class="chevron">{{ open === row.id ? '▾' : '▸' }}</span>
                  <span class="summary-text" :class="{ mono: row.kind === 'command' }">{{
                    summary(row.text, row.kind === 'command') || '查看详情'
                  }}</span>
                </button>
              </td>
              <td
                :class="{
                  'warning-text':
                    (row.exitCode != null && row.exitCode !== 0) || row.kind === 'error'
                }"
              >
                {{ stepResult(row) }}
              </td>
            </tr>
            <tr v-if="open === row.id">
              <td colspan="4" class="activity-detail">
                <ActivityDetail :job-id="jobId" :row="row" />
              </td>
            </tr>
          </template>
        </tbody>
      </table>
      <p v-if="!rows.length" class="empty">
        {{ latest?.terminal ? '此执行暂无可展示的活动' : '等待运行活动…' }}
      </p>
    </div>
    <nav class="log-nav" aria-label="执行活动分页">
      <Button type="button" variant="outline" :disabled="busy || !page?.hasOlder" @click="older"
        >较早活动</Button
      >
      <Button
        v-if="bounds.length"
        type="button"
        variant="outline"
        :disabled="busy"
        @click="navigate(bounds.slice(0, -1))"
        >较新活动</Button
      >
      <Button v-if="!following" type="button" variant="ghost" @click="navigate([])"
        >回到最新活动</Button
      >
    </nav>
    <RawJobEvents :job-id="jobId" />
  </section>
</template>
<style scoped>
.log-section {
  min-width: 0;
}
.log-heading,
.log-nav {
  display: flex;
  align-items: center;
  gap: 12px;
}
.log-nav {
  margin-top: 12px;
}
.activity-scroll {
  max-height: min(54vh, 540px);
  overflow: auto;
  overscroll-behavior: contain;
  overflow-anchor: none;
}
.activity-table {
  width: 100%;
  min-width: 540px;
  table-layout: fixed;
  border-spacing: 0;
}
.time-col {
  width: 92px;
}
.type-col {
  width: 90px;
}
.result-col {
  width: 90px;
}
.activity-table th,
.activity-table td {
  padding: 5px 10px;
  height: 40px;
  text-align: left;
  font-weight: 400;
  border-bottom: 1px solid var(--yss-colors-border);
}
.activity-table thead th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: #f7f8fa;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.activity-table time,
.mono {
  font-variant-numeric: tabular-nums;
}
.mono {
  font-family: ui-monospace, monospace;
  font-size: 12px;
}
.summary-cell button {
  display: flex;
  align-items: center;
  width: 100%;
  text-align: left;
  gap: 8px;
  min-height: 32px;
  cursor: pointer;
}
.chevron {
  color: var(--brand-color-text-secondary);
}
.summary-text {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.activity-table .activity-detail {
  background: #f7f8fa;
  padding: 12px 16px;
  white-space: normal;
}
@media (width < 720px) {
  .summary-cell button {
    min-height: 44px;
  }
}
</style>
