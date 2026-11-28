<script setup lang="ts">
import { ChevronRight } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import type { IssueSummary } from '@/services/workbench-issues'
import { issueDate } from '../schemas/issues'
import IssueState from './IssueState.vue'

defineProps<{ rows: IssueSummary[]; sourceId: string }>()
const emit = defineEmits<{ open: [number: number, trigger: HTMLElement | null] }>()

function openRow(issue: IssueSummary, event: MouseEvent) {
  const row = event.currentTarget
  emit('open', issue.number, row instanceof HTMLElement ? row.querySelector('button') : null)
}

function openTitle(issue: IssueSummary, event: MouseEvent) {
  emit('open', issue.number, event.currentTarget instanceof HTMLElement ? event.currentTarget : null)
}
</script>

<template>
  <Table class="issue-table" role="table" aria-label="Issues 列表">
    <TableHeader role="rowgroup">
      <TableRow role="row">
        <TableHead scope="col" role="columnheader">Issue</TableHead>
        <TableHead scope="col" role="columnheader">标签</TableHead>
        <TableHead scope="col" role="columnheader">状态</TableHead>
        <TableHead scope="col" role="columnheader">更新时间</TableHead>
      </TableRow>
    </TableHeader>
    <TableBody role="rowgroup">
      <TableRow v-for="issue in rows" :key="sourceId + '/' + issue.number" class="issue-row" role="row" @click="openRow(issue, $event)">
        <TableCell class="issue-title-cell" role="cell">
          <Button type="button" variant="ghost" class="issue-title" aria-haspopup="dialog" @click.stop="openTitle(issue, $event)">
            <span>{{ issue.title || '标题不可用' }}</span><small>#{{ issue.number }}</small>
          </Button>
        </TableCell>
        <TableCell class="issue-label-cell" role="cell">
          <span class="issue-labels">
            <Badge v-for="label in issue.labels" :key="label" variant="outline">{{ label }}</Badge>
            <span v-if="!issue.labels.length" aria-label="无标签">—</span>
          </span>
        </TableCell>
        <TableCell class="issue-state-cell" role="cell"><IssueState :status="issue.status" /></TableCell>
        <TableCell class="issue-updated" role="cell">
          <div>
            <time v-if="issue.updatedAt" :datetime="issue.updatedAt">{{ issueDate(issue.updatedAt) }}</time>
            <span v-else>时间不可用</span>
            <ChevronRight aria-hidden="true" />
          </div>
        </TableCell>
      </TableRow>
    </TableBody>
  </Table>
</template>

<style scoped>
.issue-table { width: 100%; table-layout: fixed; border-collapse: collapse; }
.issue-table :deep(:is(th, td)) { padding: 7px 12px; text-align: start; }
.issue-table :deep(:is(th, td):first-child) { padding-inline-start: 20px; }
.issue-table :deep(:is(th, td):last-child) { padding-inline-end: 20px; }
.issue-table :deep(th) { height: 34px; border-block: 1px solid var(--yss-colors-border); background: var(--brand-color-bg-layout); color: var(--brand-color-text-secondary); font-size: 11px; font-weight: 500; }
.issue-table :deep(th:nth-child(2)) { width: 21%; }
.issue-table :deep(th:nth-child(3)) { width: 90px; }
.issue-table :deep(th:nth-child(4)) { width: 160px; }
.issue-table :deep(td) { height: 53px; color: var(--brand-color-text-secondary); font-size: 12px; white-space: normal; }
.issue-row { border-bottom: 1px solid var(--yss-colors-border); cursor: pointer; }
.issue-row:hover, .issue-row:focus-within { background: var(--yss-colors-primary-bg); }
.issue-title[data-slot='button'] { flex-direction: column; align-items: flex-start; justify-content: center; width: 100%; height: auto; gap: 3px; padding: 0; border-radius: 4px; background: transparent; color: var(--yss-colors-text-title); font-size: 13px; font-weight: 550; text-align: start; white-space: normal; overflow-wrap: anywhere; }
.issue-title small, .issue-table .issue-updated { color: var(--brand-color-text-secondary); font-size: 11px; font-weight: 400; font-variant-numeric: tabular-nums; }
.issue-updated > div { display: flex; align-items: center; justify-content: space-between; gap: 4px; }
.issue-updated svg { width: 12px; height: 16px; color: var(--brand-color-text-secondary); }
.issue-labels { display: flex; flex-wrap: wrap; align-items: center; gap: 4px; }
.issue-labels :deep([data-slot='badge']) { max-width: 100%; padding: 1px 6px; border-color: var(--yss-colors-border); border-radius: 5px; background: var(--brand-color-bg-layout); color: var(--brand-color-text-secondary); font-size: 11px; white-space: normal; overflow-wrap: anywhere; }
@container issue-panel (width < 680px) {
  .issue-table :deep(thead) { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
  .issue-table, .issue-table :deep(tbody) { display: block; }
  .issue-table :deep(tbody) { border-block-start: 1px solid var(--yss-colors-border); }
  .issue-table .issue-row { display: grid; grid-template-columns: max-content minmax(0, 1fr); align-items: center; gap: 6px 12px; padding: 10px 16px; }
  .issue-table .issue-row :deep(td) { min-width: 0; height: auto; padding: 0; }
  .issue-title-cell, .issue-updated { grid-column: 1 / -1; }
  .issue-state-cell { grid-column: 1; grid-row: 2; }
  .issue-label-cell { grid-column: 2; grid-row: 2; }
  .issue-updated time::before { content: '更新于 ' / ''; }
  .issue-updated svg { display: none; }
}
@container issue-panel (width < 440px) {
  .issue-table .issue-row { padding-inline: 12px; }
}
</style>
