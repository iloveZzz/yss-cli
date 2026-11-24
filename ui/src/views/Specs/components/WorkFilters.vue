<script setup lang="ts">
import { computed, nextTick, shallowRef, useId, useTemplateRef } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { ListFilter } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Input } from '@/components/ui/input'
import {
  stages,
  statuses,
  contentStatuses,
  type WorkQuery,
  type ProjectView,
  type SpecView
} from '@/services/workbench'
import FilterPopover from './FilterPopover.vue'

const props = defineProps<{
  query: WorkQuery
  projects: ProjectView[]
  specs: SpecView[]
  embedded?: boolean
}>()
const emit = defineEmits<{
  update: [patch: Partial<WorkQuery>]
  toggle: [
    key: 'projectIds' | 'stages' | 'statuses' | 'contentStatuses' | 'reminderKinds' | 'attention',
    value: string,
    checked: boolean
  ]
  clear: []
}>()
const id = useId()
const desktopFilters = useMediaQuery('(min-width: 720px)')
const mobileFiltersOpen = shallowRef(false)
const filtersOpen = computed({
  get: () => desktopFilters.value || mobileFiltersOpen.value,
  set: (open) => { mobileFiltersOpen.value = open }
})
const root = useTemplateRef<HTMLElement>('root')
const chipList = useTemplateRef<HTMLDivElement>('chipList')
const groups = computed(() => [
  {
    key: 'attention' as const,
    label: '进展提醒',
    options: [
      { value: 'awaitingConfirmation', label: '待确认' },
      { value: 'unverified', label: '未核实' },
      { value: 'needsSync', label: '待同步' },
      { value: 'needsReview', label: '需复核' },
      { value: 'conflict', label: '有冲突' }
    ]
  },
  {
    key: 'projectIds' as const,
    label: '工程',
    options: props.projects.map((p) => ({
      value: p.record.id,
      label: `${p.record.name}${p.record.active ? '' : '（停用）'}`
    }))
  },
  {
    key: 'stages' as const,
    label: '阶段',
    options: Object.entries(stages).map(([value, label]) => ({ value, label }))
  },
  {
    key: 'statuses' as const,
    label: '状态',
    options: Object.entries(statuses).map(([value, label]) => ({ value, label }))
  },
  {
    key: 'contentStatuses' as const,
    label: '内容状态',
    options: Object.entries(contentStatuses).map(([value, label]) => ({ value, label }))
  },
  {
    key: 'reminderKinds' as const,
    label: '内容待办',
    options: [
      { value: 'decision', label: '待你决定' },
      { value: 'research', label: '待补充' },
      { value: 'overdue', label: '需要复查' },
      { value: 'writeback', label: '待回写' },
      { value: 'ready', label: '可确认' }
    ]
  }
])
const extraCount = computed(
  () =>
    Number(props.query.activity !== 'active') +
    Number(props.query.includeClosed) +
    Number(props.query.sourceProblemsOnly) +
    Number(props.query.needsReconfirmation)
)
const chips = computed(() => {
  const query = props.query
  const values: { key: string; label: string; missing?: boolean; patch: Partial<WorkQuery> }[] = []
  if (query.q) values.push({ key: 'q', label: `搜索：${query.q}`, patch: { q: '' } })
  for (const group of groups.value) {
    for (const value of query[group.key]) {
      const option = group.options.find((option) => option.value === value)
      values.push({
        key: `${group.key}:${value}`,
        label: `${group.label}：${option?.label ?? `已失效（${value}）`}`,
        missing: !option,
        patch: { [group.key]: query[group.key].filter((item) => item !== value) }
      })
    }
  }
  for (const value of query.specIds) {
    const spec = props.specs.find((spec) => spec.record.id === value)
    values.push({
      key: `spec:${value}`,
      label: `Spec：${spec?.record.title ?? `已失效（${value}）`}`,
      missing: !spec,
      patch: { specIds: query.specIds.filter((item) => item !== value) }
    })
  }
  if (query.activity !== 'active')
    values.push({
      key: 'activity',
      label: query.activity === 'all' ? '全部记录' : '停用记录',
      patch: { activity: 'active' }
    })
  if (query.includeClosed)
    values.push({ key: 'closed', label: '含已完成／已取消', patch: { includeClosed: false } })
  if (query.needsReconfirmation)
    values.push({ key: 'reconfirm', label: '新版待确认', patch: { needsReconfirmation: false } })
  if (query.sourceProblemsOnly)
    values.push({ key: 'source', label: '仅来源异常', patch: { sourceProblemsOnly: false } })
  return values
})

function summary(group: (typeof groups.value)[number]) {
  const selected = props.query[group.key]
  return selected.length === 0
    ? '全部'
    : selected.length === 1
      ? (group.options.find((option) => option.value === selected[0])?.label ?? '已失效')
      : undefined
}
async function remove(index: number) {
  const chip = chips.value[index]
  if (!chip) return
  emit('update', chip.patch)
  await nextTick()
  const buttons = chipList.value?.querySelectorAll<HTMLButtonElement>('.filter-chip')
  const next = buttons?.[Math.min(index, buttons.length - 1)]
  const target = next ?? root.value?.querySelector<HTMLInputElement>('input[type=search]')
  target?.focus()
}
async function clear() {
  emit('clear')
  await nextTick()
  root.value?.querySelector<HTMLInputElement>('input[type=search]')?.focus()
}
</script>

<template>
  <div ref="root" role="search" class="work-filter-panel" :class="{ embedded }" aria-label="筛选 Spec">
    <div class="filter-main">
      <div class="search-field">
        <label :for="id + '-search'" class="search-label">搜索</label>
        <div class="search-control">
          <svg width="16" height="16" viewBox="0 0 20 20" fill="none" aria-hidden="true">
            <circle cx="8.5" cy="8.5" r="5.5" stroke="currentColor" stroke-width="1.5" />
            <path d="m13 13 4 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
          </svg>
          <Input
            :id="id + '-search'"
            type="search"
            name="q"
            :model-value="query.q"
            :aria-describedby="id + '-hint'"
            placeholder="搜索标题、工程、路径、下一步、阻塞或备注"
            class="work-search"
            autocorrect="off"
            autocapitalize="none"
            :spellcheck="false"
            @update:model-value="emit('update', { q: String($event) })"
          />
        </div>
      </div>
      <Collapsible v-model:open="filtersOpen" class="filter-picker">
        <CollapsibleTrigger v-if="!desktopFilters" as-child>
          <Button type="button" variant="outline" class="filter-options-toggle">
            <ListFilter :size="16" aria-hidden="true" />
            {{ filtersOpen ? '收起筛选条件' : '展开筛选条件' }}
          </Button>
        </CollapsibleTrigger>
        <CollapsibleContent class="filter-selects">
          <FilterPopover
            v-for="group in groups"
            :key="group.key"
            :label="group.label"
            :summary="summary(group)"
            :count="query[group.key].length"
            clearable
            @clear="emit('update', { [group.key]: [] })"
          >
            <div v-if="!group.options.length" class="option-empty">暂无可选工程</div>
            <label
              v-for="option in group.options"
              :key="option.value"
              :for="`${id}-${group.key}-${option.value}`"
              class="filter-option"
              :class="{ checked: (query[group.key] as string[]).includes(option.value) }"
            >
              <Checkbox
                :id="`${id}-${group.key}-${option.value}`"
                :model-value="(query[group.key] as string[]).includes(option.value)"
                @update:model-value="emit('toggle', group.key, option.value, $event === true)"
              />
              <span>{{ option.label }}</span>
            </label>
          </FilterPopover>
          <FilterPopover
            label="更多条件"
            :count="extraCount"
            :clearable="extraCount > 0"
            @clear="
              emit('update', {
                activity: 'active',
                includeClosed: false,
                sourceProblemsOnly: false,
                needsReconfirmation: false
              })
            "
          >
            <fieldset class="scope-options">
              <legend :id="id + '-activity'">启用范围</legend>
              <RadioGroup
                :model-value="query.activity"
                :aria-labelledby="id + '-activity'"
                class="scope-radio-group"
                @update:model-value="emit('update', { activity: $event as WorkQuery['activity'] })"
              >
                <label
                  v-for="option in [
                    { value: 'active', label: '启用记录' },
                    { value: 'inactive', label: '停用记录' },
                    { value: 'all', label: '全部记录' }
                  ]"
                  :key="option.value"
                  :for="`${id}-${option.value}`"
                  class="filter-option"
                >
                  <RadioGroupItem :id="`${id}-${option.value}`" :value="option.value" />
                  <span>{{ option.label }}</span>
                </label>
              </RadioGroup>
            </fieldset>
            <div class="extra-options">
              <label :for="id + '-closed'" class="filter-option">
                <Checkbox
                  :id="id + '-closed'"
                  :model-value="query.includeClosed"
                  @update:model-value="emit('update', { includeClosed: $event === true })"
                />
                <span>显示已完成／已取消</span>
              </label>
              <label :for="id + '-source'" class="filter-option">
                <Checkbox
                  :id="id + '-source'"
                  :model-value="query.sourceProblemsOnly"
                  @update:model-value="emit('update', { sourceProblemsOnly: $event === true })"
                />
                <span>仅来源异常</span>
              </label>
              <label :for="id + '-reconfirmation'" class="filter-option">
                <Checkbox
                  :id="id + '-reconfirmation'"
                  :model-value="query.needsReconfirmation"
                  @update:model-value="emit('update', { needsReconfirmation: $event === true })"
                />
                <span>新版待确认（含已完成）</span>
              </label>
            </div>
          </FilterPopover>
        </CollapsibleContent>
      </Collapsible>
    </div>
    <div v-if="chips.length" ref="chipList" class="selected-filters" aria-label="已选条件">
      <span class="selected-label">已选</span>
      <Button
        v-for="(chip, index) in chips"
        :key="chip.key"
        type="button"
        variant="outline"
        size="xs"
        class="filter-chip"
        :class="{ missing: chip.missing }"
        :aria-label="`移除${chip.label}`"
        @click="remove(index)"
      >
        <span>{{ chip.label }}</span>
        <span aria-hidden="true">×</span>
      </Button>
      <Button type="button" variant="ghost" class="clear-filters" @click="clear">清空条件</Button>
    </div>
    <p :id="id + '-hint'" class="filter-hint">
      条件实时生效
      <span v-if="!chips.length">· 默认显示启用且未关闭的记录</span>
    </p>
  </div>
</template>

<style scoped>
.work-filter-panel {
  background: #fff;
  border: 1px solid var(--yss-colors-border);
  border-radius: 12px;
  padding: 16px;
  margin-bottom: 14px;
  min-width: 0;
}
.work-filter-panel.embedded {
  border: 0;
  border-radius: 0;
  margin: 0;
}
.filter-main {
  display: flex;
  align-items: flex-end;
  flex-wrap: wrap;
  gap: 12px 16px;
}
.search-field {
  flex: 1 1 280px;
  min-width: 0;
}
.search-label {
  display: block;
  margin-bottom: 6px;
  color: #555d6a;
  font-size: 12px;
  font-weight: 500;
}
.search-control {
  position: relative;
}
.search-control > svg {
  position: absolute;
  left: 10px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--brand-color-text-secondary);
  pointer-events: none;
}
.work-search {
  width: 100%;
  height: 34px;
  padding-left: 33px;
  background: #fafbfc;
  border-color: var(--yss-colors-border-control);
}
.filter-picker {
  min-width: 0;
}
.filter-selects {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  min-width: 0;
}
.filter-option {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 9px 8px;
  border-radius: 6px;
  cursor: pointer;
  overflow-wrap: anywhere;
  font-size: 13px;
}
.filter-option:hover,
.filter-option:focus-within {
  background: #f4f6fa;
}
.filter-option.checked {
  color: #315dcf;
  background: #f3f6ff;
}
.filter-option :is([data-slot="checkbox"], [data-slot="radio-group-item"]) {
  flex: none;
}
.scope-radio-group {
  gap: 0;
}
.filter-option > span {
  min-width: 0;
}
.scope-options {
  border: 0;
  margin: 0;
  padding: 0;
  min-width: 0;
}
.scope-options legend {
  padding: 6px 8px 3px;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.extra-options {
  border-top: 1px solid #edf0f4;
  margin-top: 6px;
  padding-top: 6px;
}
.option-empty {
  padding: 16px 8px;
  color: var(--brand-color-text-secondary);
}
.selected-filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  border-top: 1px solid #edf0f4;
  margin-top: 13px;
  padding-top: 12px;
  min-width: 0;
}
.selected-label {
  color: var(--brand-color-text-secondary);
  font-size: 11px;
  margin-right: 2px;
}
.filter-chip {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  max-width: 100%;
  min-width: 0;
  border: 1px solid #dce5fa;
  border-radius: 6px;
  background: #f3f6ff;
  color: #4564a4;
  padding: 3px 7px;
  text-align: left;
  font-size: 11px;
  line-height: 1.6;
}
.filter-chip > span:first-child {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.filter-chip > span:last-child {
  flex: none;
  font-size: 15px;
  line-height: 1;
}
.filter-chip:hover {
  border-color: #9fb5e9;
  background: #eaf0ff;
}
.filter-chip.missing {
  color: #96601e;
  background: #fff8ec;
  border-color: #ecd6b3;
}
.clear-filters {
  height: 26px;
  min-height: 26px;
  padding: 0 6px;
  color: var(--brand-color-text-secondary);
  font-size: 11px;
}
.filter-hint {
  margin-top: 10px;
  color: var(--brand-color-text-secondary);
  font-size: 11px;
}
@media (width < 720px) {
  .work-filter-panel {
    padding: 12px;
  }
  .search-field {
    flex-basis: 100%;
  }
  .work-search {
    min-height: 44px;
    font-size: 16px;
  }
  .filter-picker {
    flex-basis: 100%;
  }
  .filter-options-toggle {
    min-height: 44px;
  }
  .filter-selects {
    gap: 7px;
    margin-top: 8px;
  }
  .filter-selects[hidden] {
    display: none;
  }
  .filter-option,
  .filter-chip,
  .clear-filters {
    min-height: 44px;
  }
  .filter-chip {
    min-height: 36px;
  }
  .clear-filters {
    min-height: 40px;
  }
  .filter-hint {
    line-height: 1.7;
  }
}
</style>
