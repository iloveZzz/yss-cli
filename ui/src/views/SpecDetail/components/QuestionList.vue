<script setup lang="ts">
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import { computed, nextTick, shallowRef, useId, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { currentQuestionResponse, questionHistory, questionState, questionStateLabel, responseUse, reviewDateLabel } from '@/services/workbench-content'
import type { QuestionView } from '@/services/workbench'
import QuestionHistory from './QuestionHistory.vue'
const props = defineProps<{
  specId: string
  sourcePath: string
  items: QuestionView[]
  saving: boolean
}>()
const emit = defineEmits<{
  answer: [item: QuestionView, trigger: HTMLButtonElement]
  copy: [item: QuestionView]
}>()
const route = useRoute()
const id = useId()
const expanded = shallowRef<string[]>([])
const focused = computed(() =>
  typeof route.query.questionId === 'string' ? route.query.questionId : ''
)
const targetAvailable = computed(() =>
  props.items.some((item) => item.question.id === focused.value)
)
let positioned = ''
function toggle(questionId: string) {
  expanded.value = expanded.value.includes(questionId)
    ? expanded.value.filter((value) => value !== questionId)
    : [...expanded.value, questionId]
}
watch(
  [() => props.specId, focused, () => route.query.tab, targetAvailable],
  async (_value, _old, cleanup) => {
    let cancelled = false
    cleanup(() => {
      cancelled = true
    })
    if (route.query.tab !== 'content' || !focused.value) {
      positioned = ''
      return
    }
    const target = `${props.specId}:${focused.value}`
    if (!targetAvailable.value || target === positioned) return
    positioned = target
    if (!expanded.value.includes(focused.value)) expanded.value = [...expanded.value, focused.value]
    await nextTick()
    if (cancelled || target !== `${props.specId}:${focused.value}` || route.query.tab !== 'content')
      return
    const row = document.getElementById(`question-${focused.value}`)
    row?.scrollIntoView({ block: 'start', behavior: 'instant' })
    row?.focus({ preventScroll: true })
  },
  { immediate: true }
)
const rows = computed(() => props.items.map((item) => {
  const response = currentQuestionResponse(item)
  const history = questionHistory(item)
  const state = questionState(item)
  const summary = response
    ? (response.kind === 'answer' ? response.answer : response.reason)
    : history.length
      ? (state === 'overdue' ? '原延期已到期，需重新处理。' : '原答复仅供复查。')
      : item.question.resolution || item.question.recommendation || '尚未答复'
  const actionLabel = response
    ? '修改答复 / 延期'
    : history.length
      ? '复查'
      : item.question.kind === 'research'
        ? '补充 / 延期'
        : '答复 / 延期'
  return { item, state, response, history, summary, actionLabel }
}))
</script>
<template>
  <div class="question-table-wrap panel" tabindex="0" role="region" :aria-labelledby="id">
    <Table class="question-table" role="table">
      <TableCaption :id="id" class="sr-only">问题、答复与延期事项</TableCaption>
      <TableHeader role="rowgroup">
        <TableRow role="row">
          <TableHead scope="col">问题</TableHead>
          <TableHead scope="col">状态</TableHead>
          <TableHead scope="col">涉及范围</TableHead>
          <TableHead scope="col">答复 / 建议摘要</TableHead>
          <TableHead scope="col">操作</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody role="rowgroup">
        <template v-for="{ item, state, response, history, summary, actionLabel } in rows" :key="item.question.id">
          <TableRow
            :id="`question-${item.question.id}`"
            role="row"
            tabindex="-1"
            :class="['question-row', { 'question-selected': focused === item.question.id }]"
          >
            <TableHead scope="row" role="rowheader" class="question-title">
              <Button
                type="button"
                variant="ghost"
                :aria-expanded="expanded.includes(item.question.id)"
                :aria-controls="`${id}-${item.question.id}`"
                @click="toggle(item.question.id)"
              >
                <span class="question-id">{{ item.question.id }}</span>
                <strong>{{ item.question.title }}</strong>
                <span class="expand-label">
                  {{ expanded.includes(item.question.id) ? '收起详情' : '展开详情' }}
                  <span aria-hidden="true">
                    {{ expanded.includes(item.question.id) ? '−' : '+' }}
                  </span>
                </span>
              </Button>
            </TableHead>
            <TableCell role="cell" data-label="状态">
              <Badge variant="secondary" class="question-state" :data-state="state">
                {{ questionStateLabel(item) }}
              </Badge>
              <span v-if="item.question.blocking" class="blocking-label">影响本次确认</span>
            </TableCell>
            <TableCell role="cell" data-label="涉及范围" class="question-scope">
              <p :title="item.question.affectedScope">
                {{ item.question.affectedScope || '未说明' }}
              </p>
            </TableCell>
            <TableCell role="cell" data-label="答复摘要" class="question-response">
              <span v-if="response" class="summary-label">{{ responseUse(item, response) }}</span>
              <p :title="summary">{{ summary }}</p>
            </TableCell>
            <TableCell role="cell" data-label="操作" class="question-actions">
              <Button
                v-if="state !== 'resolved' || response"
                type="button"
                variant="outline"
                :disabled="saving"
                :aria-label="`${actionLabel}：${item.question.title}`"
                @click="emit('answer', item, $event.currentTarget as HTMLButtonElement)"
              >
                {{ actionLabel }}
              </Button>
              <Button
                v-if="response"
                type="button"
                variant="ghost"
                :disabled="saving"
                :aria-label="`复制有效答复：${item.question.title}`"
                @click="emit('copy', item)"
              >
                复制有效答复
              </Button>
            </TableCell>
          </TableRow>
          <TableRow
            v-if="expanded.includes(item.question.id)"
            :id="`${id}-${item.question.id}`"
            role="row"
            class="question-expanded"
          >
            <TableCell role="cell" colspan="5">
              <div class="question-details-grid">
                <section>
                  <h3>问题背景</h3>
                  <dl>
                    <div>
                      <dt>涉及范围</dt>
                      <dd>{{ item.question.affectedScope || '未说明' }}</dd>
                    </div>
                    <div>
                      <dt>影响</dt>
                      <dd>{{ item.question.impact || '未说明' }}</dd>
                    </div>
                    <div>
                      <dt>推荐答案</dt>
                      <dd>{{ item.question.recommendation || '暂无建议，请补充依据后判断。' }}</dd>
                    </div>
                  </dl>
                </section>
                <section v-if="response">
                  <h3>
                    {{ responseUse(item, response) }}
                  </h3>
                  <p class="path-text">答复 ID · {{ response.id }}</p>
                  <p class="preserve-lines">
                    {{ response.kind === 'answer' ? response.answer : response.reason }}
                  </p>
                  <dl v-if="response.kind === 'defer'">
                    <div>
                      <dt>影响</dt>
                      <dd>{{ response.impact }}</dd>
                    </div>
                    <div>
                      <dt>本次排除</dt>
                      <dd>{{ response.excludedScope }}</dd>
                    </div>
                    <div>
                      <dt>复查日期</dt>
                      <dd>{{ response.reviewAt ? reviewDateLabel(response) : '未设日期' }}</dd>
                    </div>
                    <div v-if="response.reviewWhen">
                      <dt>触发条件</dt>
                      <dd>{{ response.reviewWhen }} · 请核对条件是否已触发</dd>
                    </div>
                  </dl>
                </section>
              </div>
              <QuestionHistory :entries="history" />
              <p v-if="state === 'needsReview'" class="warning-text">
                问题内容或延期安排需重新核对，原答复不会作为当前回复复制。
              </p>
              <p v-if="state === 'overdue'" class="warning-text">
                延期已到期，请复查后重新答复或安排延期。
              </p>
              <p v-if="state === 'pendingWriteback'" class="muted">待 Agent 回写并核验。</p>
              <p v-if="item.question.resolution" class="preserve-lines resolution-text">
                {{ state === 'resolved' || state === 'deferred' ? '已回写结论' : '来源中的既有记录，供复查' }} ·
                {{ item.question.resolution }}
              </p>
              <p class="path-text">{{ sourcePath }} · {{ item.question.id }}</p>
            </TableCell>
          </TableRow>
        </template>
      </TableBody>
    </Table>
  </div>
</template>
<style scoped>
.question-table-wrap {
  container-type: inline-size;
  overflow: auto;
  min-width: 0;
}
.question-table {
  width: 100%;
  border-collapse: separate;
  border-spacing: 0;
  font-size: 12px;
  text-align: left;
}
.question-table th,
.question-table td {
  padding: 12px 14px;
  vertical-align: top;
  border-bottom: 1px solid #edf0f4;
}
.question-table thead th {
  color: var(--brand-color-text-secondary);
  background: #fafbfc;
  font-size: 11px;
  font-weight: 500;
  white-space: nowrap;
}
.question-row {
  scroll-margin-block-start: 8px;
}
.question-row:last-child > * {
  border-bottom: 0;
}
.question-row.question-selected > * {
  background: #f2f6ff;
}
.question-title {
  min-width: 190px;
  max-width: 290px;
  font-weight: 400;
}
.question-title > button {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  width: 100%;
  height: auto;
  white-space: normal;
  padding: 0;
  text-align: left;
  border: 0;
  background: none;
  overflow-wrap: anywhere;
}
.question-title strong {
  font-weight: 550;
  line-height: 1.6;
}
.question-id,
.expand-label {
  color: var(--brand-color-text-secondary);
  font-size: 10px;
}
.expand-label {
  margin-top: 3px;
}
.question-title button:hover strong {
  color: #315dcf;
}
.question-state {
  display: inline-block;
  color: #96601e;
  background: #fff5e5;
  border-radius: 5px;
  padding: 3px 6px;
  font-size: 11px;
  white-space: nowrap;
}
.question-state[data-state='resolved'] {
  color: #278064;
  background: #edf7f2;
}
.question-state[data-state='pendingWriteback'] {
  color: #315dcf;
  background: #eef3ff;
}
.blocking-label {
  display: block;
  color: #96601e;
  margin-top: 6px;
  font-size: 10px;
}
.question-scope {
  min-width: 125px;
  max-width: 210px;
}
.question-response {
  min-width: 170px;
  max-width: 300px;
}
.question-scope p,
.question-response p {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  line-height: 1.7;
}
.summary-label {
  display: block;
  margin-bottom: 3px;
  color: var(--brand-color-text-secondary);
  font-size: 10px;
}
.question-actions {
  min-width: 145px;
}
.question-actions button {
  display: flex;
  width: fit-content;
  margin-bottom: 5px;
  white-space: nowrap;
}
.question-expanded > td {
  padding: 16px 20px;
  background: #fafbfc;
}
.question-details-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 20px;
}
.question-details-grid section {
  min-width: 0;
}
.question-expanded h3 {
  margin-bottom: 10px;
  font-size: 12px;
}
.question-expanded dl {
  margin: 0;
}
.question-expanded dl > div {
  display: grid;
  grid-template-columns: 72px minmax(0, 1fr);
  gap: 10px;
  margin: 7px 0;
}
.question-expanded dt {
  color: var(--brand-color-text-secondary);
}
.question-expanded dd {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.question-expanded p {
  margin: 8px 0;
  overflow-wrap: anywhere;
}
.resolution-text {
  border-top: 1px solid #e4e8f0;
  padding-top: 10px;
}
@container (width<820px) {
  .question-table,
  .question-table tbody {
    display: block;
  }
  .question-table thead {
    display: none;
  }
  .question-row {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    border-bottom: 1px solid #e4e8ef;
    padding: 12px 14px;
    gap: 12px 16px;
  }
  .question-table .question-row > * {
    display: block;
    padding: 0;
    border: 0;
    min-width: 0;
    max-width: none;
  }
  .question-title {
    grid-column: 1/-1;
  }
  .question-title > button {
    display: grid;
    grid-template-columns: auto 1fr;
    column-gap: 10px;
  }
  .question-title .expand-label {
    grid-column: 2;
  }
  .question-row td::before {
    content: attr(data-label);
    display: block;
    color: var(--brand-color-text-secondary);
    font-size: 10px;
    margin-bottom: 5px;
  }
  .question-actions {
    grid-column: 1/-1;
  }
  .question-table .question-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    align-items: center;
  }
  .question-actions::before {
    display: none !important;
  }
  .question-actions button {
    margin: 0;
    min-height: max(36px, var(--yss-control-min-height));
  }
  .question-expanded {
    display: block;
  }
  .question-expanded > td {
    display: block;
  }
  .question-details-grid {
    grid-template-columns: 1fr;
    gap: 16px;
  }
}
</style>
