<script setup lang="ts">
import { computed, shallowRef, useId, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { contentReminders, matchesContentReminder } from '@/services/workbench-content'
import { useWorkbench } from '@/hooks/useWorkbench'
import type { SpecView } from '@/services/workbench'
const props = defineProps<{ specs: readonly SpecView[] }>()
const route = useRoute()
const router = useRouter()
const { snapshot } = useWorkbench()
const id = useId()
const categories = new Set(contentReminders(undefined).map((group) => group.id))
function category(value: unknown) {
  return typeof value === 'string' && categories.has(value) ? value : undefined
}
const selected = shallowRef(category(route.query.attention) ?? 'decision')
const touched = shallowRef(!!category(route.query.attention))
function select(kind: string) {
  selected.value = kind
  touched.value = true
  void router.replace({ query: { ...route.query, attention: kind } })
}
const summaries = computed(() =>
  props.specs.map((spec) => ({ spec, reminders: contentReminders(spec.content) }))
)
const counts = computed(() =>
  contentReminders(undefined).map((group) => ({
    ...group,
    count: summaries.value.reduce(
      (sum, item) =>
        sum + (item.reminders.find((reminder) => reminder.id === group.id)?.count ?? 0),
      0
    )
  }))
)
const selectedLabel = computed(
  () => counts.value.find((group) => group.id === selected.value)?.label ?? '内容待办'
)
const hasReminders = computed(() => counts.value.some((group) => group.count > 0))
const items = computed(() =>
  summaries.value.flatMap(({ spec, reminders }) => {
    const count = reminders.find((group) => group.id === selected.value)?.count ?? 0
    return count
      ? [
          {
            spec,
            count,
            projectName:
              snapshot.value?.projects.find(
                (project) => project.record.id === spec.record.projectId
              )?.record.name ?? '未知工程',
            questions: spec.content.questions
              .filter((item) => matchesContentReminder(item, selected.value))
              .slice(0, 3)
          }
        ]
      : []
  })
)
watch(
  () => route.query.attention,
  (value) => {
    const next = category(value)
    touched.value = !!next
    selected.value = next ?? counts.value.find((group) => group.count)?.id ?? 'decision'
  }
)
watch(
  counts,
  (value) => {
    if (!touched.value && !value.some((group) => group.id === selected.value && group.count))
      selected.value = value.find((group) => group.count)?.id ?? 'decision'
  },
  { immediate: true }
)
function location(specId: string, questionId?: string) {
  return {
    path: `/specs/${specId}`,
    query: { return: route.fullPath, tab: 'content', category: selected.value, questionId }
  }
}
</script>
<template>
  <section v-if="!hasReminders" class="attention-zero">
    <h2>内容待办</h2>
    <span>当前没有待你处理的内容事项</span>
    <RouterLink to="/specs">查看 Spec →</RouterLink>
  </section>
  <section v-else class="work-section content-attention" :aria-labelledby="id">
    <div class="section-heading">
      <h2 :id="id">需要你处理</h2>
      <RouterLink :to="{ path: '/specs', query: { reminder: selected } }">
        查看筛选列表 →
      </RouterLink>
    </div>
    <div class="attention-filters" role="group" aria-label="内容提醒类别">
      <button
        v-for="group in counts"
        :key="group.id"
        type="button"
        :aria-pressed="selected === group.id"
        @click="select(group.id)"
      >
        {{ group.label }}
        <span>{{ group.count }}</span>
      </button>
    </div>
    <div
      v-if="items.length"
      class="panel table-scroll attention-table"
      tabindex="0"
      role="region"
      :aria-label="selectedLabel + '事项'"
    >
      <table class="spec-table">
        <caption class="sr-only">{{ selectedLabel }}事项</caption>
        <thead>
          <tr>
            <th scope="col">Spec / 工程</th>
            <th scope="col">待处理事项</th>
            <th scope="col">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in items" :key="item.spec.record.id">
            <th scope="row">
              <RouterLink :to="location(item.spec.record.id)" :title="item.spec.record.sourcePath">
                {{ item.spec.record.title }}
              </RouterLink>
              <span class="row-sub">{{ item.projectName }}</span>
            </th>
            <td>
              <ul v-if="item.questions.length" class="attention-questions">
                <li v-for="question in item.questions" :key="question.question.id">
                  <RouterLink :to="location(item.spec.record.id, question.question.id)">
                    {{ question.question.title }}
                  </RouterLink>
                  <span v-if="question.question.blocking" class="blocking-label">阻塞</span>
                </li>
              </ul>
              <p v-else class="muted">主规格已具备确认条件</p>
              <RouterLink
                v-if="item.count > item.questions.length && item.questions.length"
                :to="location(item.spec.record.id)"
                class="more-questions"
              >
                查看其余 {{ item.count - item.questions.length }} 项
              </RouterLink>
            </td>
            <td class="attention-action">
              <RouterLink :to="location(item.spec.record.id)">
                {{
                  selected === 'ready'
                    ? '检查并确认'
                    : selected === 'writeback'
                      ? '核对回写'
                      : '处理事项'
                }}
                <span>{{ item.count }} →</span>
              </RouterLink>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else class="attention-empty">当前没有{{ selectedLabel }}事项</p>
    <details class="reminder-help">
      <summary>提醒规则</summary>
      <p>
        答复保存后进入待回写。到期或设有复查条件的延期列入需要复查，条件是否满足由你核对。提醒随工作台读取更新，不发送后台通知。
      </p>
    </details>
  </section>
</template>
<style scoped>
.attention-zero {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 8px 16px;
  padding: 12px 0 20px;
  border-bottom: 1px solid var(--yss-colors-border);
  margin-bottom: 20px;
}
.attention-zero h2 {
  font-size: 13px;
  font-weight: 550;
}
.attention-zero span,
.attention-empty {
  color: #717780;
  font-size: 12px;
}
.attention-zero a {
  margin-left: auto;
  color: #315dcf;
  font-size: 12px;
}
.attention-filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-bottom: 12px;
}
.attention-filters button {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 32px;
  padding: 5px 10px;
  border: 1px solid #e1e5ed;
  border-radius: 7px;
  color: #62666c;
  background: #fff;
  font-size: 12px;
}
.attention-filters button[aria-pressed='true'] {
  border-color: #c6d4f7;
  background: #eef3ff;
  color: #315dcf;
}
.attention-filters span {
  font-size: 11px;
  color: #858d99;
}
.attention-table table {
  min-width: 650px;
  border-collapse: separate;
  border-spacing: 0;
}
.attention-table tbody th {
  position: sticky;
  inset-inline-start: 0;
  z-index: 1;
  min-width: 190px;
  max-width: 280px;
  background: #fff;
  text-align: left;
  font-weight: 400;
  vertical-align: top;
  padding: 13px 16px;
}
.attention-table tbody th > a {
  display: block;
  font-size: 12px;
  font-weight: 550;
  overflow-wrap: anywhere;
}
.attention-table td {
  vertical-align: top;
}
.attention-questions {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 7px;
}
.attention-questions li {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.attention-questions a {
  color: #315dcf;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.blocking-label {
  flex: none;
  color: #9b5a0e;
  font-size: 10px;
}
.more-questions {
  display: inline-block;
  margin-top: 8px;
  color: #717780;
  font-size: 11px;
}
.attention-action {
  white-space: nowrap;
  font-size: 12px;
  color: #315dcf;
}
.attention-action span {
  font-size: 11px;
}
.attention-empty {
  padding: 14px 0;
}
.reminder-help {
  margin-top: 10px;
  color: #858d99;
  font-size: 11px;
}
.reminder-help summary {
  cursor: pointer;
  width: fit-content;
}
.reminder-help p {
  max-width: 760px;
  margin-top: 8px;
  line-height: 1.8;
}
@media (width<720px) {
  .attention-filters button {
    min-height: 40px;
  }
  .attention-table tbody th {
    min-width: 160px;
    max-width: 210px;
  }
  .attention-zero a {
    margin-left: 0;
  }
}
</style>
