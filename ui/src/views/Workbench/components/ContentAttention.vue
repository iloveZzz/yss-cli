<script setup lang="ts">
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import { computed, shallowRef, useId, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Badge } from '@/components/ui/badge'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { contentReminders } from '@/services/workbench-content'
import type { WorkbenchOverview } from '@/services/workbench-read'
const props = defineProps<{ reminders: WorkbenchOverview['reminders'] }>()
const route = useRoute()
const router = useRouter()
const id = useId()
const categories = new Set(contentReminders(undefined).map((group) => group.id))
function category(value: unknown) {
  return typeof value === 'string' && categories.has(value) ? value : undefined
}
const selected = shallowRef(category(route.query.attention) ?? 'decision')
const touched = shallowRef(!!category(route.query.attention))
function select(kind: string | number) {
  if (typeof kind !== 'string' || !categories.has(kind) || kind === route.query.attention) return
  selected.value = kind
  touched.value = true
  void router.replace({ query: { ...route.query, attention: kind } })
}
const counts = computed(() =>
  contentReminders(undefined).map((group) => ({
    ...group,
    count: props.reminders[group.id]?.total ?? 0
  }))
)
const selectedLabel = computed(
  () => counts.value.find((group) => group.id === selected.value)?.label ?? '内容待办'
)
const hasReminders = computed(() => counts.value.some((group) => group.count > 0))
const items = computed(() => props.reminders[selected.value]?.items ?? [])
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
      <RouterLink :to="{ path: '/specs', query: { reminder: selected, progressBasis: 'effective', showClosed: 'true' } }">
        查看筛选列表 →
      </RouterLink>
    </div>
    <Tabs class="panel home-tab-panel" :model-value="selected" activation-mode="manual" @update:model-value="select">
      <TabsList class="attention-filters home-tabs" aria-label="内容提醒类别">
        <TabsTrigger v-for="group in counts" :key="group.id" :value="group.id">
          {{ group.label }}
          <Badge variant="secondary" class="tab-count">{{ group.count }}</Badge>
        </TabsTrigger>
      </TabsList>
      <TabsContent v-for="group in counts" :key="group.id" :value="group.id" class="min-w-0">
        <div
          v-if="items.length"
          class="table-scroll attention-table"
          tabindex="0"
          role="region"
          :aria-label="selectedLabel + '事项'"
        >
          <Table class="spec-table">
            <TableCaption class="sr-only">{{ selectedLabel }}事项</TableCaption>
            <TableHeader>
              <TableRow>
                <TableHead scope="col">Spec / 工程</TableHead>
                <TableHead scope="col">待处理事项</TableHead>
                <TableHead scope="col">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="item in items" :key="item.specId">
                <TableHead scope="row">
                  <RouterLink :to="location(item.specId)" :title="item.sourcePath">
                    {{ item.title }}
                  </RouterLink>
                  <span class="row-sub">{{ item.projectName }}</span>
                </TableHead>
                <TableCell>
                  <ul v-if="item.questions.length" class="attention-questions">
                    <li v-for="question in item.questions" :key="question.id">
                      <RouterLink :to="location(item.specId, question.id)">
                        {{ question.title }}
                      </RouterLink>
                      <Badge v-if="question.blocking" variant="outline" class="blocking-label">阻塞</Badge>
                    </li>
                  </ul>
                  <p v-else class="muted">主规格已具备确认条件</p>
                  <RouterLink
                    v-if="item.count > item.questions.length && item.questions.length"
                    :to="location(item.specId)"
                    class="more-questions"
                  >
                    查看其余 {{ item.count - item.questions.length }} 项
                  </RouterLink>
                </TableCell>
                <TableCell class="attention-action">
                  <RouterLink :to="location(item.specId)">
                    {{
                      selected === 'ready'
                        ? '检查并确认'
                        : selected === 'writeback'
                          ? '核对回写'
                          : '处理事项'
                    }}
                    <span>{{ item.count }} →</span>
                  </RouterLink>
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>
        <Empty v-else class="attention-empty">
          <EmptyDescription>当前没有{{ selectedLabel }}事项</EmptyDescription>
        </Empty>
      </TabsContent>
    </Tabs>
    <Collapsible class="reminder-help">
      <CollapsibleTrigger>提醒规则</CollapsibleTrigger>
      <CollapsibleContent>
        <p>
          答复保存后进入待回写。到期或设有复查条件的延期列入需要复查，条件是否满足由你核对。提醒随工作台读取更新，不发送后台通知。
        </p>
      </CollapsibleContent>
    </Collapsible>
  </section>
</template>
<style scoped>
.attention-zero {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 8px 16px;
  padding-block: 8px;
  border-block-end: 1px solid var(--yss-colors-border);
}
.attention-zero h2 {
  font-size: 13px;
  font-weight: 550;
}
.attention-zero span,
.attention-empty {
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.attention-zero a {
  margin-left: auto;
  color: #315dcf;
  font-size: 12px;
}
.attention-table :deep(table.spec-table) {
  min-width: 0;
  table-layout: fixed;
  border-collapse: separate;
  border-spacing: 0;
}
.attention-table :deep(thead th:first-child) { width: 27%; }
.attention-table :deep(thead th:last-child) { width: 112px; }
.attention-table :deep(:is(th, td)) { white-space: normal; overflow-wrap: anywhere; }
.attention-table :deep(tbody th) {
  position: sticky;
  inset-inline-start: 0;
  z-index: 1;
  background: #fff;
  text-align: left;
  font-weight: 400;
  vertical-align: top;
}
.attention-table :deep(tbody th > a) {
  display: block;
  font-size: 12px;
  font-weight: 550;
  overflow-wrap: anywhere;
}
.attention-table :deep(td) {
  vertical-align: top;
}
.attention-questions {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 4px;
}
.attention-questions li {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 2px 8px;
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
  color: var(--brand-color-text-secondary);
  font-size: 11px;
}
.attention-action {
  font-size: 12px;
  color: #315dcf;
}
.attention-action span {
  display: inline-block;
  font-size: 11px;
  white-space: nowrap;
}
.attention-empty {
  border: 0;
  padding: 14px 10px;
}
.reminder-help {
  margin-top: 2px;
  color: var(--brand-color-text-secondary);
  font-size: 11px;
}
.reminder-help :deep([data-slot='collapsible-trigger']) { min-height: 28px; }
.reminder-help p {
  max-width: 760px;
  margin-top: 8px;
  line-height: 1.8;
}
@media (width<720px) {
  .attention-table :deep(thead th:first-child) {
    width: 30%;
  }
  .attention-table :deep(thead th:last-child) {
    width: 25%;
  }
  .attention-zero a {
    margin-left: 0;
  }
}
</style>
